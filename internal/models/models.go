package models

import (
	"time"

	"gorm.io/gorm"
)

type DownloadTask struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	UserID     int64  `gorm:"index" json:"user_id"`
	ChatID     int64  `gorm:"index" json:"chat_id"`
	MessageID  int    `json:"message_id"`
	URL        string `gorm:"size:2048" json:"url"`
	Platform   string `gorm:"size:50;index" json:"platform"`
	Status     string `gorm:"size:20;index" json:"status"` // pending, downloading, processing, uploading, done, failed
	Progress   int    `json:"progress"`                    // 0-100
	Title      string `gorm:"size:512" json:"title"`
	FilePath   string `gorm:"size:1024" json:"file_path,omitempty"`
	FileSize   int64  `json:"file_size"`
	Duration   int    `json:"duration"` // seconds
	Error      string `gorm:"size:1024" json:"error,omitempty"`
	Quality    string `gorm:"size:50" json:"quality"`
	Format     string `gorm:"size:20" json:"format"`
	RetryCount int    `json:"retry_count"`
	Priority   int    `json:"priority"`
}

type MediaInfo struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	URL       string `gorm:"size:2048;uniqueIndex" json:"url"`
	Platform  string `gorm:"size:50;index" json:"platform"`
	Title     string `gorm:"size:512" json:"title"`
	Duration  int    `json:"duration"`
	Thumbnail string `gorm:"size:1024" json:"thumbnail"`
	Uploader  string `gorm:"size:256" json:"uploader"`
	ViewCount int64  `json:"view_count"`
	Data      string `gorm:"type:text" json:"data"` // JSON с доп. метаданными
}

const (
	StatusPending     = "pending"
	StatusDownloading = "downloading"
	StatusProcessing  = "processing"
	StatusUploading   = "uploading"
	StatusDone        = "done"
	StatusFailed      = "failed"
)
