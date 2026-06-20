# WiDB 运维手册

> 本文档面向生产环境运维工程师（SRE / DBA），覆盖部署拓扑、监控告警、备份恢复、容量规划、升级回滚与灾难恢复。面向开发与贡献的文档请参阅 [development.md](development.md)，面向用户的快速上手请参阅 [getting-started.md](getting-started.md)。

## 目录

- [1. 部署拓扑](#1-部署拓扑)
- [2. 数据目录布局](#2-数据目录布局)
- [3. 启动与生命周期管理](#3-启动与生命周期管理)
- [4. 监控指标与告警](#4-监控指标与告警)
- [5. 健康检查](#5-健康检查)
- [6. 备份与恢复](#6-备份与恢复)
- [7. 容量规划](#7-容量规划)
- [8. 升级与回滚](#8-升级与回滚)
- [9. 灾难恢复](#9-灾难恢复)
- [10. 常见运维操作清单](#10-常见运维操作清单)
- [11. 安全建议](#11-安全建议)

## 1. 部署拓扑

WiDB 是单机 OLAP 数据库，不内置副本机制。生产部署的关键决策集中在「数据可靠性」与「查询性能」之间。

### 1.1 单节点部署（最简）

适用场景：开发测试、临时分析、对 SLA 不高的内部工具。

```
┌──────────────┐
│  widb-server │  ← TCP / HTTP / PG wire
└──────┬───────┘
       │  本地
       ▼
   ./data
```

```bash
./widb-server \
  -tcp  0.0.0.0:9000 \
  -http 0.0.0.0:8080 \
  -pg   0.0.0.0:5432 \
  -data /var/lib/widb \
  -max-memtable 134217728
```

### 1.2 反向代理后端（推荐生产拓扑）

将 WiDB 放在反向代理（Nginx / Envoy / Caddy）后，前端终止 TLS、统一鉴权、限流。WiDB 自身只监听内网地址。

```
                        TLS / 鉴权 / 限流
   Client ─────────► Nginx (或 Envoy) ─────────► widb-server
                      :443                          :9000/:8080/:5432
                                                 (仅监听 127.0.0.1
                                                  或内网网卡)
```

Nginx TCP 上游示例（用于 PG wire 长连接）：

```nginx
stream {
    upstream widb_pg {
        server 127.0.0.1:5432;
    }
    server {
        listen 0.0.0.0:5432 ssl;
        ssl_certificate     /etc/ssl/widb.crt;
        ssl_certificate_key /etc/ssl/widb.key;
        proxy_pass widb_pg;
        proxy_timeout 300s;
        proxy_connect_timeout 5s;
    }
}
```

HTTP 上游示例：

```nginx
http {
    upstream widb_http {
        server 127.0.0.1:8080;
        keepalive 32;
    }
    server {
        listen 0.0.0.0:8080 ssl;
        ssl_certificate     /etc/ssl/widb.crt;
        ssl_certificate_key /etc/ssl/widb.key;
        location / {
            proxy_pass http://widb_http;
            proxy_set_header X-Forwarded-For $remote_addr;
            proxy_read_timeout 60s;
        }
    }
}
```

### 1.3 共享存储 / 只读副本（高级）

WiDB 当前不内置副本，但「只读副本」可通过共享存储层 + 只读模式构造：

- 主节点：可写，挂载共享存储（iSCSI / NFS / 云盘）
- 只读节点：以 `-data` 指向同一存储，WiDB 启动时回放 WAL 并加载 Segment
- 写入路径：仅主节点开放
- 故障切换：主节点停写 → 切换只读节点为新主（修改 DNS/反向代理上游）

> ⚠️ 该模式仅适合「低频写 + 高频读」场景，主从切换需手工操作。WiDB 不保证双写时的脑裂防护，**禁止**主从同时开放写入。

### 1.4 系统要求

| 资源 | 最小 | 推荐 | 备注 |
|------|------|------|------|
| CPU  | 2 核 | 8+ 核 | Compaction 与查询并发模型偏 CPU-bound |
| 内存 | 1 GB | 8 GB+ | 至少能容纳 1.5× `max-memtable`；Block Cache 默认 256 MB |
| 磁盘 | SSD 20 GB | NVMe SSD 100 GB+ | 随机读是点查/范围扫描瓶颈，HDD 不推荐 |
| 文件系统 | ext4 / xfs | xfs | 不依赖特定 FS 特性，但需支持 `rename` 原子性 |
| 文件句柄 | 65536 | 1M+ | 每个连接 + 每个打开的 Segment 都会占用 fd |

## 2. 数据目录布局

`/var/lib/widb`（由 `-data` 指定）的标准布局：

```
/var/lib/widb/
├── catalog.json          # 元数据：所有表/列定义/引擎类型（JSON，原子写）
├── wal.log               # 当前活跃 WAL（顺序追加，周期性 fsync）
├── wal.log.prev          # 上一份归档的 WAL（Compaction 完成后保留）
├── segment_<id>.widb     # 不可变列式 Segment（字典/RLE/Bitmap + ZSTD）
├── segment_<id>.widb.tmp # 刷盘中正在构建的 Segment
└── ...（多个 segment_*.widb，ID 单调递增）
```

### 2.1 关键文件

| 文件 | 写入方 | 读出方 | 原子性 |
|------|--------|--------|--------|
| `catalog.json` | Create/Drop/Alter Table | 启动时 | `*.tmp` → `rename` |
| `wal.log` | 每次写入 | 启动时回放 | append + `fsync` |
| `wal.log.prev` | WAL 轮转/Compaction | 启动时回放 | rename 自 `wal.log` |
| `segment_*.widb` | Flush / Compaction | 查询 | `*.tmp` → `rename` |

### 2.2 不应手动修改的文件

- `catalog.json` / `*.tmp` 残留：通常 `.tmp` 是失败重试遗留，**直接删除**即可（启动时检测到 `.tmp` 会忽略并基于正式文件恢复）
- `wal.log` 中途截断：会触发「WAL 校验和失败」并**拒绝启动**——见 [9. 灾难恢复](#9-灾难恢复)
- `segment_*.widb` 改名/移动：会破坏 Sparse/Bloom 索引的 SegmentID 引用

### 2.3 权限

建议将 `data_dir` 所有者设为运行用户，权限 `0700`：

```bash
useradd -r -s /usr/sbin/nologin widb
mkdir -p /var/lib/widb
chown widb:widb /var/lib/widb
chmod 0700 /var/lib/widb
```

Systemd 单元示例（`/etc/systemd/system/widb.service`）：

```ini
[Unit]
Description=WiDB OLAP database
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=widb
Group=widb
ExecStart=/usr/local/bin/widb-server -data /var/lib/widb
Restart=on-failure
RestartSec=5
LimitNOFILE=1048576
# 优雅关闭：SIGTERM 触发 srv.Stop() 等所有连接处理完成
KillSignal=SIGTERM
TimeoutStopSec=30

# 资源限制
MemoryMax=8G
TasksMax=65536

# 安全加固
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/widb
PrivateTmp=true
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true

[Install]
WantedBy=multi-user.target
```

## 3. 启动与生命周期管理

### 3.1 启动顺序

1. **加载 catalog**：读取 `catalog.json`；不存在则建空库
2. **回放 WAL**：从 `wal.log`（必要时 `wal.log.prev`）顺序回放未刷盘的写入
3. **加载 Segment**：扫描 `segment_*.widb`，构建 Primary/Bloom/Sparse 索引
4. **启动后台调度器**：按 `scheduler.*` 配置触发 flush / compact / wal_clean
5. **绑定监听**：按 `server.tcp_addr` / `http_addr` / `pg_addr` 顺序 bind；任一失败整体退出

### 3.2 优雅关闭

收到 `SIGTERM` / `SIGINT` 后：

1. 停止接受新连接
2. 等待在途请求完成（默认等待 ≤ 30s，受 `-max-memtable` 间接影响）
3. 触发一次「强制 flush」：把 MemTable 写为 Segment
4. 关闭 WAL fd
5. 退出进程

```bash
systemctl stop widb       # 发送 SIGTERM
# 或
kill -TERM $(pidof widb-server)
```

**禁止** `kill -9`，会导致 MemTable 中未刷盘数据丢失（除非 SyncGroupCommit 模式且确实容忍最近 1ms 数据丢失）。

### 3.3 重启后的状态

| 模式 | 重启后行为 |
|------|-----------|
| LSM 引擎（默认） | 完整恢复：WAL 回放 + Segment 扫描 |
| Memory 引擎 | 数据清空（重启即丢），仅 catalog（表结构）保留 |
| 混合（LSM + memory 表） | LSM 表完整恢复，memory 表数据丢失 |

### 3.4 启动失败快速定位

```bash
# 1. 端口冲突
journalctl -u widb -n 50 | grep "address already in use"

# 2. 权限不足
journalctl -u widb -n 50 | grep "permission denied"
ls -ld /var/lib/widb

# 3. 数据损坏
journalctl -u widb -n 50 | grep -E "wal|catalog|segment"
# 见 [9. 灾难恢复]

# 4. 配置不合法
./widb-server -gen-config /tmp/c.yaml   # 生成模板对照
./widb-server -config /tmp/c.yaml -tcp "" -http "" -pg ""   # 仅校验不启动
```

## 4. 监控指标与告警

WiDB 通过 HTTP `/metrics` 端点暴露 Prometheus 指标（命名空间 `widb_`）。

### 4.1 核心指标分类

#### 4.1.1 查询（QPS / 延迟）

| 指标 | 类型 | 标签 | 含义 |
|------|------|------|------|
| `widb_queries_total` | Counter | `type=success\|parse_error\|analyze_error\|execute_error` | 查询总数 |
| `widb_query_duration_seconds` | Histogram | `type=sql` | 查询耗时分布（默认 buckets） |

**关键告警**：

```promql
# 错误率 > 1%
sum(rate(widb_queries_total{type!="success"}[5m]))
  / sum(rate(widb_queries_total[5m])) > 0.01

# P99 延迟 > 10s
histogram_quantile(0.99, sum(rate(widb_query_duration_seconds_bucket[5m])) by (le)) > 10
```

#### 4.1.2 写入（吞吐 / 错误）

| 指标 | 类型 | 标签 | 含义 |
|------|------|------|------|
| `widb_writes_total` | Counter | `result=success\|table_not_found\|convert_error\|write_error` | 写入总数 |
| `widb_write_duration_seconds` | Histogram | `result=success` | 写入耗时分布 |

**关键告警**：

```promql
# 写入错误率 > 0.5%
sum(rate(widb_writes_total{result!="success"}[5m]))
  / sum(rate(widb_writes_total[5m])) > 0.005

# 持续 5 分钟零写入（可能上游断开）
sum(rate(widb_writes_total[5m])) == 0
  and sum(widb_writes_total) > 0
```

#### 4.1.3 存储（MemTable / Segment / WAL）

| 指标 | 类型 | 标签 | 含义 |
|------|------|------|------|
| `widb_memtable_size_bytes` | Gauge | — | 当前 MemTable 字节数 |
| `widb_segment_count` | Gauge | `level=l0\|l1` | Segment 数量 |
| `widb_l0_segment_count` | Gauge | — | L0 Segment 数量 |
| `widb_wal_size_bytes` | Gauge | — | 当前 WAL 文件字节数 |
| `widb_flush_total` | Counter | — | flush 累计次数 |
| `widb_compact_total` | Counter | — | compaction 累计次数 |
| `widb_wal_clean_total` | Counter | — | WAL 清理累计次数 |

**关键告警**：

```promql
# MemTable 接近阈值（>80%）→ 写入即将触发 flush，可能引发写停顿
widb_memtable_size_bytes / (widb_memtable_size_bytes + 1)
  / (1024 * 1024 * 64) > 0.8   # 阈值按 max_memtable 调整

# L0 Segment 堆积（>20）→ Compaction 跟不上
widb_l0_segment_count > 20

# WAL 文件异常增长（>256MB）→ 调度器可能卡住
widb_wal_size_bytes > 256 * 1024 * 1024
```

#### 4.1.4 缓存

| 指标 | 类型 | 标签 | 含义 |
|------|------|------|------|
| `widb_cache_hits_total` | Counter | `cache=block\|index` | 命中数 |
| `widb_cache_misses_total` | Counter | `cache=block\|index` | 未命中数 |
| `widb_cache_size_bytes` | Gauge | `cache=block\|index` | 当前占用 |
| `widb_cache_entries` | Gauge | `cache=block\|index` | 条目数 |

**关键观察**（不是告警，是调优信号）：

```promql
# Block 缓存命中率（>80% 为健康）
sum(rate(widb_cache_hits_total{cache="block"}[5m]))
  / (sum(rate(widb_cache_hits_total{cache="block"}[5m]))
     + sum(rate(widb_cache_misses_total{cache="block"}[5m])))
```

#### 4.1.5 连接

| 指标 | 类型 | 含义 |
|------|------|------|
| `widb_active_connections` | Gauge | 当前活跃连接数 |

### 4.2 Prometheus 抓取配置

```yaml
scrape_configs:
  - job_name: widb
    scrape_interval: 15s
    static_configs:
      - targets: ['widb.internal:8080']
    metrics_path: /metrics
```

### 4.3 推荐 Grafana 仪表板

最少包含四个面板：

1. **Query**：QPS（按 type 分）、P50/P95/P99 延迟
2. **Write**：QPS（按 result 分）、P99 延迟、错误率
3. **Storage**：MemTable 大小、L0/L1 Segment 数量、WAL 大小
4. **Cache**：Block/Index 命中率与占用

## 5. 健康检查

### 5.1 进程级

```bash
# 进程存在
systemctl is-active widb

# 端口监听
ss -tlnp | grep -E ':(9000|8080|5432)'

# 进程响应
kill -0 $(pidof widb-server) && echo OK
```

### 5.2 协议级

```bash
# HTTP /health 端点（如有反向代理，可由代理对 /health 做 TCP/HTTP check）
curl -fsS http://127.0.0.1:8080/health || echo FAIL
# 期望：HTTP 200 + body `{"status":"ok"}`
# 若端点不存在：HTTP 404，由监控判定为不健康

# TCP 自定义协议最小请求（4 字节长度 + JSON）
python3 -c '
import socket, struct, json
s = socket.create_connection(("127.0.0.1", 9000), timeout=3)
req = json.dumps({"sql": "SELECT 1"}).encode()
s.send(struct.pack(">I", len(req)) + req)
n = struct.unpack(">I", s.recv(4))[0]
print(s.recv(n).decode())
s.close()
'
```

### 5.3 应用级 SQL 探针

```sql
-- 探针查询（注册到监控每 30s 跑一次）
SELECT 1;
SHOW TABLES;          -- 同时验证 catalog 可读
```

把探针的耗时加入监控（用 `widb_query_duration_seconds`），持续上升说明引擎在变慢。

## 6. 备份与恢复

### 6.1 备份范围

| 类别 | 文件 | 是否需要备份 |
|------|------|-------------|
| 必需 | `catalog.json` | ✅ |
| 必需 | `wal.log`（活跃） | ✅ |
| 必需 | `wal.log.prev` | ✅ |
| 必需 | `segment_*.widb` | ✅ |
| 不必 | `*.tmp` | ❌（失败残留，删除即可） |

### 6.2 备份策略

#### 6.2.1 冷备份（停机）

最安全：停机 → 整体 `tar` → 启动。

```bash
systemctl stop widb
tar -czf widb-$(date +%Y%m%d).tar.gz -C /var/lib widb
systemctl start widb
```

#### 6.2.2 热备份（在线）

由于 WiDB 的 Segment 一旦 flush 即不可变，**热备份天然一致**，无需停机：

```bash
# 用 rsync 增量（--delete-before 保证删除已删的 Segment）
rsync -a --delete /var/lib/widb/ backup@backup-host:/srv/widb-bak/$(date +%Y%m%d)/

# 或简单 cp -a
cp -a /var/lib/widb/. /srv/widb-bak/$(date +%Y%m%d)/
```

> ✅ 在线备份安全的前提：WAL 顺序追加（不可能产生新数据但 Segment 已不在快照内），catalog.json 用 tmp+rename 原子写（快照中要么是旧版要么是新版）。

#### 6.2.3 推荐频率

- WAL 每 5 分钟备份一次（保护近 5 分钟未刷盘数据）
- Segment/Catalog 每小时一次（变化小、但量大）

### 6.3 恢复

#### 6.3.1 整库恢复

```bash
systemctl stop widb
rm -rf /var/lib/widb
tar -xzf widb-20260620.tar.gz -C /var/lib
chown -R widb:widb /var/lib/widb
systemctl start widb
```

启动时自动：catalog 加载 → WAL 回放 → Segment 扫描。

#### 6.3.2 部分恢复（仅丢失 Segment）

```bash
# 假设 segment_00042.widb 损坏
systemctl stop widb
cd /var/lib/widb
mv segment_00042.widb segment_00042.widb.corrupt   # 保留供分析
# 从备份恢复该文件
cp /srv/widb-bak/20260620/segment_00042.widb .
chown widb:widb segment_00042.widb
systemctl start widb
```

#### 6.3.3 误删表恢复

WiDB 当前**没有 DROP TABLE 的回收站**。`DROP TABLE` 会立即从 `catalog.json` 删除并触发后台清理。**预防胜于治疗**：

- 关键表的 `DROP TABLE` 走变更审批 + 备份验证后再执行
- 误删后从最近的整库备份恢复（会丢失备份点之后的所有写入）

## 7. 容量规划

### 7.1 磁盘容量估算

```
总磁盘占用 ≈ catalog.json (KB 级)
            + Σ(segment_*.widb)   // 落盘后的不可变数据
            + 2 × max_memtable     // 活跃 WAL + MemTable 上限
            + 写放大系数 × 原始数据  // LSM 写放大，通常 1.5×~3×
```

**经验公式**（压缩率 ~3×、写放大 ~2×）：

```
所需磁盘 ≈ 原始数据量 × 2 / 3
```

示例：每天新增 100 GB 原始数据 → 需要 ~67 GB/天磁盘 → 保留 30 天需 ~2 TB。

### 7.2 内存容量估算

| 组件 | 默认 | 估算 |
|------|------|------|
| MemTable | 64 MB | 由 `max_memtable` 配置（最大可放 2×） |
| Block Cache | 256 MB | 由内部常量（`defaultBlockCacheSize`） |
| Index Cache | 1000 条 | 由内部常量（`defaultIndexCacheEntries`） |
| 客户端连接 | ~10 KB/连接 | `active_conns × 10 KB` |
| 进程自身 | ~50 MB | 基础开销 |

**建议**：总内存 ≥ `2 × max_memtable + 256 MB + 系统预留`。

示例：max_memtable=128MB → 建议 ≥ 768 MB（不含操作系统 / 其他进程）。

### 7.3 文件句柄

Segment 一旦打开会被保持 fd 用于查询。

```
所需 fd ≈ active_conns + 2 × segment_count + 系统预留
```

`-max-memtable` 越大、刷盘越不频繁 → 内存 Segment 越多 → fd 越紧张。Systemd 单元中已设 `LimitNOFILE=1048576`。

### 7.4 性能扩容路径

| 瓶颈 | 现象 | 措施 |
|------|------|------|
| 写吞吐低 | `widb_write_duration_seconds` P99 上升 | 改用 `SyncGroupCommit`（容忍最近 1ms 丢失可换 2×+ 吞吐） |
| 写停顿 | `widb_memtable_size_bytes` 长期接近阈值 | 增大 `max_memtable`（更多写缓冲） |
| 范围扫慢 | `widb_cache_misses_total` 居高 | 增大 Block Cache（修改 `defaultBlockCacheSize`） |
| Compaction 跟不上 | `widb_l0_segment_count` 持续 > 20 | 升级磁盘到 NVMe / 调小 Compaction 触发阈值 |
| 内存吃紧 | OOM | 减小 `max_memtable` + 减小 `BlockCache` |

> WiDB 当前未将这些参数全部暴露为运行时配置；如需调整，需重新编译。详见 [development.md](development.md)。

## 8. 升级与回滚

### 8.1 升级前准备

1. 查阅 [CHANGELOG / release notes]，确认是否涉及：
   - 数据格式变更（Segment / WAL / catalog 的 schema 升级）
   - 配置项重命名
   - 协议破坏性变更
2. **冷备份**（停机备份）整库
3. 在 staging 跑 [5. 健康检查](#5-健康检查) + 关键 SQL 探针
4. 准备回滚方案（同版本二进制 + 同配置）

### 8.2 升级步骤

```bash
# 1. 停服（优雅）
systemctl stop widb
sleep 5                                # 给 flush 留时间
ps -ef | grep widb-server | grep -v grep || echo "已停止"

# 2. 备份
tar -czf /srv/widb-bak/before-upgrade-$(date +%Y%m%d).tar.gz -C /var/lib widb

# 3. 替换二进制
install -m 0755 widb-server-v1.x.y /usr/local/bin/widb-server
# 或：
cp widb-server-v1.x.y /usr/local/bin/widb-server.new
mv /usr/local/bin/widb-server.new /usr/local/bin/widb-server

# 4. 检查配置兼容
./widb-server -config /etc/widb/widb.yaml -gen-config /tmp/new.yaml
diff /etc/widb/widb.yaml /tmp/new.yaml

# 5. 启动
systemctl start widb
systemctl status widb

# 6. 健康检查
curl -fsS http://127.0.0.1:8080/health
```

### 8.3 回滚

```bash
# 1. 停服
systemctl stop widb

# 2. 恢复数据
rm -rf /var/lib/widb
tar -xzf /srv/widb-bak/before-upgrade-YYYYMMDD.tar.gz -C /var/lib

# 3. 回滚二进制
cp /usr/local/bin/widb-server.v1.x.y /usr/local/bin/widb-server

# 4. 启动 + 验证
systemctl start widb
```

### 8.4 跨大版本升级

当数据格式不兼容时（catalog schema 变更），WiDB 会拒绝启动并打印迁移指引。当前**没有自动迁移工具**，需走：

1. 旧版本导出（暂未提供 SQL `EXPORT`/`DUMP`）→ 手工通过应用层 API 拉数据
2. 新版本创建空表
3. 写入

如需跨大版本升级，请联系维护者获取迁移脚本。

## 9. 灾难恢复

### 9.1 故障分类

| 类别 | 现象 | 严重度 |
|------|------|--------|
| 进程崩溃（OOM/panic） | systemd 自动重启 | 中（重启即恢复） |
| 磁盘满 | 写入失败、WAL 切分失败 | 高（需扩容） |
| catalog.json 损坏 | 启动失败 | 高（需从备份恢复） |
| WAL 末尾截断 | 启动失败「WAL 校验和错误」 | 中（可截断到上一完整 record） |
| Segment 文件损坏 | 该 Segment 上的查询失败 | 中（删除该 Segment，从备份恢复） |
| 整盘损坏 | 全部数据丢失 | 灾难（需从异地备份恢复） |

### 9.2 常见故障处理 SOP

#### 9.2.1 进程 OOM

```bash
journalctl -u widb -n 200 | grep -E "oom|killed"
dmesg | grep -i "killed process" | tail

# 措施：减 max_memtable + 减 BlockCache，或扩容内存
systemctl edit widb
# 在 [Service] 段加：
#   MemoryMax=4G
# 重新加载 + 启动
```

#### 9.2.2 磁盘满

```bash
df -h /var/lib/widb
du -sh /var/lib/widb /var/lib/widb/* | sort -h | tail

# 短期：清 *.tmp 残留（一定是失败重试遗留）
find /var/lib/widb -name '*.tmp' -delete

# 长期：扩容 / 加盘 → 软链接迁移数据目录
systemctl stop widb
mv /var/lib/widb /mnt/new-disk/widb
ln -s /mnt/new-disk/widb /var/lib/widb
systemctl start widb
```

#### 9.2.3 catalog.json 损坏

```bash
# 1. 停服
systemctl stop widb

# 2. 检查是否真的损坏
python3 -m json.tool /var/lib/widb/catalog.json | head -5

# 3. 检查是否有 .tmp 残留（写入崩溃遗留的完整版本）
ls -la /var/lib/widb/catalog.json*

# 4. 用 .tmp 替换
if [ -f /var/lib/widb/catalog.json.tmp ]; then
  mv /var/lib/widb/catalog.json /var/lib/widb/catalog.json.bad
  mv /var/lib/widb/catalog.json.tmp /var/lib/widb/catalog.json
fi

# 5. 仍失败则从备份恢复
```

#### 9.2.4 WAL 校验和错误

WAL 末尾损坏可截断到上一条完整记录（不丢任何已落盘数据，但可能丢最后几条未刷盘的 MemTable 记录）：

```bash
# WiDB 当前不提供内建工具，请保留损坏的 wal.log 后联系维护者
# 临时绕过：从最新备份恢复
```

#### 9.2.5 单个 Segment 损坏

```bash
# 启动日志会指明哪个 segment_*.widb 损坏
journalctl -u widb -n 200 | grep "segment_.*corrupt\|read error"

# 1. 隔离损坏文件
mv /var/lib/widb/segment_00042.widb /var/lib/widb/segment_00042.widb.corrupt

# 2. 启动
systemctl start widb
# 注：丢失该 Segment 内的数据点，需要从最近备份恢复或上游重放
```

### 9.3 异地备份（推荐）

使用对象存储（S3 / OSS / COS）做异地冷备：

```bash
# /etc/cron.d/widb-backup
*/15 * * * * widb rsync -a --delete /var/lib/widb/ /srv/widb-bak/$(date +\%Y\%m\%d-\%H\%M)/ && \
  aws s3 sync /srv/widb-bak/$(date +\%Y\%m\%d-\%H\%M)/ s3://my-widb-bak/$(date +\%Y/\%m/\%d/)/
```

保留策略：近 24 小时每 15 分钟一版，最近 7 天每天一版，最近 12 周每周一版。

## 10. 常见运维操作清单

| 任务 | 命令 | 注意事项 |
|------|------|---------|
| 查看监听地址 | `curl http://127.0.0.1:8080/status` | 或 `ss -tlnp` |
| 强制 flush（刷盘） | `curl -X POST http://127.0.0.1:8080/admin/flush` | 当前可能未实现，需重启 |
| 强制 compaction | 同上 | 同上 |
| 查看 Segment 数量 | `curl http://127.0.0.1:8080/metrics \| grep widb_segment_count` | — |
| 查看 WAL 大小 | `curl http://127.0.0.1:8080/metrics \| grep widb_wal_size_bytes` | — |
| 查表 | `SELECT * FROM t LIMIT 10`（widb-cli） | — |
| 修改配置 | 编辑 `/etc/widb/widb.yaml` → `systemctl restart widb` | 涉及刷盘时机，谨慎 |
| 数据目录迁移 | 停服 → `mv` → 软链 → 启动 | 见 [9.2.2](#922-磁盘满) |
| 升级二进制 | 见 [8.2](#82-升级步骤) | — |
| 回滚 | 见 [8.3](#83-回滚) | — |
| 主动清空内存表 | 重启 widb | 内存表数据会丢 |
| 重置整库 | `rm -rf /var/lib/widb && systemctl start widb` | **不可逆**，先备份 |

## 11. 安全建议

### 11.1 网络层

- **绝不**把 TCP / HTTP / PG 端口暴露到公网
- 生产用 0.0.0.0 监听 + 反向代理统一 TLS；或直接监听 `127.0.0.1` / 内网网卡
- PG wire 当前仅支持 `trust` 认证（无密码），**必须**靠网络边界限制

### 11.2 文件系统

- `data_dir` 权限 `0700`，owner 设为运行用户
- 不要把 `data_dir` 放在 NFS（影响 fsync 语义）
- 定期 `fsck` 检查底层文件系统

### 11.3 资源限制

```ini
# systemd 单元
LimitNOFILE=1048576
MemoryMax=8G
CPUQuota=400%   # 限 4 核，避免 Compaction 抢占查询
```

### 11.4 审计

WiDB 当前**没有 SQL 审计日志**。如需审计，可：

- 在反向代理层记录 HTTP 请求（含 SQL body）
- 在客户端 SDK 层埋点

### 11.5 凭据

- 配置文件中**禁止**放明文密码（当前无密码配置项，但未来扩展需注意）
- TLS 证书走文件 + `chmod 0600` + 独立用户，不放 git

## 附录 A：与 `widb-cli` 的运维操作

```bash
# 查看所有表
./widb-cli -e "SHOW TABLES;"

# 查看表结构
./widb-cli -e "DESCRIBE sensor;"

# 慢查询（如果开了慢查询日志）
./widb-cli -e "SELECT * FROM sensor WHERE id = 1;"

# 批量删除（需要 LIMIT 支持，否则分批 WHERE）
./widb-cli -e "DELETE FROM sensor WHERE ts < '2024-01-01';"
```

> 完整 SQL 子集与限制见 [sql-reference.md](sql-reference.md#10-已知限制与注意事项)。

## 附录 B：相关文档

- [getting-started.md](getting-started.md) — 快速上手
- [architecture.md](architecture.md) — 系统架构
- [performance.md](performance.md) — 性能调优
- [troubleshooting.md](troubleshooting.md) — 故障排查 FAQ
- [development.md](development.md) — 开发与构建
- [storage.md](storage.md) — 存储引擎详解（WAL/Segment/Compaction 行为细节）
- [server.md](server.md) — 服务层详解（指标/协议细节）
