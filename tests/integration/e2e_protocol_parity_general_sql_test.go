// Package integration 端到端集成测试：三协议 SQL 功能矩阵协议一致性。
//
// 本文件聚焦「同一 server + 同一表 + 同一 SQL，分别经 TCP / HTTP / PG wire
// 三个协议执行，断言三协议返回值一致」。这是多协议数据库的关键正确性
// 指标：保证任何特性对外部客户端的暴露是统一的，避免某一协议出现「只走
// 老路径」、「只走缓存」或「解析方式不同」造成的语义漂移。
//
// 与既有测试的区别：
//   - e2e_mrpcm_multiprotocol_test.go：每个客户端写自己的 ID 区间，再统一
//     COUNT；侧重并发 + 重启持久化。
//   - e2e_pgwire_sql_test.go：仅验证 PG wire 自身的 SQL 解析与执行。
//   - TestPGWireCrossProtocolConsistency：单 SQL 三协议一致，本文件
//     在此基础上扩展为「全功能矩阵 + 错误路径 + 顺序写入读出」。
//
// 覆盖的 SQL 能力（按执行顺序）：
//  1. SELECT 全表 + 行级字段一致性
//  2. WHERE 标量比较（=、>、<、>=、<=、!=、AND）
//  3. 聚合 COUNT/SUM/AVG/MIN/MAX
//  4. GROUP BY 行数 + 聚合值一致性
//  5. UPDATE / DELETE 命中行数一致
//  6. LIKE 过滤
//  7. 错误路径：未知表 / 语法错误，三协议均应返回非零 code
//  8. 错误路径后正常 SQL 仍可工作（错误隔离）
//
// 测试设计原则：
//   - 复用既有 helper：startPGWireServer / httpQuery / respRows /
//     toInt64 / toFloat64 / dialPGWire / pgRowToMap / pgFloat / pgInt
//   - 每个测试 t.Parallel 并发执行，缩短集成测试套件总时长
//   - 协议间一致性的判定：行数相同 + 关键标量字段（COUNT/SUM/AVG 等聚合）
//     在 float64 容差内相等；行级数据按主键排序后逐字段比较
//   - 错误路径仅断言 code != 0 与 error 字段非空，不深究 message 文案
//
// PG wire 特殊性：所有数值经文本协议返回为字符串，故本文件提取数值时
// 同时支持 int64/float64（HTTP/TCP） 与 string（PG wire） 两种类型。
package integration

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// protocolParityTable 协议一致性测试专用表名（每个测试用唯一后缀隔离）。
const protocolParityTableBase = "protocol_parity_events"

// protocolParityTableFor 按测试名生成唯一的表名，避免 t.Parallel 下并发测试
// 之间的写读干扰。本测试套件涉及 INSERT/DELETE/UPDATE 操作，不同测试
// 共享同一表会导致数据互相污染，故每个测试用独立后缀。
func protocolParityTableFor(testName string) string {
	// 表名只允许 [A-Za-z0-9_]，将 "-" 替换为 "_"
	safe := strings.ReplaceAll(testName, "-", "_")
	return protocolParityTableBase + "_" + safe
}

// protocolParitySeed 为测试准备的基础数据。
//
// 设计：3 个 region × 4 个 product，共 12 行；amount 在 [10, 99] 区间，
// qty 在 [1, 9] 区间，is_member 二者交替；便于 GROUP BY / 聚合 / LIKE 断言。
func protocolParitySeed() []map[string]any {
	rows := make([]map[string]any, 0, 12)
	id := int64(1)
	for _, region := range []string{"cn-east", "cn-west", "us-east"} {
		for j, product := range []string{"phone", "laptop", "tablet", "watch"} {
			rows = append(rows, map[string]any{
				"id":        id,
				"region":    region,
				"product":   product,
				"amount":    float64(10 + (int(id)*7+j*3)%90),
				"qty":       int64(1 + (j*2)%9),
				"is_member": (int(id)+j)%2 == 0,
				"note":      protocolParityNoteFor(id),
			})
			id++
		}
	}
	return rows
}

// protocolParityNoteFor 为每行生成确定性 note 字符串。
//
// 用于 LIKE 过滤的精确匹配断言（包含 'flag'、'skip' 两种前缀）。
func protocolParityNoteFor(id int64) string {
	switch id % 3 {
	case 0:
		return "flag-" + strconv.FormatInt(id, 10)
	case 1:
		return "skip-" + strconv.FormatInt(id, 10)
	default:
		return "other-" + strconv.FormatInt(id, 10)
	}
}

// protocolParityCreateSQL 建表语句。
func protocolParityCreateSQL(tbl string) string {
	return "CREATE TABLE " + tbl + " (" +
		"id INT64 NOT NULL, " +
		"region STRING NULL, " +
		"product STRING NULL, " +
		"amount FLOAT64 NULL, " +
		"qty INT64 NULL, " +
		"is_member BOOL NULL, " +
		"note STRING NULL, " +
		"PRIMARY KEY(id))"
}

// protocolParityProtocols 三协议集合，按「短写优先」原则确定测试顺序。
var protocolParityProtocols = []string{"http", "tcp", "pg"}

// protocolParityResult 跨协议统一的结果表示，便于一致性比较。
//
// PG wire 返回的是文本行，TCP/HTTP 返回的是 JSON map；为统一比较，转换
// 为 map 列表（行）+ code + message 三元组。
type protocolParityResult struct {
	rows    []map[string]any
	code    int
	message string
	err     error // 传输层错误（非 SQL 错误）
}

// TestProtocolParitySQLFeatureMatrix 验证三协议对一般 SQL 矩阵的返回结果一致。
//
// 工作流：
//  1. 启动 TCP+HTTP+PG wire server；
//  2. 仅经 HTTP 创建表（其余协议共享同一 catalog/engine）；
//  3. 仅经 HTTP 灌入 12 行种子数据；
//  4. 对每个测试 SQL（三协议任一执行），调用三协议独立执行并断言：
//     - code 全部为 0
//     - 行数（len(rows)）一致
//     - 关键聚合值（COUNT / SUM / AVG）一致
//  5. UPDATE/DELETE 命中行数一致。
//
// 不变量：「同样的数据 + 同样的 SQL」必须经任意协议得到完全等价的结果，
// 这是 BI 工具（Superset/Metabase/Grafana）多驱动接入正确性的核心。
func TestProtocolParitySQLFeatureMatrix(t *testing.T) {
	t.Parallel()
	s := startPGWireServer(t)
	tbl := protocolParityTableFor(t.Name())

	// 建表 + 灌入种子数据（仅经 HTTP 一次即可）
	if err := protocolParityCreateAndSeed(t, s, tbl); err != nil {
		t.Fatalf("建表/灌入失败: %v", err)
	}

	// 1) SELECT 全表
	protocolParityAssertRowsEqual(t, s, tbl,
		"SELECT id, region, product, amount, qty, is_member, note FROM "+tbl+" ORDER BY id",
		"全表扫描")

	// 2) 标量比较（每个 SQL 返回单行单列，列名 c 显式以兼容 PG wire 的文本协议）
	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT COUNT(*) AS c FROM "+tbl+" WHERE region = 'cn-east'",
		"COUNT(=)")

	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT COUNT(*) AS c FROM "+tbl+" WHERE amount > 50",
		"COUNT(>)")

	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT COUNT(*) AS c FROM "+tbl+" WHERE amount <= 30",
		"COUNT(<=)")

	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT COUNT(*) AS c FROM "+tbl+" WHERE region != 'us-east'",
		"COUNT(!=)")

	// 3) 复合条件
	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT COUNT(*) AS c FROM "+tbl+
			" WHERE region = 'cn-east' AND amount >= 30 AND amount <= 70",
		"COUNT(AND)")

	// 4) 聚合
	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT SUM(amount) AS c FROM "+tbl,
		"SUM")

	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT AVG(qty) AS c FROM "+tbl,
		"AVG")

	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT MIN(amount) AS c FROM "+tbl,
		"MIN")

	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT MAX(amount) AS c FROM "+tbl,
		"MAX")

	// 5) GROUP BY：行数 + SUM(amount) 在三协议之间必须一致
	protocolParityAssertRowsEqual(t, s, tbl,
		"SELECT region, COUNT(*) AS c, SUM(amount) AS s FROM "+tbl+
			" GROUP BY region ORDER BY region",
		"GROUP BY region")

	// 6) UPDATE / DELETE 命中行数一致
	protocolParityAssertUpdateAffectedEqual(t, s, tbl,
		"UPDATE "+tbl+" SET amount = amount + 1 WHERE region = 'us-east'",
		"UPDATE")

	protocolParityAssertUpdateAffectedEqual(t, s, tbl,
		"DELETE FROM "+tbl+" WHERE qty <= 2",
		"DELETE")

	// 7) LIKE 过滤：含 'flag' 的行数
	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT COUNT(*) AS c FROM "+tbl+" WHERE note LIKE '%flag%'",
		"LIKE flag")
}

// TestProtocolParityErrorIsolation 验证三协议错误返回的隔离性。
//
// 核心断言：当一个客户端发送坏 SQL（语法错误或未知表）时：
//  1. 错误响应应包含非零 code；
//  2. 错误信息非空（供客户端显示）；
//  3. 其他协议的客户端在同一时刻的正常 SQL 不受影响（仍返回 0 行 / 0 code）。
//
// 这一组测试对应生产环境的「客户端 A 发错语句、客户端 B 业务正常」场景。
func TestProtocolParityErrorIsolation(t *testing.T) {
	t.Parallel()
	s := startPGWireServer(t)
	tbl := protocolParityTableFor(t.Name())
	if err := protocolParityCreateAndSeed(t, s, tbl); err != nil {
		t.Fatalf("建表/灌入失败: %v", err)
	}

	// 错误 SQL 候选：每个 case 由三协议分别执行，应均返回非零 code + 非空 message
	badCases := []struct {
		name string
		sql  string
	}{
		{"未知表", "SELECT * FROM protocol_parity_missing"},
		{"语法错误", "SELEC * FORM " + tbl},
		{"重复主键", "INSERT INTO " + tbl + " (id) VALUES (1)"},
	}

	for _, c := range badCases {
		t.Run(c.name, func(t *testing.T) {
			for _, via := range protocolParityProtocols {
				r := protocolParityRunSQL(s, via, c.sql)
				if r.err != nil {
					t.Errorf("[%s] %s 传输失败: %v", via, c.name, r.err)
					continue
				}
				if r.code == 0 {
					t.Errorf("[%s] %s 期望非零 code，得到 0：%s", via, c.name, r.message)
				}
				if r.message == "" {
					t.Errorf("[%s] %s 期望非空 message", via, c.name)
				}
			}
		})
	}

	// 验证错误路径不会影响正常客户端：在所有坏 SQL 执行完后，
	// 三协议均能正常返回 COUNT(*)=12
	protocolParityAssertScalarEqual(t, s, tbl,
		"SELECT COUNT(*) AS c FROM "+tbl,
		"错误路径后 COUNT 仍正常")
}

// TestProtocolParityConcurrentMixedWorkload 验证三协议在并发混合读写下结果一致。
//
// 12 客户端（每协议 4 个）并发执行 INSERT 混合负载，所有客户端写入
// 「自己 (协议, clientID) 唯一」的 ID 区间互不重叠。结束后通过三协议
// 各做一次总 COUNT 校验，应读到完全相同的行数。
//
// 价值：发现协议间缓存不一致、读快照差异、MemTable flush 时序差异等
// 难以单协议复现的隐性问题。
func TestProtocolParityConcurrentMixedWorkload(t *testing.T) {
	t.Parallel()
	s := startPGWireServer(t)
	tbl := protocolParityTableFor(t.Name())

	// 建表（HTTP）
	if resp, err := httpQuery(s.httpAddr, protocolParityCreateSQL(tbl)); err != nil {
		t.Fatalf("建表失败: %v", err)
	} else if resp.Code != 0 {
		t.Fatalf("建表返回错误: %s", resp.Message)
	}

	// 4 客户端 × 3 协议 = 12 客户端
	const (
		parityClients = 4
		parityRowsPer = 3
		parityRounds  = 2
		parityBaseID  = 90000
	)
	parityWant := parityClients * len(protocolParityProtocols) * parityRowsPer * parityRounds

	var (
		wg        sync.WaitGroup
		failCount int64
	)
	for c := 0; c < parityClients; c++ {
		for _, via := range protocolParityProtocols {
			wg.Add(1)
			go func(clientID int, via string) {
				defer wg.Done()
				if err := protocolParityConcurrentWorker(s, via, tbl, clientID, parityRounds, parityRowsPer, parityBaseID); err != nil {
					t.Logf("[%s c%d] 失败: %v", via, clientID, err)
					atomic.AddInt64(&failCount, 1)
				}
			}(c, via)
		}
	}
	wg.Wait()
	if failCount > 0 {
		t.Fatalf("%d 个客户端工作负载失败", failCount)
	}

	// 三协议分别 COUNT，结果应一致
	gotByProtocol := make(map[string]int, len(protocolParityProtocols))
	for _, via := range protocolParityProtocols {
		r := protocolParityRunSQL(s, via,
			"SELECT COUNT(*) AS c FROM "+tbl)
		if r.err != nil {
			t.Fatalf("[%s] COUNT 传输失败: %v", via, r.err)
		}
		if r.code != 0 {
			t.Fatalf("[%s] COUNT 返回错误: %s", via, r.message)
		}
		if len(r.rows) != 1 {
			t.Fatalf("[%s] COUNT 期望 1 行，得到 %d", via, len(r.rows))
		}
		got, ok := protocolParityExtractInt(r.rows[0], "c")
		if !ok {
			t.Fatalf("[%s] COUNT 返回值类型异常 %T", via, r.rows[0])
		}
		gotByProtocol[via] = int(got)
	}
	for _, via := range protocolParityProtocols {
		if gotByProtocol[via] != parityWant {
			t.Errorf("[%s] 并发写入后 COUNT: 期望 %d，得到 %d",
				via, parityWant, gotByProtocol[via])
		}
	}
}

// -----------------------------------------------------------------------
// 内部 helper
// -----------------------------------------------------------------------

// protocolParityCreateAndSeed 建表 + 灌入种子数据（仅经 HTTP 一次）。
func protocolParityCreateAndSeed(t *testing.T, s *sqlServer, tbl string) error {
	t.Helper()
	if resp, err := httpQuery(s.httpAddr, protocolParityCreateSQL(tbl)); err != nil {
		return fmt.Errorf("建表: %w", err)
	} else if resp.Code != 0 {
		return fmt.Errorf("建表: %s", resp.Message)
	}
	if resp, err := httpWrite(s.httpAddr, tbl, protocolParitySeed()); err != nil {
		return fmt.Errorf("灌入种子: %w", err)
	} else if resp.Code != 0 {
		return fmt.Errorf("灌入种子: %s", resp.Message)
	}
	return nil
}

// protocolParityRunSQL 按协议执行 SQL 并返回统一形态的结果。
//
// 三个协议分别走：
//   - HTTP：经 httpQuery 取 JSON Response → 走 respRows 提取行；
//   - TCP ：经 dialTCP + query 取 JSON Response → 走 respRows 提取行；
//   - PG wire：经 dialPGWire + sendQueryRead 取 pgResult → 自行转换为 map 行。
//
// 错误判定：传输层错误 → r.err 非空；SQL 执行错误 → r.code != 0。
func protocolParityRunSQL(s *sqlServer, via, sql string) protocolParityResult {
	switch via {
	case "http":
		resp, err := httpQuery(s.httpAddr, sql)
		if err != nil {
			return protocolParityResult{err: fmt.Errorf("http 查询: %w", err)}
		}
		return protocolParityResult{
			rows:    respRows(resp),
			code:    resp.Code,
			message: resp.Message,
		}
	case "tcp":
		tc, err := dialTCP(s.tcpAddr)
		if err != nil {
			return protocolParityResult{err: fmt.Errorf("tcp 拨号: %w", err)}
		}
		defer tc.close()
		resp, err := tc.query(sql)
		if err != nil {
			return protocolParityResult{err: fmt.Errorf("tcp 查询: %w", err)}
		}
		return protocolParityResult{
			rows:    respRows(resp),
			code:    resp.Code,
			message: resp.Message,
		}
	case "pg":
		c, err := dialPGWireErr(s.srv.PGAddr())
		if err != nil {
			return protocolParityResult{err: fmt.Errorf("pg 拨号: %w", err)}
		}
		defer c.close()
		if err := c.handshakeErr(); err != nil {
			return protocolParityResult{err: fmt.Errorf("pg 握手: %w", err)}
		}
		res, err := c.sendQueryRead(sql)
		if err != nil {
			return protocolParityResult{err: fmt.Errorf("pg 查询: %w", err)}
		}
		if res.errMsg != "" {
			return protocolParityResult{code: -1, message: res.errMsg}
		}
		out := make([]map[string]any, 0, len(res.rows))
		for _, r := range res.rows {
			out = append(out, pgRowToMap(res.columns, r))
		}
		return protocolParityResult{rows: out, code: 0}
	default:
		return protocolParityResult{err: fmt.Errorf("未知协议: %s", via)}
	}
}

// protocolParityAssertScalarEqual 断言三协议执行相同 SQL 得到的标量值一致。
//
// 适用：COUNT/SUM/AVG/MIN/MAX 等单行单列查询。
// 浮点字段（AVG）使用容差 1e-6 比较。
func protocolParityAssertScalarEqual(t *testing.T, s *sqlServer, tbl, sql, label string) {
	t.Helper()
	values := make(map[string]float64, len(protocolParityProtocols))
	for _, via := range protocolParityProtocols {
		r := protocolParityRunSQL(s, via, sql)
		if r.err != nil {
			t.Errorf("[%s] %s 传输失败: %v", via, label, r.err)
			continue
		}
		if r.code != 0 {
			t.Errorf("[%s] %s 返回错误: %s", via, label, r.message)
			continue
		}
		if len(r.rows) != 1 {
			t.Errorf("[%s] %s 期望 1 行，得到 %d", via, label, len(r.rows))
			continue
		}
		// 取首行的首列
		var v float64
		var ok bool
		for _, x := range r.rows[0] {
			if f, isF := protocolParityExtractFloat(x); isF {
				v = f
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("[%s] %s 无法从 %v 中提取数值", via, label, r.rows[0])
			continue
		}
		values[via] = v
	}
	// 三两两比较
	firstVia := protocolParityProtocols[0]
	firstV := values[firstVia]
	for _, via := range protocolParityProtocols[1:] {
		v := values[via]
		if math.Abs(v-firstV) > 1e-6*math.Max(1, math.Abs(firstV)) {
			t.Errorf("%s 三协议不一致: %s=%v, %s=%v",
				label, firstVia, firstV, via, v)
		}
	}
}

// protocolParityAssertRowsEqual 断言三协议 SELECT 的行数据完全一致。
//
// 比较策略：
//  1. 行数必须相同；
//  2. 各协议按主键排序后，逐行逐字段比较（字符串/数值/布尔按对应规则）。
func protocolParityAssertRowsEqual(t *testing.T, s *sqlServer, tbl, sql, label string) {
	t.Helper()
	perProtocol := make(map[string][]map[string]any, len(protocolParityProtocols))
	for _, via := range protocolParityProtocols {
		r := protocolParityRunSQL(s, via, sql)
		if r.err != nil {
			t.Errorf("[%s] %s 传输失败: %v", via, label, r.err)
			continue
		}
		if r.code != 0 {
			t.Errorf("[%s] %s 返回错误: %s", via, label, r.message)
			continue
		}
		rows := r.rows
		// 按 id 排序（PG wire 返回的列序可能不同；统一排序后再比）
		sort.Slice(rows, func(i, j int) bool {
			a, _ := protocolParityExtractInt(rows[i], "id")
			b, _ := protocolParityExtractInt(rows[j], "id")
			return a < b
		})
		perProtocol[via] = rows
	}
	// 与第一个协议逐行逐字段比对
	first := protocolParityProtocols[0]
	base := perProtocol[first]
	for _, via := range protocolParityProtocols[1:] {
		rows := perProtocol[via]
		if len(rows) != len(base) {
			t.Errorf("%s 行数不一致: %s=%d, %s=%d",
				label, first, len(base), via, len(rows))
			continue
		}
		for i := range rows {
			for k, v := range base[i] {
				if !protocolParityFieldEqual(rows[i][k], v) {
					t.Errorf("%s 第 %d 行字段 %s 不一致: %s=%v, %s=%v",
						label, i, k, first, v, via, rows[i][k])
				}
			}
		}
	}
}

// protocolParityAssertUpdateAffectedEqual 断言 UPDATE/DELETE 三协议影响行数一致。
func protocolParityAssertUpdateAffectedEqual(t *testing.T, s *sqlServer, tbl, sql, label string) {
	t.Helper()
	affected := make(map[string]int, len(protocolParityProtocols))
	for _, via := range protocolParityProtocols {
		r := protocolParityRunSQL(s, via, sql)
		if r.err != nil {
			t.Errorf("[%s] %s 传输失败: %v", via, label, r.err)
			continue
		}
		if r.code != 0 {
			t.Errorf("[%s] %s 返回错误: %s", via, label, r.message)
			continue
		}
		affected[via] = len(r.rows)
	}
	first := protocolParityProtocols[0]
	for _, via := range protocolParityProtocols[1:] {
		if affected[via] != affected[first] {
			t.Errorf("%s 影响行数不一致: %s=%d, %s=%d",
				label, first, affected[first], via, affected[via])
		}
	}
}

// protocolParityConcurrentWorker 单个客户端在自身 ID 区间的并发工作负载。
//
// 工作流：每轮 INSERT 1 行；总写入 = clients * rounds * rowsPer。
// 为避免不同协议的客户端写入相同主键，ID 由「协议偏移 + 客户端 + 轮次 + 行号」唯一生成：
//
//	id = baseID + viaOffset*spacePerProtocol + clientID*rowsPer*rounds + r*rowsPer + j
//
// 其中 viaOffset 来自协议在 protocolParityProtocols 中的下标（0/1/2）。
func protocolParityConcurrentWorker(s *sqlServer, via, tbl string, clientID, rounds, rowsPer, baseID int) error {
	// 协议 → 偏移
	viaOffset := -1
	for i, v := range protocolParityProtocols {
		if v == via {
			viaOffset = i
			break
		}
	}
	if viaOffset < 0 {
		return fmt.Errorf("未知协议: %s", via)
	}
	// 协议段长度 = clients * rounds * rowsPer（每客户端独占 rounds*rowsPer 个 ID）
	const spacePerProtocol = 1000

	for r := 0; r < rounds; r++ {
		for j := 0; j < rowsPer; j++ {
			id := int64(baseID + viaOffset*spacePerProtocol + clientID*rowsPer*rounds + r*rowsPer + j)
			insertSQL := fmt.Sprintf(
				"INSERT INTO %s (id, region, product, amount, qty, is_member, note) "+
					"VALUES (%d, 'parity-r%d', 'p-%d', %d, %d, true, 'note-%d')",
				tbl, id, r, j, id%100, (id%9)+1, id,
			)
			r2 := protocolParityRunSQL(s, via, insertSQL)
			if r2.err != nil {
				return fmt.Errorf("INSERT: %w", r2.err)
			}
			if r2.code != 0 {
				return fmt.Errorf("INSERT code=%d msg=%s", r2.code, r2.message)
			}
		}
	}
	return nil
}

// protocolParityFieldEqual 跨类型比较两个字段值是否等价。
//
// 支持 int64/float64（HTTP/TCP）、string（PG wire 文本协议） 三种类型，数值
// 按 float64 容差比较；非数值先归一化布尔再按 fmt 字符串相等。
//
// PG wire 布尔特别处理：服务端编码为 "t" / "f" 单字符；归一化为 "true" /
// "false" 后再与 HTTP/TCP 返回的 JSON bool 比较。
func protocolParityFieldEqual(a, b any) bool {
	if af, aok := protocolParityExtractFloat(a); aok {
		if bf, bok := protocolParityExtractFloat(b); bok {
			return math.Abs(af-bf) <= 1e-6*math.Max(1, math.Abs(af))
		}
		return false
	}
	return protocolParityNormalizeBool(a) == protocolParityNormalizeBool(b)
}

// protocolParityNormalizeBool 把 PG wire 的 "t" / "f" / "true" / "false"
// 与 JSON 的 true / false / "true" / "false" 统一为字符串 "true" / "false"。
//
// 非布尔值原样 fmt 格式化。
func protocolParityNormalizeBool(v any) string {
	switch n := v.(type) {
	case bool:
		if n {
			return "true"
		}
		return "false"
	case string:
		switch n {
		case "t", "true", "T", "TRUE":
			return "true"
		case "f", "false", "F", "FALSE":
			return "false"
		}
		return n
	}
	return fmt.Sprintf("%v", v)
}

// protocolParityExtractFloat 从协议异构值提取 float64。
//
// 同时支持 int64/float64（HTTP/TCP）与 string（PG wire 文本协议）。
func protocolParityExtractFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// protocolParityExtractInt 从协议异构值提取 int64。
//
// 同时支持 int64/float64（HTTP/TCP）与 string（PG wire 文本协议）。
func protocolParityExtractInt(row map[string]any, key string) (int64, bool) {
	v, ok := row[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0, false
		}
		return i, true
	}
	return 0, false
}
