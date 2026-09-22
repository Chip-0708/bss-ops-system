// Package db 负责数据库连接生命周期：PG15（GORM/pgx）连接池的初始化、健康检查与关闭。
// 骨架阶段仅建立连接与暴露 Ping；具体的数据库合约（代码内的 SQL、GORM 模型、
// migrations 执行）由后续的 service / repo 层承担。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"model_bss/internal/infra/config"
)

// Client 封装 GORM 数据库句柄，提供连接池管理与健康检查。
type Client struct {
	*gorm.DB
}

// New 根据配置建立数据库连接池并立即 Ping 验证连通性。
// GORM 的 DSN 使用 pgx 作为底层驱动（gorm.io/driver/postgres 为 pgx 的封装）。
func New(cfg config.Database) (*Client, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, cfg.SSLMode, cfg.TimeZone,
	)

	gormCfg := &gorm.Config{
		SkipDefaultTransaction: true,
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	}

	db, err := gorm.Open(postgres.Open(dsn), gormCfg)
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}

	configurePool(sqlDB, cfg)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return &Client{DB: db}, nil
}

// configurePool 设置连接池参数；0/负值时采用 sql.DB 的默认行为。
func configurePool(sqlDB *sql.DB, cfg config.Database) {
	if cfg.MaxOpen > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpen)
	}
	if cfg.MaxIdle > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdle)
	}
	if cfg.MaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.MaxLifetime)
	}
}

// Ping 实际检测数据库连接可用性，供 /healthz 等场景使用。
func (c *Client) Ping(ctx context.Context) error {
	sqlDB, err := c.DB.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	return sqlDB.PingContext(ctx)
}

// Close 关闭底层连接池。与其他应用组件一样，应当在进程退出时调用。
func (c *Client) Close() error {
	sqlDB, err := c.DB.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	return sqlDB.Close()
}

// ctxKeyDB 用于在 context 中传递事务级 *gorm.DB。
type ctxKeyDB struct{}

// WithDB 将 *gorm.DB（通常是正在进行的事务）注入 context。
func WithDB(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, ctxKeyDB{}, tx)
}

// FromContext 尝试从 context 中提取注入的 *gorm.DB（例如由幂等中间件注入的事务）。
// 若不存在则返回 nil。
func FromContext(ctx context.Context) *gorm.DB {
	if v, ok := ctx.Value(ctxKeyDB{}).(*gorm.DB); ok {
		return v
	}
	return nil
}
