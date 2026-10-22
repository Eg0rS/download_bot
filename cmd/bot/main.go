package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
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
	path := flag.String("config", "config.yaml", "Path to configuration")
	flag.Parse()
	if err := run(*path); err != nil {
		logrus.Fatal(err)
	}
}
func run(path string) error {
	_ = godotenv.Load()
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	// The same limits and location are used by subprocesses and HTTP downloads.
	_ = os.Setenv("TEMP_DIR", cfg.App.TempDir)
	_ = os.Setenv("MAX_FILE_SIZE_MB", fmt.Sprint(cfg.App.MaxFileSizeMB))
	if err := os.MkdirAll(cfg.App.TempDir, 0700); err != nil {
		return err
	}
	db, err := gorm.Open(sqlite.Open(cfg.Database.DSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1) // SQLite has a single writer.
	if err = db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		return err
	}
	if err = db.Exec("PRAGMA busy_timeout=5000").Error; err != nil {
		return err
	}
	if err = db.AutoMigrate(&models.DownloadTask{}, &models.MediaInfo{}); err != nil {
		return err
	}
	client := queue.NewRedisClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = client.Ping(ctx).Err()
	cancel()
	if err != nil {
		return err
	}
	registry := extractor.NewRegistry()
	defer registry.Close()
	if cfg.Platforms.YouTube.Enabled {
		registry.Register(extractor.NewYouTubeExtractor(extractor.YouTubeConfig{QualityPreference: cfg.Platforms.YouTube.QualityPreference, DownloadSubtitles: cfg.Platforms.YouTube.DownloadSubtitles, SubtitleLangs: cfg.Platforms.YouTube.SubtitleLangs}))
	}
	if cfg.Platforms.TikTok.Enabled {
		e, err := extractor.NewTikTokExtractor(extractor.TikTokConfig{Watermark: cfg.Platforms.TikTok.Watermark})
		if err != nil {
			return err
		}
		registry.Register(e)
	}
	if cfg.Platforms.Instagram.Enabled {
		e, err := extractor.NewInstagramExtractor(extractor.InstagramConfig{Enabled: true})
		if err != nil {
			return err
		}
		registry.Register(e)
	}
	if cfg.Platforms.Twitter.Enabled {
		e, err := extractor.NewTwitterExtractor(extractor.TwitterConfig{Enabled: true})
		if err != nil {
			return err
		}
		registry.Register(e)
	}
	if cfg.Platforms.SoundCloud.Enabled {
		e, err := extractor.NewSoundCloudExtractor(extractor.SoundCloudConfig{Enabled: true})
		if err != nil {
			return err
		}
		registry.Register(e)
	}
	if cfg.Platforms.Generic.Enabled {
		registry.Register(extractor.NewGenericExtractor(extractor.GenericConfig{Enabled: true}))
	}
	q := queue.NewTaskQueue(client)
	if err = q.Recover(); err != nil {
		return err
	}
	b, err := bot.NewBot(cfg, db, registry, q)
	if err != nil {
		return err
	}
	workers := queue.NewWorkerPool(q, cfg.Downloader.MaxConcurrent, func(req queue.DownloadRequest) error {
		task := &models.DownloadTask{}
		if req.TaskID != 0 {
			if err := db.First(task, req.TaskID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			if task.Status == models.StatusDone || task.Status == models.StatusFailed {
				return nil
			}
		} else {
			task = &models.DownloadTask{UserID: req.UserID, ChatID: req.ChatID, MessageID: req.MessageID, URL: req.URL, Quality: req.Quality, Format: map[bool]string{true: "audio", false: "video"}[req.AudioOnly], Status: models.StatusPending}
			if err := db.Create(task).Error; err != nil {
				return err
			}
		}
		if ext, err := registry.FindForURL(task.URL); err == nil {
			task.Platform = ext.Name()
		}
		return b.ProcessTask(task)
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if client.Ping(ctx).Err() != nil || sqlDB.PingContext(ctx) != nil {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	workers.Start()
	go b.Start()
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	select {
	case <-signalCtx.Done():
	case err = <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			logrus.Error(err)
		}
	}
	b.CancelActive()
	// Workers finish before databases, extractors and connections are closed.
	workers.Stop()
	b.Stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	return err
}
