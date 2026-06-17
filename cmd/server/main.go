// Package main 是 test-db 服务器的入口点。
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/what-is-me-vibe-coding/test-db/pkg/config"
	"github.com/what-is-me-vibe-coding/test-db/pkg/server"
	"github.com/what-is-me-vibe-coding/test-db/pkg/storage"
)

// run 启动服务器并等待终止信号，用于支持测试。
func run(tcpAddr, httpAddr, dataDir string, maxMemTableSize int64, enableScheduler bool, schedulerCfg storage.SchedulerConfig, opts ...server.Option) error {
	cfg := server.Config{
		TCPAddr:         tcpAddr,
		HTTPAddr:        httpAddr,
		DataDir:         dataDir,
		MaxMemTableSize: maxMemTableSize,
		EnableScheduler: enableScheduler,
		SchedulerConfig: schedulerCfg,
	}

	srv, err := server.NewServer(cfg, opts...)
	if err != nil {
		return err
	}

	if err := srv.Start(); err != nil {
		return err
	}

	// 等待终止信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("收到信号 %v，正在关闭...", sig)

	return srv.Stop()
}

// cliFlags 封装命令行参数。
// 命令行参数的默认值留空（或零值），由配置文件提供默认值；显式传入的参数覆盖配置文件。
type cliFlags struct {
	fs                *flag.FlagSet
	configPath        *string
	genConfigPath     *string
	tcpAddr           *string
	httpAddr          *string
	pgAddr            *string
	dataDir           *string
	maxMemTableSize   *int64
	enableScheduler   *bool
	flushInterval     *time.Duration
	compactInterval   *time.Duration
	walCleanInterval  *time.Duration
	walCleanThreshold *int64
}

// newCLIFlags 构建命令行参数集。
func newCLIFlags() *cliFlags {
	fs := flag.NewFlagSet("test-db", flag.ContinueOnError)
	return &cliFlags{
		fs:                fs,
		configPath:        fs.String("config", "", "配置文件路径（YAML），未指定时依次查找 ./widb.yaml、./config.yaml"),
		genConfigPath:     fs.String("gen-config", "", "生成带注释的默认配置模板到指定路径后退出"),
		tcpAddr:           fs.String("tcp", "", "TCP 监听地址（覆盖配置文件）"),
		httpAddr:          fs.String("http", "", "HTTP 监听地址（覆盖配置文件）"),
		pgAddr:            fs.String("pg", "", "PostgreSQL wire 协议监听地址（覆盖配置文件，留空禁用）"),
		dataDir:           fs.String("data", "", "数据目录（覆盖配置文件）"),
		maxMemTableSize:   fs.Int64("max-memtable", 0, "MemTable 最大字节数（覆盖配置文件）"),
		enableScheduler:   fs.Bool("scheduler", false, "启用后台调度器（覆盖配置文件）"),
		flushInterval:     fs.Duration("scheduler.flush-interval", 0, "自动刷盘检查间隔（覆盖配置文件）"),
		compactInterval:   fs.Duration("scheduler.compact-interval", 0, "自动 Compaction 检查间隔（覆盖配置文件）"),
		walCleanInterval:  fs.Duration("scheduler.wal-clean-interval", 0, "WAL 清理检查间隔（覆盖配置文件）"),
		walCleanThreshold: fs.Int64("scheduler.wal-clean-threshold", 0, "WAL 文件大小阈值（覆盖配置文件）"),
	}
}

// loadConfig 按分层策略加载配置：默认值 < 配置文件。
// configPath 非空时必须存在；为空时依次查找 ./widb.yaml、./config.yaml，均不存在则使用默认值。
func loadConfig(configPath string) (config.Config, error) {
	resolved := config.ResolvePath(configPath, ".")
	if resolved == "" {
		log.Printf("未找到配置文件，使用默认配置（可用 -gen-config widb.yaml 生成模板）")
		return config.Default(), nil
	}
	return config.Load(resolved)
}

// applyOverrides 将显式设置的命令行参数覆盖到配置上。
func (c *cliFlags) applyOverrides(cfg *config.Config) {
	set := make(map[string]bool, 11)
	c.fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["tcp"] {
		cfg.Server.TCPAddr = *c.tcpAddr
	}
	if set["http"] {
		cfg.Server.HTTPAddr = *c.httpAddr
	}
	if set["pg"] {
		cfg.Server.PGAddr = *c.pgAddr
	}
	if set["data"] {
		cfg.Storage.DataDir = *c.dataDir
	}
	if set["max-memtable"] {
		cfg.Storage.MaxMemTableSize = *c.maxMemTableSize
	}
	if set["scheduler"] {
		cfg.Scheduler.Enabled = *c.enableScheduler
	}
	if set["scheduler.flush-interval"] {
		cfg.Scheduler.FlushInterval = config.Duration(*c.flushInterval)
	}
	if set["scheduler.compact-interval"] {
		cfg.Scheduler.CompactInterval = config.Duration(*c.compactInterval)
	}
	if set["scheduler.wal-clean-interval"] {
		cfg.Scheduler.WALCleanInterval = config.Duration(*c.walCleanInterval)
	}
	if set["scheduler.wal-clean-threshold"] {
		cfg.Scheduler.WALCleanThreshold = *c.walCleanThreshold
	}
}

// toServerConfig 将 YAML 配置转换为服务层配置。
func toServerConfig(cfg config.Config) server.Config {
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
	}
}

// runMainWithArgs 解析命令行参数并启动服务器，返回退出码。
// 使用自定义 FlagSet 以支持在测试中多次调用。
func runMainWithArgs(args []string) int {
	c := newCLIFlags()

	if err := c.fs.Parse(args); err != nil {
		return 2
	}

	if *c.genConfigPath != "" {
		if err := config.GenerateTemplate(*c.genConfigPath); err != nil {
			log.Printf("生成配置模板失败: %v", err)
			return 1
		}
		log.Printf("已生成配置模板: %s", *c.genConfigPath)
		return 0
	}

	cfg, err := loadConfig(*c.configPath)
	if err != nil {
		log.Printf("加载配置失败: %v", err)
		return 1
	}

	c.applyOverrides(&cfg)

	if err := cfg.Validate(); err != nil {
		log.Printf("配置不合法: %v", err)
		return 1
	}

	serverCfg := toServerConfig(cfg)
	srv, err := server.NewServer(serverCfg)
	if err != nil {
		log.Printf("服务器错误: %v", err)
		return 1
	}

	if err := srv.Start(); err != nil {
		log.Printf("服务器错误: %v", err)
		return 1
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("收到信号 %v，正在关闭...", sig)

	if err := srv.Stop(); err != nil {
		log.Printf("关闭错误: %v", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(runMainWithArgs(os.Args[1:]))
}
