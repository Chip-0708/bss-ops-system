// Package logger 提供进程级 zap 结构化日志与初始化辅助。
package logger

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"model_bss/internal/infra/config"
)

// New 按配置构造 *zap.Logger。format=console 时使用人类可读的 DevelopmentEncoder，
// format=json 时使用 ProductionEncoder；level 非法时返回错误。
func New(cfg config.Log) (*zap.Logger, error) {
	level, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("parse log level %q: %w", cfg.Level, err)
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encCfg.EncodeLevel = zapcore.LowercaseLevelEncoder
	encCfg.EncodeCaller = zapcore.ShortCallerEncoder

	var encoder zapcore.Encoder
	switch cfg.Format {
	case "json":
		encoder = zapcore.NewJSONEncoder(encCfg)
	case "console":
		encoder = zapcore.NewConsoleEncoder(encCfg)
	default:
		return nil, fmt.Errorf("unknown log format %q", cfg.Format)
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stderr), level)
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)), nil
}

// Nop 返回一个不输出的 Logger，供测试或降级场景使用。
func Nop() *zap.Logger {
	return zap.NewNop()
}
