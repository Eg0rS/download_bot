package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"media-downloader-bot/internal/bot"
	"media-downloader-bot/internal/config"
	"media-downloader-bot/internal/extractor"
	"media-downloader-bot/internal/models"
	"media-downloader-bot/internal/queue"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	// Load .env if exists
	_ = godotenv.Load()

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		logrus.Fatalf("Failed to load config: %v", err)
	}

	logrus.Info("Starting Media Downloader Bot...")

	// Database
	db, err := gorm.Open(sqlite.Open(cfg.Database.DSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		logrus.Fatalf("Failed to connect to database: %v", err)
	}

	// Auto-migrate
	if err := db.AutoMigrate(&models.DownloadTask{}, &models.MediaInfo{}); err != nil {
		logrus.Fatalf("Failed to migrate database: %v", err)
	}

	// Redis
	redisClient := queue.NewRedisClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logrus.Fatalf("Failed to connect to Redis: %v", err)
	}
	logrus.Info("Connected to Redis")

	// Extractors registry
	registry := extractor.NewRegistry()

	// Register YouTube
	if cfg.Platforms.YouTube.Enabled {
		ytCfg := extractor.YouTubeConfig{
			QualityPreference: cfg.Platforms.YouTube.QualityPreference,
			DownloadSubtitles: cfg.Platforms.YouTube.DownloadSubtitles,
			SubtitleLangs:     cfg.Platforms.YouTube.SubtitleLangs,
		}
		registry.Register(extractor.NewYouTubeExtractor(ytCfg))
	}

	// Register TikTok (requires headless Chrome)
	if cfg.Platforms.TikTok.Enabled {
		ttCfg := extractor.TikTokConfig{Watermark: cfg.Platforms.TikTok.Watermark}
		if ext, err := extractor.NewTikTokExtractor(ttCfg); err != nil {
			logrus.Warnf("Failed to init TikTok extractor: %v", err)
		} else {
			registry.Register(ext)
		}
	}

	// Register Instagram
	if cfg.Platforms.Instagram.Enabled {
		igCfg := extractor.InstagramConfig{Enabled: true}
		if ext, err := extractor.NewInstagramExtractor(igCfg); err != nil {
			logrus.Warnf("Failed to init Instagram extractor: %v", err)
		} else {
			registry.Register(ext)
		}
	}

	// Register Twitter/X
	if cfg.Platforms.Twitter.Enabled {
		twCfg := extractor.TwitterConfig{Enabled: true}
		if ext, err := extractor.NewTwitterExtractor(twCfg); err != nil {
			logrus.Warnf("Failed to init Twitter extractor: %v", err)
		} else {
			registry.Register(ext)
		}
	}

	// Register Generic (fallback)
	if cfg.Platforms.Generic.Enabled {
		registry.Register(extractor.NewGenericExtractor(extractor.GenericConfig{Enabled: true}))
	}

	logrus.Infof("Registered extractors: %v", registry.List())

	// Task queue
	taskQueue := queue.NewTaskQueue(redisClient)

	// Bot
	b, err := bot.NewBot(cfg, db, registry, taskQueue)
	if err != nil {
		logrus.Fatalf("Failed to create bot: %v", err)
	}

	// Worker pool for processing queue
	workerPool := queue.NewWorkerPool(taskQueue, cfg.Downloader.MaxConcurrent, func(req queue.DownloadRequest) error {
		// Create task in DB
		task := &models.DownloadTask{
			UserID:     req.UserID,
			ChatID:     req.ChatID,
			MessageID:  req.MessageID,
			URL:        req.URL,
			Quality:    req.Quality,
			Format:     map[bool]string{true: "audio", false: "video"}[req.AudioOnly],
			Status:     models.StatusPending,
			Priority:   req.Priority,
		}
		if err := db.Create(task).Error; err != nil {
			return err
		}

		// Process (this runs in worker goroutine)
		b.ProcessTask(task)
		return nil
	})
	workerPool.Start()

	// Start bot
	go b.Start()

	// Wait for shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logrus.Infof("Received signal %v, shutting down...", sig)
	
	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	
	done := make(chan struct{})
	go func() {
		workerPool.Stop()
		b.Stop()
		registry.Close()
		redisClient.Close()
		
		sqlDB, _ := db.DB()
		sqlDB.Close()
		close(done)
	}()
	
	select {
	case <-done:
		logrus.Info("Graceful shutdown completed")
	case <-shutdownCtx.Done():
		logrus.Warn("Shutdown timeout, forcing exit")
	}
	
	fmt.Println("Bot stopped gracefully")
}