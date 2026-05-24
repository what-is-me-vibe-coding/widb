# 单机高性能宽表数据库开发计划

## 1. 项目定义

**目标**：构建一个面向分析型负载（OLAP）的单机宽表数据库，支持高吞吐写入与低延迟点查/范围扫描，列式存储，Schema-free 宽表模型。

**核心指标**：
- 写入吞吐：≥ 100k rows/s（单节点）
- 点查延迟：P99 < 10ms
- 支持列数：≥ 10,000 列
- 数据格式：列式存储 + 字典编码 + RLE + 压缩

## 2. 架构设计

### 2.1 存储引擎（Storage Engine）
- **LSM-Tree 变体**：写入先落 WAL，再刷入内存 MemTable，后台 Compaction 生成不可变 Segment
- **列式块存储**：每列独立存储为 Block，按 Row Group 组织，Block 内使用字典编码 + RLE + ZSTD 压缩
- **稀疏索引**：每个 Segment 维护 Min/Max 统计信息与布隆过滤器，跳过不满足条件的 Block
- **WAL**：顺序追加写，用于崩溃恢复，写满后切分并异步刷盘

### 2.2 内存结构
- **MemTable**：跳表（Skip List）或 BTree，按主键排序，支持并发写入
- **Block Cache**：LRU 缓存解压后的列 Block
- **Index Cache**：缓存 Segment 级稀疏索引与布隆过滤器

### 2.3 查询引擎（Query Engine）
- **执行模型**：向量化执行（Volcano / Batch 模式），一次处理一批（如 1024 行）
- **算子**：Scan、Filter、Project、Aggregate（Count/Sum/Min/Max/Avg）、TopN、Limit
- **优化器**：基于规则的优化（RBO），列裁剪、谓词下推、分区裁剪
- **表达式求值**：支持常量折叠与短路求值

### 2.4 元数据管理
- **Catalog**：表 Schema、列定义、数据类型、分区信息
- **Segment Manager**：Segment 生命周期（创建、合并、删除）、版本控制
- **持久化**：JSON / Protobuf 文件，原子替换保证一致性

## 3. 模块划分

| 模块 | 职责 |
|------|------|
| `pkg/storage` | WAL、MemTable、Segment、Block、压缩/编码 |
| `pkg/index` | 主键索引、稀疏索引、布隆过滤器 |
| `pkg/query` | SQL 解析、执行计划、向量化算子 |
| `pkg/catalog` | Schema 管理、元数据持久化 |
| `pkg/server` | TCP/HTTP 协议层、连接管理、请求路由 |
| `pkg/common` | 数据类型、内存池、错误处理、工具函数 |

## 4. 开发阶段

### Phase 1：核心存储（Week 1-2）
1. 定义数据类型系统（INT64, FLOAT64, STRING, TIMESTAMP, BOOL, NULL）
2. 实现列式内存布局（ColumnVector / Chunk）
3. 实现 WAL：顺序写、校验和、回放恢复
4. 实现 MemTable：并发跳表，支持插入与快照读取
5. 实现 Segment 编码：字典编码、RLE、位图（Bitmap）、ZSTD 压缩
6. 实现 Segment 文件格式：Header + Column Blocks + Footer（索引、统计）
7. 实现 Compaction：Tiered 策略，合并小 Segment，重写大 Segment，清理过期数据

### Phase 2：索引与查询（Week 3-4）
1. 实现稀疏索引：每 Segment 每列维护 Min/Max，查询时剪枝
2. 实现布隆过滤器：主键存在性判断，减少无效 IO
3. 实现点查路径：主键 → 索引 → Segment → Block → 解压 → 返回值
4. 实现范围扫描：主键范围 → 索引过滤 → 并行 Segment 扫描
5. 实现向量化 Filter 算子：SIMD 友好的批量比较
6. 实现 Project 与 Aggregate 算子
7. 实现简易 SQL 解析器（或引入 sqlparser）与执行计划生成

### Phase 3：系统与优化（Week 5-6）
1. 实现 Catalog：Create Table、Alter Table、Drop Table
2. 实现 Block Cache 与 Index Cache，配置容量与淘汰策略
3. 实现后台任务调度：Compaction、WAL 归档、过期清理
4. 实现 TCP/HTTP Server，支持批量写入（Batch Insert）与查询接口
5. 性能基准测试：YCSB、TPC-H 子集，定位瓶颈并优化
6. 内存池与对象复用，减少 GC 压力

### Phase 4：稳定与测试（Week 7-8）
1. 崩溃恢复测试：随机 Kill、WAL 回放一致性校验
2. 并发测试：多线程写入 + 读取，验证 MemTable 快照隔离
3. 数据正确性测试：与 SQLite/MySQL 对比随机 SQL 结果
4. 压力测试：长时间运行，观察内存泄漏与 Compaction 稳定性
5. 完善错误处理、日志、监控指标（Prometheus 格式）

## 5. 关键技术决策

| 决策项 | 选择 | 理由 |
|--------|------|------|
| 存储格式 | 自研列式 Segment | 宽表场景下控制列编码与压缩粒度 |
| 写入模型 | LSM-Tree + WAL | 顺序写优化，适合高吞吐写入 |
| 并发控制 | MVCC + 快照读 | 读写不阻塞，MemTable 不可变快照 |
| 压缩算法 | ZSTD | 高压缩比与解压速度平衡 |
| 缓存策略 | LRU | 简单高效，热点数据自动驻留 |
| 网络协议 | TCP 自定义 + HTTP | 内部高效，外部易用 |

## 6. 风险与应对

| 风险 | 应对 |
|------|------|
| Compaction 写放大 | 监控层级，调整触发阈值；支持列级 TTL |
| 宽表列数过多导致元数据膨胀 | 延迟加载列统计，按需解压 Block |
| 内存不足（MemTable 过大） | 配置写入限流，强制刷盘阈值 |
| 字符串列压缩率低 | 针对低基数列优先字典编码，高基数列直接 ZSTD |

## 7. 子模块详细设计索引

| 模块 | 文件 | 说明 |
|------|------|------|
| Storage | [storage.md](storage.md) | WAL、MemTable、Segment、ColumnBlock、编码/压缩、Compaction、读写流程 |
| Index | [index.md](index.md) | 主键索引、稀疏索引、布隆过滤器、查询路径、持久化与恢复 |
| Query | [query.md](query.md) | SQL 解析、执行计划、向量化 Chunk、算子实现、支持的 SQL 子集 |
| Catalog | [catalog.md](catalog.md) | Schema 管理、数据类型系统、Segment 列表、原子性变更、版本控制 |
| Server | [server.md](server.md) | TCP/HTTP 协议、连接管理、批量写入、监控指标、优雅关闭 |
| Common | [common.md](common.md) | 错误码、日志、内存池、Bitmap、编码工具、第三方依赖汇总 |

## 8. 第三方依赖汇总

| 库 | 模块 | 用途 |
|---|------|------|
| `github.com/klauspost/compress/zstd` | Storage | Block 级 ZSTD 压缩与解压 |
| `github.com/bits-and-blooms/bloom/v3` | Index | Segment 级主键布隆过滤器 |
| `github.com/xwb1989/sqlparser` | Query | SQL 词法/语法解析（MySQL 方言） |
| `github.com/cespare/xxhash/v2` | Common | 高性能哈希（Cache 键、字典编码） |

## 9. 开发步骤表

| 步骤 | 任务 | 目标模块 | 验收标准 |
|------|------|----------|----------|
| 1 | 初始化 Go 模块与目录结构 | 全局 | `go.mod` 创建，`pkg/` 与 `cmd/` 目录就绪 |
| 2 | 实现 DataType、Value、ColumnDef 基础类型 | Common | 6 种类型可序列化/反序列化，单元测试通过 |
| 3 | 实现 Bitmap 与 BufferPool | Common | Bitmap Set/Get/Count 正确，Pool 复用无泄漏 |
| 4 | 实现 WAL 顺序写与回放恢复 | Storage | 写入 10k 条记录，Kill 后回放数据一致 |
| 5 | 实现 MemTable（并发跳表） | Storage | 并发写入 100k 条，快照读取结果正确 |
| 6 | 实现 ColumnVector / Chunk 内存布局 | Storage/Query | 1024 行批次内存紧凑，NULL 标识正确 |
| 7 | 实现 Plain / Dict / RLE / Bitmap 编码 | Storage | 各编码 round-trip 正确，压缩率可测 |
| 8 | 集成 ZSTD 压缩 | Storage | 编码后数据 ZSTD 压缩/解压正确 |
| 9 | 实现 Segment 文件格式与 Builder | Storage | 写入 Segment 文件，Footer 可读取，Magic 正确 |
| 10 | 实现 MemTable → Segment 刷盘 | Storage | 触发阈值后生成 Segment，Catalog 更新正确 |
| 11 | 实现 Tiered Compaction | Storage | L0→L1 合并，旧 Segment 删除，数据一致 |
| 12 | 实现主键索引（BTree） | Index | 注册 Segment 后点查返回正确 ID 列表 |
| 13 | 实现稀疏索引与加载 | Index | Min/Max 剪枝跳过无效 Segment，统计正确 |
| 14 | 集成布隆过滤器 | Index | 1% 误判率下，不存在 key 的跳过率 > 95% |
| 15 | 实现点查完整路径 | Storage+Index | 主键查询返回正确值，P99 < 10ms（单线程） |
| 16 | 实现范围扫描与 MergeIterator | Storage+Index | 多 Segment 范围扫描结果有序、无遗漏 |
| 17 | 引入 SQL 解析器 | Query | `SELECT/INSERT/CREATE TABLE` 可解析为 AST |
| 18 | 实现 Analyzer 与 RBO 优化器 | Query | 列裁剪、谓词下推后计划节点减少 |
| 19 | 实现向量化 Scan / Filter 算子 | Query | 批次处理 1024 行，Filter 结果正确 |
| 20 | 实现 Project / Aggregate 算子 | Query | GROUP BY + COUNT/SUM/AVG 结果正确 |
| 21 | 实现 Limit / TopN 算子 | Query | LIMIT 截断正确，TopN 堆排序输出正确 |
| 22 | 实现 Catalog 与 Schema 变更 | Catalog | Create/Drop Table、Add/Drop Column 持久化正确 |
| 23 | 实现 Block Cache 与 Index Cache | Catalog+Index | LRU 淘汰正确，命中率可观测 |
| 24 | 实现后台任务调度器 | Storage | Compaction、WAL 清理定时触发，不阻塞写入 |
| 25 | 实现 TCP Server 与协议编解码 | Server | 客户端可发送 Packet，服务端返回正确响应 |
| 26 | 实现 HTTP REST API | Server | `/query`、`/write`、`/metrics` 端点可用 |
| 27 | 集成 Prometheus 监控指标 | Server | `widb_*` 指标可被 Prometheus 抓取 |
| 28 | 性能基准测试与调优 | 全局 | YCSB 写入 ≥ 100k rows/s，点查 P99 < 10ms |
| 29 | 崩溃恢复测试 | Storage | 随机 Kill 100 次，数据零丢失 |
| 30 | 并发正确性测试 | 全局 | 多线程读写 1 小时，结果与 SQLite 对比一致 |
| 31 | 压力测试与内存泄漏检测 | 全局 | 24 小时运行，RSS 增长 < 10%，无 panic |
| 32 | 编写 CLI 工具与使用文档 | 全局 | `widb-cli` 可连接服务执行 SQL |

## 10. 交付物

- 可运行的数据库服务（`go run ./cmd/server`）
- 客户端 SDK / CLI 工具
- 设计文档（存储格式、执行模型、API 规范）
- 性能测试报告
