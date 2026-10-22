package bot

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"media-downloader-bot/internal/config"
	"media-downloader-bot/internal/extractor"
	"media-downloader-bot/internal/models"
	"media-downloader-bot/internal/queue"
)

// MockExtractorRegistry implements extractor.Registry for testing
type MockExtractorRegistry struct {
	mock.Mock
}

func (m *MockExtractorRegistry) Register(e extractor.Extractor) {
	m.Called(e)
}

func (m *MockExtractorRegistry) Get(name string) (extractor.Extractor, bool) {
	args := m.Called(name)
	return args.Get(0).(extractor.Extractor), args.Bool(1)
}

func (m *MockExtractorRegistry) FindForURL(url string) (extractor.Extractor, error) {
	args := m.Called(url)
	return args.Get(0).(extractor.Extractor), args.Error(1)
}

func (m *MockExtractorRegistry) List() []string {
	args := m.Called()
	return args.Get(0).([]string)
}

func (m *MockExtractorRegistry) Extract(ctx context.Context, url string) (*extractor.MediaInfo, error) {
	args := m.Called(ctx, url)
	return args.Get(0).(*extractor.MediaInfo), args.Error(1)
}

func (m *MockExtractorRegistry) Download(ctx context.Context, url string, qualityPref string, audioOnly bool, progress extractor.ProgressFunc) (*extractor.DownloadResult, error) {
	args := m.Called(ctx, url, qualityPref, audioOnly, progress)
	return args.Get(0).(*extractor.DownloadResult), args.Error(1)
}

func (m *MockExtractorRegistry) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockTaskQueue implements queue.TaskQueue for testing
type MockTaskQueue struct {
	mock.Mock
}

func (m *MockTaskQueue) Enqueue(req queue.DownloadRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

func (m *MockTaskQueue) Dequeue(count int) ([]queue.DownloadRequest, error) {
	args := m.Called(count)
	return args.Get(0).([]queue.DownloadRequest), args.Error(1)
}

func (m *MockTaskQueue) Requeue(req queue.DownloadRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

func (m *MockTaskQueue) Stats() (pending, processing int, err error) {
	args := m.Called()
	return args.Int(0), args.Int(1), args.Error(2)
}

func (m *MockTaskQueue) MarkProcessing(req queue.DownloadRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

func (m *MockTaskQueue) MarkDone(req queue.DownloadRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

func (m *MockTaskQueue) Len() (int64, error) {
	args := m.Called()
	return args.Get(0).(int64), args.Error(1)
}

func setupTestDB() *gorm.DB {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	db.AutoMigrate(&models.DownloadTask{}, &models.MediaInfo{})
	return db
}

func setupTestConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{
			TempDir:       "/tmp/test",
			MaxFileSizeMB: 2048,
		},
		Telegram: config.TelegramConfig{
			BotToken: "test_token",
			AdminIDs: []int64{123456789},
		},
		Downloader: config.DownloaderConfig{
			MaxConcurrent:  1,
			TimeoutMinutes: 30,
		},
	}
}

func TestBot_CancelTask(t *testing.T) {
	db := setupTestDB()
	cfg := setupTestConfig()
	registry := new(MockExtractorRegistry)
	taskQueue := new(MockTaskQueue)

	b := &Bot{cfg: cfg, db: db, registry: registry, taskQueue: taskQueue, active: make(map[uint]activeTask)}

	// Create a task
	task := &models.DownloadTask{
		UserID:   123456789,
		ChatID:   987654321,
		URL:      "https://youtube.com/watch?v=test",
		Status:   models.StatusDownloading,
		Progress: 50,
	}
	db.Create(task)

	// Add to active
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.active[task.ID] = activeTask{userID: task.UserID, chatID: task.ChatID, cancel: cancel}

	// Cancel
	b.cancelTask(task)
	assert.Error(t, ctx.Err())

	// Verify
	db.First(task, task.ID)
	assert.Equal(t, models.StatusFailed, task.Status)
	assert.Equal(t, "Cancelled by user", task.Error)
	// A late progress callback must not resurrect a cancelled task.
	b.updateTaskStatus(task, models.StatusDownloading, 90, "")
	var persisted models.DownloadTask
	require.NoError(t, db.First(&persisted, task.ID).Error)
	assert.Equal(t, models.StatusFailed, persisted.Status)
	assert.Equal(t, "Cancelled by user", persisted.Error)
}

type blockingRegistry struct{ started chan struct{} }

func (r *blockingRegistry) Download(ctx context.Context, raw, quality string, audio bool, progress extractor.ProgressFunc) (*extractor.DownloadResult, error) {
	close(r.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestShutdownInterruptsDownloadForRequeue(t *testing.T) {
	db := setupTestDB()
	r := &blockingRegistry{started: make(chan struct{})}
	b := &Bot{cfg: setupTestConfig(), db: db, registry: r, active: make(map[uint]activeTask)}
	task := &models.DownloadTask{UserID: 1, ChatID: 1, URL: "https://youtu.be/test", Status: models.StatusPending}
	require.NoError(t, db.Create(task).Error)
	done := make(chan error, 1)
	go func() { done <- b.ProcessTask(task) }()
	select {
	case <-r.started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	b.CancelActive()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not cancel download")
	}
	var persisted models.DownloadTask
	require.NoError(t, db.First(&persisted, task.ID).Error)
	require.NotEqual(t, models.StatusFailed, persisted.Status)
}

func TestCancelledPendingTaskDoesNotStartDownload(t *testing.T) {
	db := setupTestDB()
	r := &blockingRegistry{started: make(chan struct{})}
	b := &Bot{cfg: setupTestConfig(), db: db, registry: r, active: make(map[uint]activeTask)}
	task := &models.DownloadTask{UserID: 1, ChatID: 1, URL: "https://youtu.be/test", Status: models.StatusPending}
	require.NoError(t, db.Create(task).Error)
	b.cancelTask(task)
	require.NoError(t, b.ProcessTask(task))
	select {
	case <-r.started:
		t.Fatal("cancelled task started downloading")
	default:
	}
}

func TestBot_FormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1536 * 1024 * 1024, "1.5 GB"},
	}

	for _, tc := range tests {
		result := formatBytes(tc.input)
		assert.Equal(t, tc.expected, result, "input: %d", tc.input)
	}
}

func TestBot_FormatDuration(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "N/A"},
		{30, "0:30"},
		{90, "1:30"},
		{3600, "1:00:00"},
		{3661, "1:01:01"},
		{7200 + 300 + 5, "2:05:05"},
	}

	for _, tc := range tests {
		result := formatDuration(tc.input)
		assert.Equal(t, tc.expected, result, "input: %d", tc.input)
	}
}

func TestBot_ParseQuality(t *testing.T) {
	// Test quality parsing logic
	tests := []struct {
		input     string
		quality   string
		audioOnly bool
	}{
		{"https://youtube.com/watch?v=123 720p", "720p", false},
		{"https://youtube.com/watch?v=123 audio", "audio", true},
		{"https://youtube.com/watch?v=123 mp3", "audio", true},
		{"https://youtube.com/watch?v=123 best", "best", false},
		{"https://youtube.com/watch?v=123", "best", false},
	}

	for _, tc := range tests {
		parts := splitURLAndQuality(tc.input)
		assert.Equal(t, tc.quality, parts.quality)
		assert.Equal(t, tc.audioOnly, parts.audioOnly)
	}
}

// Helper for testing (not in production code)
type qualityParts struct {
	url       string
	quality   string
	audioOnly bool
}

func splitURLAndQuality(input string) qualityParts {
	parts := splitFields(input)
	url := parts[0]
	quality := "best"
	audioOnly := false
	if len(parts) > 1 {
		q := parts[1]
		if q == "audio" || q == "mp3" {
			audioOnly = true
			quality = "audio"
		} else {
			quality = q
		}
	}
	return qualityParts{url, quality, audioOnly}
}

func splitFields(s string) []string {
	// Simple split by whitespace
	var result []string
	current := ""
	inQuote := false
	for _, r := range s {
		if r == '"' || r == '\'' {
			inQuote = !inQuote
			continue
		}
		if (r == ' ' || r == '\t' || r == '\n') && !inQuote {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(r)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
