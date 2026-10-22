package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB() *gorm.DB {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	db.AutoMigrate(&DownloadTask{}, &MediaInfo{})
	return db
}

func TestDownloadTask_CRUD(t *testing.T) {
	db := setupTestDB()

	task := &DownloadTask{
		UserID:    123456789,
		ChatID:    987654321,
		MessageID: 1,
		URL:       "https://youtube.com/watch?v=test",
		Platform:  "youtube",
		Status:    StatusPending,
		Quality:   "720p",
		Format:    "video",
		Priority:  10,
	}

	// Create
	assert.NoError(t, db.Create(task).Error)
	assert.Greater(t, task.ID, uint(0))

	// Read
	var found DownloadTask
	assert.NoError(t, db.First(&found, task.ID).Error)
	assert.Equal(t, task.URL, found.URL)
	assert.Equal(t, task.UserID, found.UserID)
	assert.Equal(t, StatusPending, found.Status)

	// Update
	found.Status = StatusDownloading
	found.Progress = 50
	assert.NoError(t, db.Save(&found).Error)

	var updated DownloadTask
	db.First(&updated, task.ID)
	assert.Equal(t, StatusDownloading, updated.Status)
	assert.Equal(t, 50, updated.Progress)

	// Delete (soft delete)
	assert.NoError(t, db.Delete(&updated).Error)

	var deleted DownloadTask
	assert.Error(t, db.First(&deleted, task.ID).Error) // Should not find (soft deleted)
}

func TestMediaInfo_CRUD(t *testing.T) {
	db := setupTestDB()

	info := &MediaInfo{
		URL:      "https://youtube.com/watch?v=test",
		Platform: "youtube",
		Title:    "Test Video",
		Duration: 300,
		Uploader: "Test Channel",
	}

	assert.NoError(t, db.Create(info).Error)
	assert.Greater(t, info.ID, uint(0))

	var found MediaInfo
	assert.NoError(t, db.Where("url = ?", info.URL).First(&found).Error)
	assert.Equal(t, info.Title, found.Title)
	assert.Equal(t, info.Platform, found.Platform)
}

func TestStatusConstants(t *testing.T) {
	assert.Equal(t, "pending", StatusPending)
	assert.Equal(t, "downloading", StatusDownloading)
	assert.Equal(t, "processing", StatusProcessing)
	assert.Equal(t, "uploading", StatusUploading)
	assert.Equal(t, "done", StatusDone)
	assert.Equal(t, "failed", StatusFailed)
}
