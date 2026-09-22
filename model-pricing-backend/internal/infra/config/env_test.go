package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoad 验证 YAML 配置加载、默认值、以及 MODEL_BSS_ 前缀环境变量的覆盖语义。
func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	confDir := filepath.Join(tmpDir, "configs")
	require.NoError(t, os.MkdirAll(confDir, 0o755))

	conf := []byte(`
server:
  host: 0.0.0.0
  port: 8080
db:
  host: 127.0.0.1
  port: 5433
  user: app
  password: example
  dbname: model_bss
  sslmode: disable
log:
  level: debug
  format: console
`)
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "config.yaml"), conf, 0o644))

	origWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(origWD))
	})

	t.Setenv("MODEL_BSS_HTTP_PORT", "9090")
	t.Setenv("MODEL_BSS_DB_HOST", "10.0.0.1")
	t.Setenv("MODEL_BSS_DB_PASSWORD", "secret")
	t.Setenv("MODEL_BSS_DB_NAME", "proddb")
	t.Setenv("MODEL_BSS_TZ", "Asia/Shanghai")

	cfg, err := Load()
	require.NoError(t, err)

	require.Equal(t, 9090, cfg.Server.Port, "env var should override yaml")
	require.Equal(t, "10.0.0.1", cfg.Database.Host, "env var should override yaml")
	require.Equal(t, "secret", cfg.Database.Password, "env var should override yaml")
	require.Equal(t, "proddb", cfg.Database.DBName, "MODEL_BSS_DB_NAME should map to db.dbname")
	require.Equal(t, "Asia/Shanghai", cfg.Database.TimeZone, "TZ should map to db.timezone")
}
