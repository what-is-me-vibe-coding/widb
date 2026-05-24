# Agents 开发规范

## 1. 目标

本文档规范所有参与本项目的 Agent（包括人类开发者与 AI 助手）的行为准则、代码标准与协作流程，确保开发过程一致、可追溯、可维护。

## 2. 通用原则

- **设计优先**：编码前必须阅读对应模块的详细设计文档（`.agent_plan/*.md`），理解接口定义与数据流。
- **小步提交**：每个步骤独立实现、独立测试、独立提交，禁止跨步骤大段合并。
- **测试驱动**：新增功能必须附带单元测试，修复 Bug 必须先写复现测试。
- **避免重复**：已定义的结构、常量、工具函数优先复用，禁止在不同模块间复制相同逻辑。
- **性能敏感**：存储引擎与查询引擎的代码需关注内存分配，热点路径减少 GC 压力。

## 3. 代码规范

### 3.1 风格

- 遵循官方 `gofmt` 与 `go vet`，提交前必须执行。
- 命名：接口以 `er` 结尾（`StorageEngine`），私有实现不加前缀（`storageEngine`），缩写全大写（`WAL`、`HTTP`）。
- 注释：导出符号必须写文档注释，说明功能、参数、返回值与错误情况。
- 错误处理：使用 `fmt.Errorf("context: %w", err)` 包装，禁止忽略错误（`_ = fn()` 需注释理由）。

### 3.2 模块边界

| 规则 | 说明 |
|------|------|
| `pkg/common` 不依赖其他 pkg | 基础类型与工具，保持最底层 |
| `pkg/catalog` 可依赖 `pkg/common` | 元数据管理使用基础类型 |
| `pkg/storage` 可依赖 `pkg/common`、`pkg/catalog` | 存储引擎需知悉 Schema |
| `pkg/index` 可依赖 `pkg/common`、`pkg/catalog`、`pkg/storage` | 索引需读取 Segment 元数据 |
| `pkg/query` 可依赖 `pkg/common`、`pkg/catalog`、`pkg/index`、`pkg/storage` | 查询引擎调用存储与索引 |
| `pkg/server` 可依赖所有 pkg | 接入层聚合所有能力 |
| 禁止循环依赖 | 出现循环依赖时，提取公共接口到 `pkg/common` |

### 3.3 并发安全

- 共享状态必须显式加锁或使用原子操作，禁止依赖 map/slice 的隐式线程安全。
- 锁粒度：优先细粒度（per-segment、per-table），避免全局大锁。
- 锁顺序：多处加锁时定义全局顺序，防止死锁。

## 4. 开发流程

### 4.1 步骤执行

1. 从 [plan.md 开发步骤表](plan.md#9-开发步骤表) 选择当前步骤。
2. 阅读该步骤涉及模块的详细设计文档（`.agent_plan/*.md`）。
3. 编写代码与单元测试，确保 `go test ./...` 通过。
4. 执行 `go fmt ./...` 与 `go vet ./...`。
5. 提交 Git，提交信息格式：`[步骤N] 模块: 简要描述`，例如 `[步骤5] storage: 实现并发跳表 MemTable`。
6. 更新 `.agent_plan/plan.md` 中该步骤的状态（如标记为已完成，或追加备注）。

### 4.2 变更设计

- 若实现中发现设计缺陷需调整，先修改对应 `.agent_plan/*.md` 文档，再编码。
- 设计变更需说明理由，并在 Git 提交中引用文档修改。

### 4.3 引入新依赖

- 优先使用标准库。
- 第三方库需满足：活跃维护、Apache/MIT/BSD 许可证、无 CGO（除非必要）。
- 引入前在 `.agent_plan/agents.md` 的「依赖清单」中登记，说明模块与用途。

## 5. 测试规范

### 5.1 单元测试

- 文件：`*_test.go`，与实现文件同目录。
- 覆盖率：核心逻辑（编码、WAL、索引、算子）≥ 80%。
- 表驱动：使用 `[]struct{ name string; input; want }` 模式。
- 并发测试：使用 `-race` 检测数据竞争，`go test -race ./...` 必须通过。

### 5.2 集成测试

- 目录：`tests/integration/`。
- 覆盖：端到端写入 → 查询 → Compaction → 重启恢复。
- 使用临时目录，测试结束后清理。

### 5.3 性能测试

- 文件：`*_benchmark_test.go`。
- 基准：每次变更后对比基准数据，性能退化 > 5% 需说明理由。

## 6. 文档维护

- 代码变更导致接口变动时，同步更新对应 `.agent_plan/*.md` 中的接口定义。
- 新增模块需新建 `.agent_plan/模块名.md`，并在 `plan.md` 中追加索引。
- 设计文档使用中文，代码注释使用中文（与用户语言一致）。

## 7. 依赖清单

| 库 | 模块 | 用途 | 引入步骤 |
|---|------|------|----------|
| `github.com/klauspost/compress/zstd` | Storage | Block 级 ZSTD 压缩 | 步骤 8 |
| `github.com/bits-and-blooms/bloom/v3` | Index | 布隆过滤器 | 步骤 14 |
| `github.com/xwb1989/sqlparser` | Query | SQL 解析 | 步骤 17 |
| `github.com/cespare/xxhash/v2` | Common | 高性能哈希 | 步骤 3 |

## 8. Git 规范

- 分支：`main` 为主分支，开发从 `step/N-描述` 分支切出，完成后 PR 合并。
- 提交信息语言：中文。
- 禁止提交：二进制文件、测试生成的数据文件（`*.db`、`*.seg` 等已在 `.gitignore` 中）。
