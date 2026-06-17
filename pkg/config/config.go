// Package config 实现 WiDB 的 YAML 配置加载与带注释模板生成。
//
// 配置采用分层覆盖：默认值 < 配置文件 < 命令行参数。
// 配置文件查找顺序：-config 指定路径 > ./widb.yaml > ./config.yaml；
// 三者均不存在时使用默认值并可在当前目录生成带注释的 widb.yaml 模板。
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	yaml "go.yaml.in/yaml/v2"
)

// defaultTemplate 是嵌入的带注释默认配置模板。
//
//go:embed template.yaml
var defaultTemplate string

// Duration 是支持 Go duration 字符串（如 "5s"、"1m"）的 YAML 时间间隔类型。
// YAML 中写作字符串，加载后转为 time.Duration。
type Duration time.Duration

// UnmarshalYAML 实现 yaml.Unmarshaler，将字符串解析为 time.Duration。
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("无效的时间间隔 %q: %w", raw, err)
	}
	*d = Duration(parsed)
	return nil
}

// ServerConfig 是服务监听相关的配置。
type ServerConfig struct {
	TCPAddr  string `yaml:"tcp_addr"`
	HTTPAddr string `yaml:"http_addr"`
	PGAddr   string `yaml:"pg_addr"`
}

// StorageConfig 是存储引擎相关的配置。
type StorageConfig struct {
	DataDir         string `yaml:"data_dir"`
	MaxMemTableSize int64  `yaml:"max_memtable_size"`
}

// SchedulerConfig 是后台调度器相关的配置。
type SchedulerConfig struct {
	Enabled           bool     `yaml:"enabled"`
	FlushInterval     Duration `yaml:"flush_interval"`
	CompactInterval   Duration `yaml:"compact_interval"`
	WALCleanInterval  Duration `yaml:"wal_clean_interval"`
	WALCleanThreshold int64    `yaml:"wal_clean_threshold"`
}

// Config 是 WiDB 的完整 YAML 配置。
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Storage   StorageConfig   `yaml:"storage"`
	Scheduler SchedulerConfig `yaml:"scheduler"`
}

// Default 返回与原命令行默认值一致的默认配置。
func Default() Config {
	return Config{
		Server: ServerConfig{
			TCPAddr:  "0.0.0.0:9000",
			HTTPAddr: "0.0.0.0:8080",
			PGAddr:   "0.0.0.0:5432",
		},
		Storage: StorageConfig{
			DataDir:         "./data",
			MaxMemTableSize: 64 << 20,
		},
		Scheduler: SchedulerConfig{
			Enabled:           true,
			FlushInterval:     Duration(5 * time.Second),
			CompactInterval:   Duration(10 * time.Second),
			WALCleanInterval:  Duration(30 * time.Second),
			WALCleanThreshold: 64 << 20,
		},
	}
}

// Validate 校验配置合法性，返回首个不合法项的错误。
func (c Config) Validate() error {
	if c.Server.TCPAddr == "" {
		return errors.New("server.tcp_addr 不能为空")
	}
	if c.Server.HTTPAddr == "" {
		return errors.New("server.http_addr 不能为空")
	}
	if c.Storage.DataDir == "" {
		return errors.New("storage.data_dir 不能为空")
	}
	if c.Storage.MaxMemTableSize <= 0 {
		return fmt.Errorf("storage.max_memtable_size 必须为正数，当前 %d", c.Storage.MaxMemTableSize)
	}
	if c.Scheduler.Enabled {
		if c.Scheduler.FlushInterval < 0 {
			return errors.New("scheduler.flush_interval 不能为负数")
		}
		if c.Scheduler.CompactInterval < 0 {
			return errors.New("scheduler.compact_interval 不能为负数")
		}
		if c.Scheduler.WALCleanInterval < 0 {
			return errors.New("scheduler.wal_clean_interval 不能为负数")
		}
		if c.Scheduler.WALCleanThreshold < 0 {
			return fmt.Errorf("scheduler.wal_clean_threshold 不能为负数，当前 %d", c.Scheduler.WALCleanThreshold)
		}
	}
	return nil
}

// Load 从 path 读取并解析 YAML 配置，缺失字段用默认值填充。
func Load(path string) (Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Config{}, fmt.Errorf("读取配置文件 %q 失败: %w", path, err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置文件 %q 失败: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("配置文件 %q 不合法: %w", path, err)
	}
	return cfg, nil
}

// GenerateTemplate 将带注释的默认配置模板写入 path。
// 若 path 已存在则返回错误，避免覆盖用户配置。
func GenerateTemplate(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("配置文件 %q 已存在，拒绝覆盖", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查配置文件 %q 失败: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	if err := os.WriteFile(path, []byte(defaultTemplate), 0o644); err != nil {
		return fmt.Errorf("写入配置模板 %q 失败: %w", path, err)
	}
	return nil
}

// defaultConfigNames 是未指定 -config 时按顺序查找的默认配置文件名。
var defaultConfigNames = []string{"widb.yaml", "config.yaml"}

// ResolvePath 根据是否显式指定 configPath 返回应加载的配置文件路径。
//   - configPath 非空：直接返回（即使文件不存在，由调用方决定报错或生成）。
//   - configPath 为空：在 dir 目录下依次查找 widb.yaml、config.yaml，返回首个存在的路径。
//   - 均不存在：返回空字符串。
func ResolvePath(configPath, dir string) string {
	if configPath != "" {
		return configPath
	}
	for _, name := range defaultConfigNames {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
