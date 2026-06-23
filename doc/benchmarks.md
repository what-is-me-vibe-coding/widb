# 基准测试指南

本指南汇总 WiDB 当前可用的所有基准测试（Benchmark），并说明如何运行、分析结果与解读数据，便于在性能优化、回归定位与容量规划时使用。

> 适用代码库版本：`main` 分支最新提交
> 关联文件：[development.md](./development.md)、[roadmap.md](../.agent_plan/roadmap.md)

## 1. 快速开始

所有基准测试使用标准库 `testing` 的 `-bench` 接口，运行方式一致：

```bash
# 运行指定包内全部基准测试
go test -bench=. -benchmem ./pkg/storage/...

# 运行指定基准
go test -bench=BenchmarkEngineScanRange -run=^$ -benchtime=3s ./pkg/storage/...

# 多次采样以便统计（默认内存分配数据已包含）
go test -bench=BenchmarkEngineWrite -count=5 -benchtime=2s ./pkg/storage/...
```

通用选项：

| 选项 | 用途 |
|------|------|
| `-bench=.` | 匹配包内所有 Benchmark 函数 |
| `-bench=^BenchmarkEngine` | 正则匹配特定前缀 |
| `-run=^$` | 跳过单元测试，仅跑基准 |
| `-benchtime=Ns` | 每个基准最少运行 N 秒（默认 1s） |
| `-benchtime=Nx` | 每个基准固定执行 N 次迭代 |
| `-count=N` | 重复运行 N 次以观察抖动 |
| `-benchmem` | 输出每次操作的内存分配次数与字节数 |
| `-cpuprofile=cpu.out` | 导出 CPU profile，配合 `go tool pprof` 分析 |
| `-memprofile=mem.out` | 导出内存 profile |
| `-timeout=10m` | 调高整体超时，避免长时间基准被强杀 |
| `-parallel=N` | 控制并行基准并发度 |

## 2. 基准测试矩阵

下表列出当前仓库内全部 `Benchmark*` 函数及其关注点。条目按所在包分组。

### 2.1 `pkg/storage`（存储引擎）

源文件：[`pkg/storage/benchmark_test.go`](../pkg/storage/benchmark_test.go) 与 [`pkg/storage/segment_binary_search_test.go`](../pkg/storage/segment_binary_search_test.go)

| 基准 | 关注点 | 典型用例 |
|------|--------|----------|
| `BenchmarkMemTablePut` | 单行写入 MemTable 内部路径 | 评估跳表/slab 分配器性能 |
| `BenchmarkMemTableGet` | MemTable 点查（点读热点） | 评估跳表/哈希索引点查路径 |
| `BenchmarkMemTableScan` | 顺序扫描 MemTable 全量 | 评估 MemTable 顺序读吞吐 |
| `BenchmarkEngineWrite` | 走完整 WAL + MemTable 的单行写入 | 端到端写入基线 |
| `BenchmarkEngineWriteBatch` | 批量写入（组提交路径） | 评估批量接口吞吐 |
| `BenchmarkEngineWriteParallel` | 多 goroutine 并发写入 | 并发场景的写扩展性 |
| `BenchmarkEngineGet` | 端到端点查（MemTable + Segment 查找） | 点查延迟基线 |
| `BenchmarkEngineScanRange` | 端到端区间扫描 | 范围查询吞吐基线 |
| `BenchmarkEngineWriteGroupCommit` | 启用 group commit 后的写入 | 评估 fsync 合并效果 |
| `BenchmarkEngineWriteGroupCommitParallel` | group commit 并发压测 | 评估并发场景下 fsync 合并 |
| `BenchmarkEngineWriteBatchGroupCommit` | 批量 + group commit | 高吞吐写入 |
| `BenchmarkWALAppend` | 裸 WAL 顺序追加 | 评估 fsync 与编码成本 |
| `BenchmarkEncodePlain` / `BenchmarkDecodePlain` | Plain 编码/解码 | 列存编码基线 |
| `BenchmarkEncodeDict` / `BenchmarkDecodeDict` | Dictionary 编码/解码 | 低基数字段压缩效率 |
| `BenchmarkEncodeRLE` / `BenchmarkDecodeRLE` | RLE 编码/解码 | 连续重复值压缩 |
| `BenchmarkCompress` / `BenchmarkDecompress` | ZSTD 块级压缩/解压 | 压缩/解压吞吐基线 |
| `BenchmarkColumnVectorAppend` | Column Vector 追加 | 列式内存布局追加 |
| `BenchmarkColumnVectorGetValue` | Column Vector 随机位置读 | 列式读延迟 |
| `BenchmarkCopySelectedInt64` / `BenchmarkCopySelectedString` | 向量化选择拷贝 | 算子下推 / 向量化执行 |
| `BenchmarkSegmentIterator_BinarySearchPositioning` | Segment 内二分定位 | 范围扫描前导成本 |
| `BenchmarkFindRowByKeyGE` / `BenchmarkFindRowByKeyLE` | 单边边界查找 | 等值与范围扫描首行定位 |
| `BenchmarkComputeRange` | 区间[start,end] 计算 | 范围扫描边界计算 |

### 2.2 `pkg/query`（查询引擎）

源文件：[`pkg/query/benchmark_test.go`](../pkg/query/benchmark_test.go)

| 基准 | 关注点 | 典型用例 |
|------|--------|----------|
| `BenchmarkParserSelect` | SELECT 解析耗时 | SQL 解析延迟基线 |
| `BenchmarkParserInsert` | INSERT 解析耗时 | 写入路径 SQL 解析 |
| `BenchmarkParserCreateTable` | CREATE TABLE 解析耗时 | DDL 路径解析基线 |
| `BenchmarkPreprocessSQLNoMatch` | 无关键字预处理的零拷贝快速路径 | 验证常见 DML 的预处理开销 |
| `BenchmarkPreprocessSQLWithMatch` | 含关键字（CREATE TABLE）预处理 | 验证关键字替换路径开销 |
| `BenchmarkAnalyzer` | 语义分析（绑定/类型/Schema 解析） | 分析阶段吞吐基线 |
| `BenchmarkOptimizer` | 优化器整体耗时（谓词下推/常量折叠/列裁剪等） | 优化阶段延迟基线 |
| `BenchmarkExecutorScan` | 执行器全表扫描 | 扫描算子基线 |
| `BenchmarkEndToEndSelect` | 解析→分析→优化→执行 全链路 | 端到端 SELECT 延迟基线 |
| `BenchmarkFilterInt64FastPath` | INT64 列过滤快速路径 | 类型特化过滤算子吞吐 |
| `BenchmarkFilterFloat64FastPath` | FLOAT64 列过滤快速路径 | 类型特化过滤算子吞吐 |
| `BenchmarkFilterStringFastPath` | STRING 列过滤快速路径 | 字符串等值/范围过滤 |
| `BenchmarkFilterInt64FastPathWithNulls` | 含 NULL 的 INT64 列过滤 | 验证 NULL 检查分支开销 |
| `BenchmarkFilterStringFastPathWithNulls` | 含 NULL 的 STRING 列过滤 | 验证 NULL 检查分支开销 |
| `BenchmarkAggregateGroupBySum` | 窄表（3 列）GROUP BY + SUM 聚合 | 聚合算子基线 |
| `BenchmarkAggregateWideGroupBySum` | 宽表（32 列）GROUP BY + SUM 聚合 | 宽表聚合扩展性 |
| `BenchmarkProjectNarrow` | 窄表（3 列）3 个投影表达式 | 投影算子基线 |
| `BenchmarkProjectWide` | 宽表（32 列）32 个投影表达式 | 宽表投影扩展性 |

### 2.3 `pkg/server`（接入层）

源文件：[`pkg/server/benchmark_test.go`](../pkg/server/benchmark_test.go)

| 基准 | 关注点 | 典型用例 |
|------|--------|----------|
| `BenchmarkHTTPHealth` | HTTP `/healthz` QPS | 健康检查吞吐基线 |
| `BenchmarkHTTPQuery` | HTTP `/query` SELECT 路径 | 读请求延迟基线 |
| `BenchmarkHTTPWrite` | HTTP `/write` INSERT 路径 | 写请求延迟基线 |
| `BenchmarkHTTPWriteBatch` | HTTP `/write` 批量 | 批量接口吞吐 |
| `BenchmarkPacketEncode` | TCP 协议包编码 | 入站序列化成本 |
| `BenchmarkPacketDecode` | TCP 协议包解码 | 出站反序列化成本 |
| `BenchmarkChunksToRows` | 单 chunk → 行结果 | HTTP/TCP 结果拼装 |
| `BenchmarkChunksToRowsMultiChunk` | 多 chunk → 行结果 | 大结果集拼装 |
| `BenchmarkChunksToRowsWideTable` | 宽表 chunk → 行结果 | 宽表列数对拼装的影响 |
| `BenchmarkInterfaceToValue` | `any` → `common.Value` 转换（数值类型） | 列数据类型断言成本 |
| `BenchmarkInterfaceToValueString` | `any` → `common.Value` 转换（字符串类型） | 字符串列转换成本 |
| `BenchmarkConvertWriteRow` | HTTP/TCP 写入行转换 | 写入预处理基线 |
| `BenchmarkTCPPing` | TCP ping/pong 往返 | 长连接保活与心跳延迟 |
| `BenchmarkDeleteByPK` | 主键等值 DELETE 路径 | 点查快路径收益验证 |
| `BenchmarkUpdateByPK` | 主键等值 UPDATE 路径 | 点查快路径收益验证 |

### 2.4 `tests/integration`（端到端基准）

源文件：[`tests/integration/ycsb_benchmark_test.go`](../tests/integration/ycsb_benchmark_test.go)

| 基准 | 关注点 | 典型用例 |
|------|--------|----------|
| `BenchmarkYCSB_WorkloadA` | YCSB Workload A（50%读 50%更新） | OLTP 读写混合基线 |
| `BenchmarkYCSB_WorkloadB` | YCSB Workload B（95%读 5%更新） | 读密集场景 |
| `BenchmarkYCSB_WorkloadC` | YCSB Workload C（100%读） | 纯读缓存命中场景 |
| `BenchmarkYCSB_WriteThroughput` | 持续写入吞吐 | 写入容量规划 |
| `BenchmarkYCSB_BatchWriteThroughput` | 批量写入吞吐 | 评估批量接口上限 |
| `BenchmarkYCSB_ParallelWriteThroughput` | 并发写入吞吐 | 并发扩展性 |
| `BenchmarkYCSB_PointQueryLatency` | 点查 P50/P99 延迟 | 延迟 SLO 验证 |
| `BenchmarkYCSB_RangeScan` | 范围扫描吞吐 | 范围查询评估 |
| `BenchmarkYCSB_WideTableWrite` | 宽表写入（>50 列） | 宽表场景写吞吐 |
| `BenchmarkYCSB_WideTableRead` | 宽表读取 | 宽表场景读吞吐 |
| `BenchmarkYCSB_WriteFlushCompact` | 写满 + 触发 flush/compaction | 后台任务对前台影响 |

## 3. 解读结果

典型输出形如：

```
BenchmarkEngineScanRange-8    27130    44300 ns/op    110898 B/op    10 allocs/op
```

字段含义：

| 字段 | 含义 |
|------|------|
| `BenchmarkEngineScanRange-8` | 函数名 + GOMAXPROCS（`-8` 表示 8 个 OS 线程） |
| `27130` | 实际迭代次数（自动调整以满足 `-benchtime`） |
| `44300 ns/op` | 每次操作平均耗时（纳秒） |
| `110898 B/op` | 每次操作平均分配字节数（仅 `-benchmem` 输出） |
| `10 allocs/op` | 每次操作平均分配次数 |

### 3.1 抖动与统计置信

单次 `-benchtime=1s` 抖动较大，建议：

- `-count=5` 多次采样，观察 `p50/p99` 区间；
- 排除前两次结果作为预热（`go test -bench=. -count=7 -benchtime=2s` 后丢弃前两次）；
- 使用 `benchstat`（`golang.org/x/perf/cmd/benchstat`）对比改动前后基线。

```bash
# 在主分支跑一次保存 baseline
go test -bench=BenchmarkEngine -count=10 -benchtime=2s ./pkg/storage/ > old.txt

# 切换到优化分支再跑一次
go test -bench=BenchmarkEngine -count=10 -benchtime=2s ./pkg/storage/ > new.txt

# 用 benchstat 给出统计性结论
benchstat old.txt new.txt
```

### 3.2 性能退化阈值

依据 [AGENTS.md §5.3](../AGENTS.md) 与 [development.md §4.3](./development.md)：

- 性能退化 > 5% 必须在 PR 描述中给出原因；
- 内存分配（`B/op`、`allocs/op`）显著上升需同样说明；
- 建议在 PR 中附 `benchstat` 输出，便于评审量化影响。

## 4. 常见场景速查

### 4.1 写吞吐

```bash
go test -bench='BenchmarkEngineWrite|BenchmarkWALAppend' -benchmem -count=3 ./pkg/storage/
```

### 4.2 读延迟与范围扫描

```bash
go test -bench='BenchmarkEngineGet|BenchmarkEngineScanRange|BenchmarkFindRowByKey' -benchmem -count=3 ./pkg/storage/
```

### 4.3 编码/压缩

```bash
go test -bench='BenchmarkEncode|BenchmarkDecode|BenchmarkCompress' -benchmem -count=3 ./pkg/storage/
```

### 4.4 端到端 YCSB

```bash
go test -bench='BenchmarkYCSB' -benchmem -count=3 -timeout=20m ./tests/integration/
```

### 4.5 配合 pprof 定位热点

```bash
go test -bench=BenchmarkEngineScanRange -run=^$ -benchtime=5s -cpuprofile=cpu.out ./pkg/storage/
go tool pprof -top -cum cpu.out

go test -bench=BenchmarkEngineScanRange -run=^$ -benchtime=5s -memprofile=mem.out ./pkg/storage/
go tool pprof -alloc_space -top mem.out
```

## 5. 编写新基准的约定

为保持一致性，新基准请遵循以下规范：

1. **文件命名**：单元粒度的微基准放入 `pkg/<包>/benchmark_test.go`；跨包 / 端到端基准放入 `tests/integration/ycsb_benchmark_test.go`。
2. **函数命名**：使用 `BenchmarkXxx` 前缀，命名需能直观说明关注点（被测对象 + 场景），例如 `BenchmarkEngineScanRange`。
3. **参数覆盖**：在表驱动基准（`b.Run`）中覆盖典型参数（行数、并发度、字段类型），避免只测单一规模。
4. **资源隔离**：使用 `b.TempDir()` 或 `t.TempDir()` 创建临时目录；测试结束后由框架自动清理。
5. **重置计时器**：`b.ResetTimer` 必须在数据准备完成后调用；并发基准使用 `b.RunParallel` 时配合 `b.ReportAllocs()`。
6. **不要在基准中做断言**：基准只测性能，正确性由对应单元测试 / 集成测试覆盖。
7. **PR 提交**：在 PR 描述中说明新增基准的目的、典型数值与是否可能影响 CI 时长。

## 6. 常见问题

**Q1：基准在我的机器上比 CI 慢 / 快很多？**
A：基准对 CPU 频率、磁盘 IO、文件系统缓存、Go 版本敏感。CI 环境差异在 10–20% 内属正常。性能结论请使用同机对比，并结合 `benchstat`。

**Q2：内存分配数看起来很高？**
A：先定位分配来源：`go test -bench=... -memprofile=mem.out` → `go tool pprof -alloc_objects -top mem.out`。

**Q3：基准跑不动 / 超时？**
A：增加 `-timeout=30m`，或单独运行耗时基准（`go test -bench=BenchmarkYCSB_WriteFlushCompact -run=^$ -timeout=30m ./tests/integration/`）。

**Q4：是否需要使用 `pgo`？**
A：目前未启用 Profile-Guided Optimization（PGO）。如果某些热路径经过反复优化后仍存留 5% 以上的差距，可在 PR 中讨论是否引入 PGO。

---

> 修订记录
> - v1（初版）：建立基准测试矩阵与运行速查表。
