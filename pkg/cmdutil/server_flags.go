// Package cmdutil 提供 cmd/server 与 cmd/widb 共享的命令行参数与配置加载逻辑。
//
// 重构动机：cmd/server 与 cmd/widb 各维护一份几乎相同的 cliFlags / newCLIFlags /
// applyOverrides / loadConfig / toServerConfig，且 flag 名、语义、默认值、覆盖规则完全
// 一致。两侧独立维护容易出现漂移（例如一方补了字段另一方忘记同步），
// 同时增加阅读成本。
//
// 本包将上述原语集中到 ServerFlags 类型中，cmd/* 仅保留 main 入口的胶水代码。
// 该包依赖 pkg/config、pkg/server、pkg/storage，但不被其他业务包依赖，
// 符合 AGENTS.md「pkg/server 可依赖所有 pkg」的边界。
package cmdutil

import (
	"flag"
	"log"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/config"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// ServerFlags 封装 server 与 widb 共用的命令行参数。
//
// 字段命名与原 cmd/{server,widb}/main.go 中 cliFlags 保持一致；默认值通过
// config.Default() 提供，命令行仅作为「显式覆盖」通道，因此所有字段零值表示「未设置」。
type ServerFlags struct {
	FS                   *flag.FlagSet
	ConfigPath           *string
	GenConfigPath        *string
	TCPAddr              *string
	HTTPAddr             *string
	PGAddr               *string
	DataDir              *string
	MaxMemTableSize      *int64
	EnableScheduler      *bool
	FlushInterval        *time.Duration
	CompactInterval      *time.Duration
	WALCleanInterval     *time.Duration
	WALCleanThreshold    *int64
	SlowQueryThresholdMS *int
	SlowQueryMaxEntries  *int
}

// NewServerFlags 在 fs 上注册全部 server/widb 共享 flag 并返回 ServerFlags。
// flagName 决定 FlagSet 名称（仅影响 Usage 输出，不影响行为）。
func NewServerFlags(flagName string) *ServerFlags {
	fs := flag.NewFlagSet(flagName, flag.ContinueOnError)
	return &ServerFlags{
		FS:                   fs,
		ConfigPath:           fs.String("config", "", "配置文件路径（YAML），未指定时依次查找 ./widb.yaml、./config.yaml"),
		GenConfigPath:        fs.String("gen-config", "", "生成带注释的默认配置模板到指定路径后退出"),
		TCPAddr:              fs.String("tcp", "", "TCP 监听地址（覆盖配置文件）"),
		HTTPAddr:             fs.String("http", "", "HTTP 监听地址（覆盖配置文件）"),
		PGAddr:               fs.String("pg", "", "PostgreSQL wire 协议监听地址（覆盖配置文件，留空禁用）"),
		DataDir:              fs.String("data", "", "数据目录（覆盖配置文件）"),
		MaxMemTableSize:      fs.Int64("max-memtable", 0, "MemTable 最大字节数（覆盖配置文件）"),
		EnableScheduler:      fs.Bool("scheduler", false, "启用后台调度器（覆盖配置文件）"),
		FlushInterval:        fs.Duration("scheduler.flush-interval", 0, "自动刷盘检查间隔（覆盖配置文件）"),
		CompactInterval:      fs.Duration("scheduler.compact-interval", 0, "自动 Compaction 检查间隔（覆盖配置文件）"),
		WALCleanInterval:     fs.Duration("scheduler.wal-clean-interval", 0, "WAL 清理检查间隔（覆盖配置文件）"),
		WALCleanThreshold:    fs.Int64("scheduler.wal-clean-threshold", 0, "WAL 文件大小阈值（覆盖配置文件）"),
		SlowQueryThresholdMS: fs.Int("slow-query-threshold-ms", -1, "慢查询判定阈值（毫秒），<= 0 禁用（覆盖配置文件，-1 表示未设置）"),
		SlowQueryMaxEntries:  fs.Int("slow-query-max-entries", -1, "慢查询日志环形缓冲容量（覆盖配置文件，-1 表示未设置）"),
	}
}

// SetFlags 返回显式传入的 flag 名集合，供 applyOverrides 判定哪些字段被覆盖。
// 公开给测试使用，避免散落字符串字面量。
func (f *ServerFlags) SetFlags() map[string]bool {
	set := make(map[string]bool, 13)
	f.FS.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
	return set
}

// ApplyOverrides 将命令行显式传入的参数覆盖到 cfg 上；未传参的字段保持 cfg 现状。
// 调用方应先以 config.Default() 初始化 cfg 再调用本方法。
func (f *ServerFlags) ApplyOverrides(cfg *config.Config) {
	set := f.SetFlags()
	if set["tcp"] {
		cfg.Server.TCPAddr = *f.TCPAddr
	}
	if set["http"] {
		cfg.Server.HTTPAddr = *f.HTTPAddr
	}
	if set["pg"] {
		cfg.Server.PGAddr = *f.PGAddr
	}
	if set["data"] {
		cfg.Storage.DataDir = *f.DataDir
	}
	if set["max-memtable"] {
		cfg.Storage.MaxMemTableSize = *f.MaxMemTableSize
	}
	if set["scheduler"] {
		cfg.Scheduler.Enabled = *f.EnableScheduler
	}
	if set["scheduler.flush-interval"] {
		cfg.Scheduler.FlushInterval = config.Duration(*f.FlushInterval)
	}
	if set["scheduler.compact-interval"] {
		cfg.Scheduler.CompactInterval = config.Duration(*f.CompactInterval)
	}
	if set["scheduler.wal-clean-interval"] {
		cfg.Scheduler.WALCleanInterval = config.Duration(*f.WALCleanInterval)
	}
	if set["scheduler.wal-clean-threshold"] {
		cfg.Scheduler.WALCleanThreshold = *f.WALCleanThreshold
	}
	if set["slow-query-threshold-ms"] {
		cfg.Server.SlowQueryThresholdMS = *f.SlowQueryThresholdMS
	}
	if set["slow-query-max-entries"] {
		cfg.Server.SlowQueryMaxEntries = *f.SlowQueryMaxEntries
	}
}

// LoadConfig 按分层策略加载配置：默认值 < 配置文件。
// configPath 非空时必须存在；为空时依次查找 ./widb.yaml、./config.yaml，
// 均不存在时使用默认值。返回值始终为非零 Config，便于调用方直接调用 Validate。
func LoadConfig(configPath string) (config.Config, error) {
	resolved := config.ResolvePath(configPath, ".")
	if resolved == "" {
		log.Printf("未找到配置文件，使用默认配置（可用 -gen-config widb.yaml 生成模板）")
		return config.Default(), nil
	}
	return config.Load(resolved)
}

// ToServerConfig 将 YAML 配置转换为服务层配置。
// 字段映射规则与原 cmd/server.toServerConfig / cmd/widb.toServerConfig 完全一致。
func ToServerConfig(cfg config.Config) server.Config {
	return server.Config{
		TCPAddr:         cfg.Server.TCPAddr,
		HTTPAddr:        cfg.Server.HTTPAddr,
		PGAddr:          cfg.Server.PGAddr,
		DataDir:         cfg.Storage.DataDir,
		MaxMemTableSize: cfg.Storage.MaxMemTableSize,
		EnableScheduler: cfg.Scheduler.Enabled,
		SchedulerConfig: storage.SchedulerConfig{
			FlushInterval:     time.Duration(cfg.Scheduler.FlushInterval),
			CompactInterval:   time.Duration(cfg.Scheduler.CompactInterval),
			WALCleanInterval:  time.Duration(cfg.Scheduler.WALCleanInterval),
			WALCleanThreshold: cfg.Scheduler.WALCleanThreshold,
		},
		SlowQueryThreshold: time.Duration(cfg.Server.SlowQueryThresholdMS) * time.Millisecond,
		SlowQueryCapacity:  cfg.Server.SlowQueryMaxEntries,
	}
}
