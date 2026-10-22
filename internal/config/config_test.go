package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	// Create temporary config file
	configContent := `
app:
  env: "test"
  log_level: "debug"
  temp_dir: "/tmp/test"
  max_file_size_mb: 1024

telegram:
  bot_token: "test_token"
  admin_ids: [123456789]
  webhook:
    enabled: false
    host: ""
    port: 8080
    path: "/webhook"

redis:
  addr: "localhost:6379"
  password: ""
  db: 0
  pool_size: 10

database:
  driver: "sqlite"
  dsn: "file:test.db?cache=shared&mode=rwc"

downloader:
  max_concurrent: 2
  chunk_size_mb: 8
  timeout_minutes: 30
  user_agent: "TestAgent"

platforms:
  youtube:
    enabled: true
    quality_preference: "720p"
    download_subtitles: true
    subtitle_langs: ["ru", "en"]
  tiktok:
    enabled: true
    watermark: false
  instagram:
    enabled: true
  twitter:
    enabled: true
  soundcloud:
    enabled: false
  vk:
    enabled: false
  generic:
    enabled: true
`
	tmpFile, err := os.CreateTemp("", "config_*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(configContent)
	require.NoError(t, err)
	tmpFile.Close()

	cfg, err := Load(tmpFile.Name())
	require.NoError(t, err)

	assert.Equal(t, "test", cfg.App.Env)
	assert.Equal(t, "debug", cfg.App.LogLevel)
	assert.Equal(t, "/tmp/test", cfg.App.TempDir)
	assert.Equal(t, 50, cfg.App.MaxFileSizeMB)

	assert.Equal(t, "test_token", cfg.Telegram.BotToken)
	assert.Equal(t, []int64{123456789}, cfg.Telegram.AdminIDs)

	assert.Equal(t, 2, cfg.Downloader.MaxConcurrent)
	assert.Equal(t, "720p", cfg.Platforms.YouTube.QualityPreference)
	assert.True(t, cfg.Platforms.YouTube.DownloadSubtitles)
	assert.False(t, cfg.Platforms.SoundCloud.Enabled)
}

func TestEnvOverride(t *testing.T) {
	configContent := `
app:
  env: "production"
  log_level: "info"
  temp_dir: "/tmp/default"
  max_file_size_mb: 500

telegram:
  bot_token: "default_token"
  admin_ids: []
  webhook:
    enabled: false

redis:
  addr: "localhost:6379"

database:
  driver: "sqlite"
  dsn: "file:default.db"

downloader:
  max_concurrent: 1

platforms:
  youtube:
    enabled: false
`
	tmpFile, err := os.CreateTemp("", "config_*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(configContent)
	require.NoError(t, err)
	tmpFile.Close()

	// Set env vars
	os.Setenv("APP_ENV", "development")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("TEMP_DIR", "/tmp/env")
	os.Setenv("MAX_FILE_SIZE_MB", "2048")
	os.Setenv("BOT_TOKEN", "env_token")
	os.Setenv("ADMIN_IDS", "111,222")
	os.Setenv("DOWNLOADER_MAX_CONCURRENT", "5")
	os.Setenv("YOUTUBE_ENABLED", "true")
	defer func() {
		os.Unsetenv("APP_ENV")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("TEMP_DIR")
		os.Unsetenv("MAX_FILE_SIZE_MB")
		os.Unsetenv("BOT_TOKEN")
		os.Unsetenv("ADMIN_IDS")
		os.Unsetenv("DOWNLOADER_MAX_CONCURRENT")
		os.Unsetenv("YOUTUBE_ENABLED")
	}()

	cfg, err := Load(tmpFile.Name())
	require.NoError(t, err)

	assert.Equal(t, "development", cfg.App.Env)
	assert.Equal(t, "debug", cfg.App.LogLevel)
	assert.Equal(t, "/tmp/env", cfg.App.TempDir)
	assert.Equal(t, 50, cfg.App.MaxFileSizeMB)
	assert.Equal(t, "env_token", cfg.Telegram.BotToken)
	assert.Equal(t, []int64{111, 222}, cfg.Telegram.AdminIDs)
	assert.Equal(t, 5, cfg.Downloader.MaxConcurrent)
	assert.True(t, cfg.Platforms.YouTube.Enabled)
}
