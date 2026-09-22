// Package config 中本文件定义环境变量与配置键的映射关系，以及负责加载的 Load 函数。
//
// 所有环境变量一律使用 MODEL_BSS_ 前缀，避免与机器上的其他程序冲突。
// 映射方式：MODEL_BSS_DB_HOST -> db.host，即去掉前缀后 "." 与 "_" 互换。
// 特例：MODEL_BSS_DB_NAME 映射到 db.dbname（与 YAML 中的键名约定一致）。
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// envPrefix 是所有应用环境变量必须携带的统一前缀。
const envPrefix = "MODEL_BSS_"

// keyReplacer 将环境变量名（MODEL_BSS_DB_HOST）转换为配置键（db.host）。
var keyReplacer = strings.NewReplacer(".", "_")

// Load 按以下顺序加载配置，后加载的覆盖先加载的：
//  1. configs/config.yaml（若存在）
//  2. 前缀为 MODEL_BSS_ 的环境变量
//
// 配置文件路径可通过环境变量 MODEL_BSS_CONFIG 指定（默认 "configs/config.yaml"）。
func Load() (*Config, error) {
	v := viper.New()

	v.SetConfigFile(configPath())
	v.SetEnvPrefix(strings.TrimSuffix(envPrefix, "_"))
	v.SetEnvKeyReplacer(keyReplacer)

	// 显式绑定关键键位，确保环境变量优先级高于配置文件。
	bindings := map[string]string{
		"server.port": "MODEL_BSS_HTTP_PORT",
		"db.host":     "MODEL_BSS_DB_HOST",
		"db.port":     "MODEL_BSS_DB_PORT",
		"db.user":     "MODEL_BSS_DB_USER",
		"db.password": "MODEL_BSS_DB_PASSWORD",
		"db.dbname":   "MODEL_BSS_DB_NAME",
		"db.sslmode":  "MODEL_BSS_DB_SSLMODE",
		"db.timezone": "MODEL_BSS_TZ",
		"docs.enable": "MODEL_BSS_DOCS_ENABLE",
	}

	// 在线接口调试页（Swagger UI）默认开启：内部 BSS 系统，前后端联调期都要用。
	// 生产用 docs.enable=false 或 MODEL_BSS_DOCS_ENABLE=false 显式关掉。
	// 放在这里而不是配置文件里，是因为 configs/config.yaml 被 .gitignore 忽略（含密码），
	// 默认值必须落在代码里才能对所有人都生效。
	v.SetDefault("docs.enable", true)

	// worker 默认开启（同理：configs/config.yaml 被 .gitignore 忽略，前端同事 clone 后
	// 没有这个文件，worker.enable 会落到 Go 零值 false → worker 不启动 → 成本重算不跑）。
	// 生产可用 worker.enable=false 或 MODEL_BSS_WORKER_ENABLE=false 显式关掉。
	// 默认值只覆盖默认开启的两个 job（activate_quote / cost_recalc），其余三个（quote_expire_*）
	// 默认关闭（零值 false），与 configs/config.yaml 的意图一致。
	v.SetDefault("worker.enable", true)
	v.SetDefault("worker.jobs.activate_quote.enable", true)
	v.SetDefault("worker.jobs.activate_quote.interval", time.Minute)
	v.SetDefault("worker.jobs.cost_recalc.enable", true)
	v.SetDefault("worker.jobs.cost_recalc.interval", time.Minute)

	for key, env := range bindings {
		if err := v.BindEnv(key, env); err != nil {
			return nil, fmt.Errorf("bind env %s to %s: %w", env, key, err)
		}
	}
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &cfg, nil
}

// configPath 返回配置文件位置；MODEL_BSS_CONFIG 可覆盖，默认 configs/config.yaml。
func configPath() string {
	if p := strings.TrimSpace(os.Getenv("MODEL_BSS_CONFIG")); p != "" {
		return p
	}
	return "configs/config.yaml"
}
