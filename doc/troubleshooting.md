# WiDB 故障排查指南

> 本文档汇总常见错误场景、原因分析与解决方案。遇到未覆盖的问题时，请先查阅 [sql-reference.md](sql-reference.md) 与 [development.md](development.md)。

## 目录

- [1. 连接类](#1-连接类)
- [2. SQL 解析与执行类](#2-sql-解析与执行类)
- [3. 写入类](#3-写入类)
- [4. 查询结果异常](#4-查询结果异常)
- [5. 性能类](#5-性能类)
- [6. 持久化与崩溃恢复](#6-持久化与崩溃恢复)
- [7. 内存引擎相关](#7-内存引擎相关)
- [8. 协议兼容（PG wire）](#8-协议兼容pg-wire)
- [9. 监控与诊断](#9-监控与诊断)

## 1. 连接类

### 1.1 `widb-server: listen tcp ...: bind: address already in use`

**原因**：端口已被占用（可能上一个 widb 进程没退干净）。

**解决**：
```bash
# 1) 找出占用端口的进程
lsof -i :9000
# 或
ss -tlnp | grep 9000

# 2) 优雅退出旧进程
pkill -TERM widb-server
sleep 1
pkill -9 widb-server   # 万不得已再 KILL -9

# 3) 换端口启动
./widb-server -tcp 0.0.0.0:19090 -http 0.0.0.0:18080
```

### 1.2 HTTP `connection refused`

**排查步骤**：
1. 确认 server 已启动：`ps -ef | grep widb-server`
2. 确认 HTTP 端口：日志第一行 `HTTP 监听 0.0.0.0:8080`
3. 确认防火墙：`sudo iptables -L -n` 或云服务商安全组
4. 确认 client 连接的是 server 的可达地址，不是 `127.0.0.1`（跨主机时）

### 1.3 TCP 握手成功但第一个请求超时

**症状**：客户端能连上但读不到响应。

**原因**：客户端可能没按 protocol 的「4 字节长度前缀 + JSON payload」格式发送。

**解决**：参考 [api.md](api.md) §1.2；或直接用 `widb-cli`（已封装协议）。

### 1.4 psql 报 `FATAL: unsupported startup message`

**原因**：PG 协议握手字段不被支持（如 `database` 字段非空、`user` 含特殊字符）。

**解决**：
```bash
# 用最简连接
psql -h 127.0.0.1 -p 5432 -U postgres -d postgres
```

详见 [pgwire.md](pgwire.md) §3。

## 2. SQL 解析与执行类

### 2.1 `code: -1, message: "SQL 解析错误: query parse: unsupported expr type ..."`

**原因**：当前 parser 不支持该语法节点。

**解决方案**：

| 报错关键字 | 不支持语法 | 替代方案 |
|----------|----------|----------|
| `ValTuple` | `IN (a, b, c)` | 用 `OR` 链：`a=1 OR a=2 OR a=3` |
| `RangeCond` | `BETWEEN a AND b` | 用 `>=` + `<=`：`col >= a AND col <= b` |
| `IsExpr` | `IS NULL` / `IS NOT NULL` | **无 SQL 替代方案**：`WHERE col = NULL` 会因 NULL 三值逻辑过滤掉所有行（既不匹配 NULL 也不匹配非 NULL），**无法**用于筛选 NULL 行。需在客户端对返回结果的 `valid` 字段自行判断，或改写 schema 把 NULL 替换为哨兵值 |
| `JoinTableExpr` | `INNER JOIN` / `LEFT JOIN` | 多次查询后客户端合并 |
| `Union` | `UNION ALL` | 多次查询后客户端合并 |
| `OrderBy` 静默丢弃 | `ORDER BY` | 客户端排序 |

完整限制列表见 [sql-reference.md](sql-reference.md) §10。

### 2.2 `code: -1, message: "表 xxx 不存在"`

**原因**：SQL 中引用的表未创建。

**排查**：
```sql
SHOW TABLES;   -- 确认所有表
DESCRIBE xxx;  -- 确认表结构
```

### 2.3 `code: -1, message: "列 xxx 不存在"`

**原因**：`SELECT` 列表或 `WHERE` 中引用了未定义的列。

**排查**：
```sql
DESCRIBE your_table;
```

**注意**：列名区分大小写。

### 2.4 `code: -1, message: "主键冲突"`

**原因**：`INSERT` 或 `UPDATE` 后产生与已有行相同的主键。

**排查**：
- `INSERT` 重复：用 `SELECT` 查现有主键集合
- `UPDATE` 改主键：检查新主键是否已存在

**解决**：应用层先 `SELECT ... WHERE pk = ?` 判定，或改为 `INSERT ... ON DUPLICATE KEY UPDATE`（当前不支持，需应用层处理）。

### 2.5 `code: -1, message: "类型不兼容: cannot convert STRING to INT64"`

**原因**：插入或比较时类型不匹配。

**示例**：
```sql
-- 错误
INSERT INTO t (id, name) VALUES ('abc', 123);
-- 正确
INSERT INTO t (id, name) VALUES (1, 'sensor-1');
```

> 整数族（INT8/16/32/64/UINT64）之间可隐式转换；其他跨族转换会报错。

### 2.6 算术运算结果为 NULL

**原因**：任一操作数为 NULL 都会得到 NULL（标准 SQL 三值逻辑）。

**示例**：
```sql
SELECT NULL + 1;     -- NULL
SELECT 1 / 0;        -- 整数除零报错；浮点除零得到 +Inf / -Inf / NaN
```

## 3. 写入类

### 3.1 `/write` 接口返回 `code: 0` 但客户端报超时

**原因**：HTTP 响应正常返回，但客户端读取超时设置过短；或 server 处于 Compaction 高峰期。

**解决**：
- 客户端增加超时：`requests.post(..., timeout=30)`
- 检查 `metrics`：`widb_compact_duration_seconds` 突增说明正在合并大 Segment

### 3.2 批量写入部分成功

**症状**：`/write` 返回 `rows: N` 但实际表里少于 N 行。

**原因**：LSM 引擎对整批做原子提交，**要么全部成功要么全部失败**；内存引擎行为相同。如果你看到「部分写入」，可能是：

- 重试逻辑：客户端重试了部分批次
- Schema 不匹配：部分行因类型转换失败被静默跳过

**排查**：
```bash
# 1) 看 server 日志
journalctl -u widb-server | tail -100

# 2) 用 SELECT 核对行数
curl -X POST http://localhost:8080/query \
  -d '{"sql":"SELECT COUNT(*) AS n FROM your_table"}'
```

### 3.3 WAL 文件占用磁盘过大

**原因**：`WAL` 默认不会被自动清理；后台调度器有 `wal_clean_interval` 与 `wal_clean_threshold` 配置。

**排查**：
```bash
ls -lah ./data/wal/
du -sh ./data/wal/
```

**调优**（见 [performance.md](performance.md)）：
```yaml
scheduler:
  wal_clean_interval: 30s
  wal_clean_threshold: 67108864  # 64MB
```

## 4. 查询结果异常

### 4.1 `ORDER BY` 没生效

**原因**：当前 parser 静默丢弃 `ORDER BY` 子句（已知限制）。

**临时方案**：
- 客户端排序
- `LIMIT N` 后客户端再排

### 4.2 `DISTINCT` 没去重

**原因**：同上，`DISTINCT` 静默丢弃。

**临时方案**：用 `GROUP BY` 替代。

### 4.3 `HAVING` 没过滤

**原因**：同上，`HAVING` 静默丢弃。

**临时方案**：用子查询 + 外层 `WHERE` 替代。

### 4.4 聚合结果列名奇怪（如 `(v + 1)`）

**原因**：算术表达式没有 `AS` 别名时，自动生成的列名为表达式原文。

**修复**：
```sql
SELECT v + 1 AS v_plus_one FROM t;
```

### 4.5 `COUNT(*)` 与 `COUNT(col)` 结果不一致

**预期行为**：`COUNT(*)` 包含 NULL 行；`COUNT(col)` 忽略 NULL。

**示例**：
| data    | COUNT(*) | COUNT(col) |
|---------|----------|------------|
| 1, 2, NULL, 3 | 4 | 3 |

## 5. 性能类

### 5.1 写入吞吐上不去（< 1k rows/s）

**排查**：
```bash
curl -s http://localhost:8080/metrics | grep -E 'widb_(write|wal|flush)'
```

| 指标 | 含义 | 调优方向 |
|------|------|----------|
| `widb_wal_sync_duration_seconds` | WAL fsync 耗时 | 改用 GroupCommit（默认已开） |
| `widb_memtable_bytes` | 内存表大小 | 调大 `max_memtable_size` |
| `widb_flush_duration_seconds` | MemTable 刷盘耗时 | 调大 `max_memtable_size` 减少刷盘次数 |

**调优建议**：
- 批量大小：单次 `/write` 1k~10k 行
- 并发：多客户端并发 `/write` 走 GroupCommit
- MemTable 大小：64MB ~ 256MB 适合高吞吐

详见 [performance.md](performance.md)。

### 5.2 查询 P99 > 100ms

**排查**：
1. `EXPLAIN <query>` 看是否走主键索引
2. `widb_cache_hit_ratio` 缓存命中率
3. `widb_sparse_index_skip_ratio` 稀疏索引裁剪率

**优化套路**：
- 加 `LIMIT`：早停
- 列裁剪：只 `SELECT` 需要的列
- 用主键：`WHERE` 尽量包含主键列
- 减少函数：避免在 `WHERE` 中对列做函数变换

### 5.3 Compaction 写放大严重

**症状**：`widb_compact_bytes_written` 远大于 `widb_write_bytes`。

**原因**：频繁的小 Segment 触发级联合并。

**调优**：
- 调大 `max_memtable_size`：减少刷盘次数
- 调小 `compact_interval`：让合并更及时
- 减少小批量写入

## 6. 持久化与崩溃恢复

### 6.1 启动报 "WAL replay failed"

**原因**：WAL 文件损坏（异常断电 + 写入未刷盘）。

**解决**：
- 默认行为：跳过损坏记录并报警告；启动后用 `SHOW TABLES` 与 `SELECT COUNT(*)` 核对数据
- 极端情况：备份并删除损坏的 WAL：
  ```bash
  mv ./data/wal/wal-xxx.log ./data/wal/wal-xxx.log.bak
  ```
  重启时会从其他 WAL 恢复。

### 6.2 重启后部分行丢失

**排查**：
1. 检查 `widb_wal_clean_count` 与 `widb_wal_clean_threshold` 设置
2. 检查 `widb_last_flush_error`（如果有）
3. `DESCRIBE` 看看表结构是否变化
4. 检查 `data_dir` 路径是否正确

### 6.3 数据目录迁移后无法启动

**原因**：Catalog 文件中包含绝对路径，或 Segment 引用了原目录。

**解决**：
- 用 `rsync -a` 而非 `cp` 保留 inode/权限
- 同一台机器内：硬链接或 `mv` 即可
- 跨机器：`tar` 打包保留所有元数据

## 7. 内存引擎相关

### 7.1 `ENGINE=memory` 表重启后空

**预期行为**：内存引擎不持久化；重启后表结构保留但数据清空。

**说明**：表 Schema 持久化在 Catalog JSON 中；表数据存在内存里。

### 7.2 内存表占用过高

**原因**：内存表没有 Compaction；写入越多占用越大。

**解决**：
- 控制写入速率
- 周期性 `TRUNCATE TABLE`（当前不支持；可用 `DROP TABLE` + `CREATE TABLE`）
- 改用 LSM 引擎

## 8. 协议兼容（PG wire）

### 8.1 psql 报 `column "xxx" does not exist`

**原因**：列名是大小写敏感的，psql 默认把不带引号的标识符转为小写。

**解决**：
```sql
-- 用双引号保留原大小写
SELECT "device_id" FROM "sensor" LIMIT 1;
```

### 8.2 psql 报 `function xxx does not exist`

**原因**：PG 客户端函数（如 `now()`、`generate_series()`）不被支持。

**解决**：用字面量替代：
```sql
-- 错误
SELECT now();
-- 正确
SELECT TIMESTAMP '2026-06-01T00:00:00Z';
```

### 8.3 JDBC 启动报 `unsupported type`

**原因**：JDBC 驱动发送了不被支持的 PG OID。

**解决**：检查 `pkg/server/pgwire/types.go` 中的 OID 映射；如缺类型可提 issue。

## 8a. CLI / REPL 类

### 8a.1 打开 `widb-cli` 立即报 `错误: invalid prompt`

**症状**（issue #233）：
```
$ ./widb-cli.exe
widb-cli - WiDB 命令行客户端
输入 SQL 语句执行查询，输入 \q 退出，输入 \h 查看帮助
模式: tcp | TCP: 127.0.0.1:9000 | HTTP: 127.0.0.1:8080

错误: invalid prompt
```

**原因**：`peterh/liner` 库严格校验 prompt 字符串，凡含 Unicode 类别 C（控制字符，含 ESC `\x1b`）的 prompt 一律返回 `ErrInvalidPrompt("invalid prompt")`。而 WiDB CLI 在 TTY 模式下默认会调用 `ColorizePrompt("widb> ")`，其产物是含 ANSI 转义码的高亮字符串，于是触发该错误。

**解决**：升级到包含此修复的版本（`LinerSession.PromptWithWriter`）。临时绕开方式：设置 `NO_COLOR=1` 关闭颜色，让 prompt 不含控制字符（功能完整，仅缺高亮）。

## 9. 监控与诊断

### 9.1 关键指标

```bash
# 抓取一次完整指标
curl -s http://localhost:8080/metrics > /tmp/metrics.txt

# 关注：
grep '^widb_' /tmp/metrics.txt | grep -E '(total|bytes|duration|errors)'
```

| 指标 | 含义 |
|------|------|
| `widb_query_total{type=...}` | SELECT/INSERT/UPDATE/DELETE 调用次数 |
| `widb_write_bytes_total` | 累计写入字节数 |
| `widb_memtable_bytes` | 当前 MemTable 内存占用 |
| `widb_segment_count` | 当前活跃 Segment 数 |
| `widb_cache_hit_ratio` | Block Cache 命中率 |
| `widb_sparse_index_skip_ratio` | 稀疏索引裁剪率 |
| `widb_compact_duration_seconds` | Compaction 耗时直方图 |
| `widb_wal_sync_duration_seconds` | WAL fsync 耗时直方图 |

### 9.2 调试协议

```bash
# 用 tcpdump 抓 widb-server 通信
sudo tcpdump -i lo -A -s 0 'port 9000'

# 用 widb-cli 走 HTTP 协议（避免解析 TCP 帧）
./widb-cli -mode http -e "SELECT 1"
```

### 9.3 启用 debug 日志

`widb-server` 当前默认 `INFO` 级别。临时调试可设置环境变量（如适用）：

```bash
WIDB_LOG_LEVEL=debug ./widb-server
```

> 实际日志级别支持以代码为准（参考 `pkg/server/server.go`）。

### 9.4 提交 issue

如果以上都不能解决，请到仓库 issue 区提交：

- widb-server 版本（`./widb-server -h` 或 `git rev-parse HEAD`）
- 复现命令（SQL / curl 命令）
- 期望结果 vs 实际结果
- `metrics` 关键指标快照
- server 日志最后 50 行

---

> 仍有疑问？查阅：
> - [architecture.md](architecture.md) — 系统如何工作
> - [sql-reference.md](sql-reference.md) — SQL 语法权威参考
> - [tutorial.md](tutorial.md) — 端到端上手
> - [cookbook.md](cookbook.md) — 常见 SQL 套路
> - [development.md](development.md) — 本地开发与 CI
