# PostgreSQL wire 协议详解

## 1. 概述

WiDB 通过 `pkg/server/pgwire/` 实现 PostgreSQL wire 协议（v3）服务端，使标准 PostgreSQL 客户端（`psql`、pgJDBC 驱动、BI 工具如 Superset/Metabase/Grafana）可直接连接 WiDB，无需专用驱动。

实现基于 `github.com/jackc/pgproto3/v2`（MIT，pgx 生态事实标准），自建连接处理循环。当前支持：

- **认证方式**：trust（AuthenticationOk，无密码）
- **协议模式**：
  - Simple Query（`'Q'` 消息，客户端发送整条 SQL，服务端一次性返回结果）
  - Extended Query（`Parse / Bind / Describe / Execute / Sync` 序列，支持 prepared statement 与 portal 复用，覆盖 pgx、psql、DBeaver、DataGrip、Navicat 等真实客户端的协议路径）
- **资源保护**：最大并发连接、空闲超时、写入超时

## 2. 启用与配置

PG wire 默认监听 `0.0.0.0:5432`。三种配置方式（优先级：命令行 > 配置文件 > 默认值）：

```bash
# 1. 命令行参数（覆盖配置文件）
./widb-server -pg 0.0.0.0:5432

# 2. 配置文件 widb.yaml
# server:
#   pg_addr: "0.0.0.0:5432"

# 3. 留空禁用
./widb-server -pg ""
```

`widb`（一键启动）与 `widb-server` 共享同一参数。启动后 `\addrs` 命令会显示 PG 监听地址。

## 3. 连接示例

### psql

```bash
# 执行单条查询
psql -h 127.0.0.1 -p 5432 -c "SELECT id, name FROM sensor WHERE id = 1"

# 交互模式
psql -h 127.0.0.1 -p 5432
```

### JDBC（Java）

```java
String url = "jdbc:postgresql://127.0.0.1:5432/";
Connection conn = DriverManager.getConnection(url);
Statement st = conn.createStatement();
ResultSet rs = st.executeQuery("SELECT id, name FROM sensor WHERE id = 1");
```

### Python（psycopg）

```python
import psycopg
conn = psycopg.connect("host=127.0.0.1 port=5432")
cur = conn.execute("SELECT id, name FROM sensor WHERE id = 1")
for row in cur:
    print(row)
```

## 4. 协议流程

### 4.1 Simple Query

```
客户端                          服务端
  │                               │
  │── StartupMessage ────────────▶│   (含数据库名/用户名)
  │◀─ AuthenticationOk ───────────│   (trust 认证直接通过)
  │◀─ ParameterStatus* ───────────│   (server_version 等)
  │◀─ BackendKeyData ─────────────│
  │◀─ ReadyForQuery ──────────────│   (进入就绪状态)
  │                               │
  │── Query("SELECT ...") ───────▶│
  │◀─ RowDescription ─────────────│   (列名 + 类型 OID)
  │◀─ DataRow* ───────────────────│   (每行一个 DataRow)
  │◀─ CommandComplete ────────────│   (如 "SELECT 3")
  │◀─ ReadyForQuery ──────────────│
  │                               │
  │── Terminate ─────────────────▶│   (断开连接)
```

错误时服务端发送 `ErrorResponse`（含 severity/code/message 字段）后回到 `ReadyForQuery`，连接保持可用。

### 4.2 Extended Query

Extended Query 协议将一次查询拆为四个阶段，由客户端通过 `Parse` → `Bind` → `Describe` → `Execute` 消息驱动，并以 `Sync` 结束一个周期。PG 规范要求服务端在收到 `Sync` 后返回 `ReadyForQuery` 才会继续处理后续消息。

```
客户端                          服务端
  │                               │
  │── Parse("", sql, []) ────────▶│   准备预编译语句
  │◀─ ParseComplete ──────────────│
  │── Bind("", "", [], []) ──────▶│   绑定参数到 portal
  │◀─ BindComplete ───────────────│
  │── Describe('P', "") ─────────▶│   询问 portal 描述
  │◀─ NoData ────────────────────│   (Execute 阶段再补 RowDescription)
  │── Execute("", 0) ────────────▶│   执行
  │◀─ RowDescription ────────────│   (如返回结果集)
  │◀─ DataRow* ──────────────────│
  │◀─ CommandComplete ───────────│
  │── Sync ──────────────────────▶│   周期结束
  │◀─ ReadyForQuery ─────────────│
  │                               │
  │── Close('S', "stmt1") ───────▶│   释放预编译语句
  │◀─ CloseComplete ─────────────│
  │── Terminate ─────────────────▶│
```

错误处理（按 PG 规范）：
- `Parse` / `Bind` / `Describe` 失败 → 服务端返回 `ErrorResponse` 并进入错误状态，吸收后续消息直到 `Sync`，`Sync` 返回 `ReadyForQuery` 后状态清空。
- `Execute` 失败 → 仅返回 `ErrorResponse`，**不**进入错误状态（PG 规范允许同连接继续执行）。
- `Close` 始终成功（即使对象不存在），用于客户端清理命名空间。

### 4.3 WiDB Extended Query 简化语义

为减少实现复杂度，WiDB 的 Extended Query 路径相对 PG 标准做了如下简化（与上游行为一致，客户端通常无感知）：

| 简化点 | 说明 | 客户端影响 |
|--------|------|-----------|
| 占位符不被解析 | `$1`/`$2` 等占位符不被替换，Bind 携带的参数值被忽略 | 客户端不应使用 server-side 参数化；改用字符串拼接或客户端拼接 |
| Describe 一律返回 `NoData` | `RowDescription` 在 `Execute` 阶段按实际结果集补发 | 不影响：客户端通常在拿到 DataRow 之后才关心列定义 |
| 错误状态严格遵循 PG 规范 | 错误状态吸收消息直到 `Sync` | 不影响：所有合规驱动均按规范处理 |

e2e 测试 `tests/integration/e2e_pgwire_extended_query_test.go` 已覆盖以下场景，可作为参考实现：

| 测试用例 | 覆盖点 |
|----------|--------|
| `TestPGExtQuerySelect` | SELECT 全表 + 列投影 + CommandComplete 标签 |
| `TestPGExtQueryPointRange` | 点查、范围 (`>=`/`<=`)、`LIKE` |
| `TestPGExtQueryGroupByAggregate` | `GROUP BY` + `COUNT/SUM` |
| `TestPGExtWriteDML` | `UPDATE/DELETE` 受影响行数 + 后续 `SELECT` 验证 |
| `TestPGExtMetaCommands` | `SHOW TABLES` / `DESCRIBE` 元命令 |
| `TestPGExtNullValue` | `NULL` 在 Extended Query 路径下的编解码 |
| `TestPGExtErrorRecovery` | 错误 SQL 触发 `ErrorResponse` 后连接仍可用 |
| `TestPGExtMultiClientParallel` | 6 客户端并发 prepared statement 命名空间隔离 |
| `TestPGExtRoundTrip` | 一次周期内消息序列断言（1/2/n/T/D/C/Z） |

## 5. 类型映射

WiDB 类型到 PostgreSQL 类型的映射（`pkg/server/pgwire/types.go`）：

| WiDB 类型 | PG OID | PG 类型名 | 格式 |
|-----------|--------|-----------|------|
| BOOL | 16 | bool | 文本 "t"/"f" |
| INT8 | 21 | int2 | 文本数字 |
| INT16 | 21 | int2 | 文本数字 |
| INT32 | 23 | int4 | 文本数字 |
| INT64 | 20 | int8 | 文本数字 |
| UINT64 | 20 | int8 | 文本数字 |
| FLOAT64 | 701 | float8 | 文本数字 |
| STRING | 25 | text | 文本 |
| DATE | 1082 | date | "YYYY-MM-DD" |
| TIMESTAMP | 1114 | timestamp | RFC3339Nano |

类型推断优先使用查询计划的 Schema 列类型；Schema 缺失时回退到从结果行值推断（`inferTypeFromValue`），全为 NULL 的列默认 TEXT。

## 6. 资源保护与安全

为防止恶意客户端耗尽服务端 goroutine，`NewServer` 默认启用：

| 保护项 | 默认值 | 选项 | 说明 |
|--------|--------|------|------|
| 最大并发连接 | 256 | `WithMaxConns(n)` | 超限连接立即关闭，<=0 不限制 |
| 单次读取空闲超时 | 5 分钟 | `WithIdleTimeout(d)` | 空闲连接自动断开 |
| 单次写入超时 | 30 秒 | `WithWriteTimeout(d)` | 慢客户端不阻塞 goroutine |

### 安全注意事项

当前认证方式为 **trust**，任何能连上监听端口的客户端均可执行 SQL。在不可信网络中部署时：

1. 将监听地址绑定到回环或内网（如 `127.0.0.1` 或内网 IP），避免暴露到公网；
2. 在网络边界（防火墙/安全组）限制 5432 端口访问来源；
3. 通过反向代理或带认证的网关前置 pgwire 端口。

## 7. 实现结构

| 文件 | 职责 |
|------|------|
| `server.go` | 监听、accept 循环、连接限流、优雅停机 |
| `conn.go` | 单连接处理：Startup 握手、Query 循环、消息分发（Simple + Extended） |
| `conn_extended.go` | Extended Query 协议：`Parse/Bind/Describe/Execute/Close/Sync` 处理与错误状态机 |
| `executor.go` | `SQLExecutor` 接口定义（由服务层 `pgwireAdapter` 实现） |
| `encode.go` | 结果行编码为 DataRow、RowDescription 构造 |
| `types.go` | WiDB DataType → PG OID 映射、类型推断 |

`pgwireAdapter`（`pkg/server/pgwire_adapter.go`）将 `*Server` 适配为 `pgwire.SQLExecutor`，复用现有的 parser → analyzer → optimizer → executor 管线，与 TCP/HTTP 协议走完全相同的执行路径。
