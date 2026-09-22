// Package config 负责应用配置的加载：configs/config.yaml 为默认值来源，
// 环境变量以 MODEL_BSS_ 为前缀覆盖同名键（详见 internal/infra/config/env.go）。
package config

import (
	"time"
)

// Config 是应用全部配置的聚合。当前阶段（骨架）只实际使用 Server / Database / Log，
// 其余字段为后续阶段预留，避免配置结构随模块推进反复迁移。
type Config struct {
	Server   Server   `mapstructure:"server"`
	Database Database `mapstructure:"db"`
	Redis    Redis    `mapstructure:"redis"`
	Auth     Auth     `mapstructure:"auth"`
	Log      Log      `mapstructure:"log"`
	Worker   Worker   `mapstructure:"worker"`
	Docs     Docs     `mapstructure:"docs"`
}

// Docs 在线接口调试页（Swagger UI）配置。
// 默认开启（见 env.go 的 viper.SetDefault）：这是个内部 BSS 系统，联调期前后端都要用。
// 生产部署请显式置 false（docs.enable=false 或 MODEL_BSS_DOCS_ENABLE=false）。
type Docs struct {
	Enable bool `mapstructure:"enable"`
}

// Worker 后台任务调度配置（阶段 6a）。
// 单源纪律：worker 是进程级基础设施，开关/频率只在这里配，**不进 sys_config**
// （sys_config 只放业务参数：宽限期、异常阈值、补录上限等）。
type Worker struct {
	Enable bool       `mapstructure:"enable"`
	Jobs   WorkerJobs `mapstructure:"jobs"`
}

// WorkerJobs 是各任务的独立开关与频率。
// 每日任务的 daily 按 db.timezone（Asia/Shanghai）解析，不用 UTC。
type WorkerJobs struct {
	ActivateQuote    JobConfig `mapstructure:"activate_quote"`
	QuoteExpireScan  JobConfig `mapstructure:"quote_expire_scan"`
	QuoteExpireFinal JobConfig `mapstructure:"quote_expire_final"`
	QuoteAnomalyScan JobConfig `mapstructure:"quote_anomaly_scan"`
	CostRecalc       JobConfig `mapstructure:"cost_recalc"`
}

// JobConfig 是单个任务的配置。
// Interval > 0 时为分钟级轮询任务；Daily 非空（"HH:MM"）时为每日任务（cron_lock 防重）。
type JobConfig struct {
	Enable   bool          `mapstructure:"enable"`
	Interval time.Duration `mapstructure:"interval"`
	Daily    string        `mapstructure:"daily"`
}

// Server HTTP 服务配置。
type Server struct {
	Host         string        `mapstructure:"host"`
	Port         int           `mapstructure:"port"`
	Mode         string        `mapstructure:"mode"`          // debug | release
	Timeout      time.Duration `mapstructure:"timeout"`       // HTTP read/write 超时
	AllowOrigins []string      `mapstructure:"allow_origins"` // CORS 允许的前端域名
}

// Database PostgreSQL 连接配置。
type Database struct {
	Host          string        `mapstructure:"host"`
	Port          int           `mapstructure:"port"`
	User          string        `mapstructure:"user"`
	Password      string        `mapstructure:"password"`
	DBName        string        `mapstructure:"dbname"`
	SSLMode       string        `mapstructure:"sslmode"`
	TimeZone      string        `mapstructure:"timezone"`     // 仅影响连接时区展示；生效判定一律 Asia/Shanghai
	MaxOpen       int           `mapstructure:"max_open"`     // 连接池上限
	MaxIdle       int           `mapstructure:"max_idle"`     // 空闲连接上限
	MaxLifetime   time.Duration `mapstructure:"max_lifetime"` // 连接最大存活时间
	SlowThreshold time.Duration `mapstructure:"slow_threshold"`
}

// Redis 配置。Redis 为可选组件：enable=false 时降级为进程内实现（见 SysDesign §12）。
type Redis struct {
	Enable   bool   `mapstructure:"enable"`
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// Auth 认证相关配置，骨架阶段仅保留结构，鉴权中间件在第 2 步实现。
type Auth struct {
	TokenSecret  string        `mapstructure:"token_secret"`
	AccessTTL    time.Duration `mapstructure:"access_ttl"`
	RefreshTTL   time.Duration `mapstructure:"refresh_ttl"`
	MaxLoginFail int           `mapstructure:"max_login_fail"`
	LockDuration time.Duration `mapstructure:"lock_duration"`
}

// Log 日志配置。
type Log struct {
	Level  string `mapstructure:"level"`  // debug | info | warn | error
	Format string `mapstructure:"format"` // console | json
	File   string `mapstructure:"file"`   // 预留，当前输出到 stdout
}
