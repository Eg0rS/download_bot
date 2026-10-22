package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

type Config struct {
	App      AppConfig      `yaml:"app"`
	Telegram TelegramConfig `yaml:"telegram"`
	Redis    RedisConfig    `yaml:"redis"`
	Database DatabaseConfig `yaml:"database"`
	Downloader DownloaderConfig `yaml:"downloader"`
	Platforms PlatformsConfig `yaml:"platforms"`
}

type AppConfig struct {
	Env         string `yaml:"env"`
	LogLevel    string `yaml:"log_level"`
	TempDir     string `yaml:"temp_dir"`
	MaxFileSizeMB int  `yaml:"max_file_size_mb"`
}

type TelegramConfig struct {
	BotToken string   `yaml:"bot_token"`
	AdminIDs []int64  `yaml:"admin_ids"`
	Webhook  WebhookConfig `yaml:"webhook"`
}

type WebhookConfig struct {
	Enabled bool   `yaml:"enabled"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	Path    string `yaml:"path"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	PoolSize int    `yaml:"pool_size"`
}

type DatabaseConfig struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

type DownloaderConfig struct {
	MaxConcurrent int    `yaml:"max_concurrent"`
	ChunkSizeMB   int    `yaml:"chunk_size_mb"`
	TimeoutMinutes int   `yaml:"timeout_minutes"`
	UserAgent     string `yaml:"user_agent"`
}

type PlatformsConfig struct {
	YouTube  YouTubeConfig  `yaml:"youtube"`
	TikTok   TikTokConfig   `yaml:"tiktok"`
	Instagram InstagramConfig `yaml:"instagram"`
	Twitter  TwitterConfig  `yaml:"twitter"`
	SoundCloud SoundCloudConfig `yaml:"soundcloud"`
	VK       VKConfig       `yaml:"vk"`
	Generic  GenericConfig  `yaml:"generic"`
}

type YouTubeConfig struct {
	Enabled           bool     `yaml:"enabled"`
	QualityPreference string   `yaml:"quality_preference"`
	DownloadSubtitles bool     `yaml:"download_subtitles"`
	SubtitleLangs     []string `yaml:"subtitle_langs"`
}

type TikTokConfig struct {
	Enabled    bool `yaml:"enabled"`
	Watermark  bool `yaml:"watermark"`
}

type InstagramConfig struct {
	Enabled bool `yaml:"enabled"`
}

type TwitterConfig struct {
	Enabled bool `yaml:"enabled"`
}

type SoundCloudConfig struct {
	Enabled bool `yaml:"enabled"`
}

type VKConfig struct {
	Enabled bool `yaml:"enabled"`
}

type GenericConfig struct {
	Enabled bool `yaml:"enabled"`
}

func Load(path string) (*Config, error) {
	_ = godotenv.Load() // игнорируем ошибку, если .env нет

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Override from env
	cfg.App.Env = getEnv("APP_ENV", cfg.App.Env)
	cfg.App.LogLevel = getEnv("LOG_LEVEL", cfg.App.LogLevel)
	cfg.App.TempDir = getEnv("TEMP_DIR", cfg.App.TempDir)
	cfg.App.MaxFileSizeMB = getEnvInt("MAX_FILE_SIZE_MB", cfg.App.MaxFileSizeMB)

	cfg.Telegram.BotToken = getEnv("BOT_TOKEN", cfg.Telegram.BotToken)
	if ids := getEnv("ADMIN_IDS", ""); ids != "" {
		for _, id := range strings.Split(ids, ",") {
			if v, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64); err == nil {
				cfg.Telegram.AdminIDs = append(cfg.Telegram.AdminIDs, v)
			}
		}
	}
	cfg.Telegram.Webhook.Enabled = getEnvBool("WEBHOOK_ENABLED", cfg.Telegram.Webhook.Enabled)
	cfg.Telegram.Webhook.Host = getEnv("WEBHOOK_HOST", cfg.Telegram.Webhook.Host)
	cfg.Telegram.Webhook.Port = getEnvInt("WEBHOOK_PORT", cfg.Telegram.Webhook.Port)
	cfg.Telegram.Webhook.Path = getEnv("WEBHOOK_PATH", cfg.Telegram.Webhook.Path)

	cfg.Redis.Addr = getEnv("REDIS_ADDR", cfg.Redis.Addr)
	cfg.Redis.Password = getEnv("REDIS_PASSWORD", cfg.Redis.Password)
	cfg.Redis.DB = getEnvInt("REDIS_DB", cfg.Redis.DB)

	cfg.Database.Driver = getEnv("DB_DRIVER", cfg.Database.Driver)
	cfg.Database.DSN = getEnv("DB_DSN", cfg.Database.DSN)

	cfg.Downloader.MaxConcurrent = getEnvInt("DOWNLOADER_MAX_CONCURRENT", cfg.Downloader.MaxConcurrent)
	cfg.Downloader.ChunkSizeMB = getEnvInt("DOWNLOADER_CHUNK_SIZE_MB", cfg.Downloader.ChunkSizeMB)
	cfg.Downloader.TimeoutMinutes = getEnvInt("DOWNLOADER_TIMEOUT_MINUTES", cfg.Downloader.TimeoutMinutes)

	cfg.Platforms.YouTube.Enabled = getEnvBool("YOUTUBE_ENABLED", cfg.Platforms.YouTube.Enabled)
	cfg.Platforms.TikTok.Enabled = getEnvBool("TIKTOK_ENABLED", cfg.Platforms.TikTok.Enabled)
	cfg.Platforms.Instagram.Enabled = getEnvBool("INSTAGRAM_ENABLED", cfg.Platforms.Instagram.Enabled)
	cfg.Platforms.Twitter.Enabled = getEnvBool("TWITTER_ENABLED", cfg.Platforms.Twitter.Enabled)
	cfg.Platforms.SoundCloud.Enabled = getEnvBool("SOUNDCLOUD_ENABLED", cfg.Platforms.SoundCloud.Enabled)
	cfg.Platforms.VK.Enabled = getEnvBool("VK_ENABLED", cfg.Platforms.VK.Enabled)
	cfg.Platforms.Generic.Enabled = getEnvBool("GENERIC_ENABLED", cfg.Platforms.Generic.Enabled)

	// Setup logger
	level, _ := logrus.ParseLevel(cfg.App.LogLevel)
	logrus.SetLevel(level)
	if cfg.App.Env == "production" {
		logrus.SetFormatter(&logrus.JSONFormatter{})
	} else {
		logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	}

	return &cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		return v == "true" || v == "1"
	}
	return fallback
}