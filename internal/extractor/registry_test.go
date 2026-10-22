package extractor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockExtractor struct {
	mock.Mock
}

func (m *MockExtractor) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockExtractor) CanHandle(url string) bool {
	args := m.Called(url)
	return args.Bool(0)
}

func (m *MockExtractor) Extract(ctx context.Context, url string) (*MediaInfo, error) {
	args := m.Called(ctx, url)
	return args.Get(0).(*MediaInfo), args.Error(1)
}

func (m *MockExtractor) Download(ctx context.Context, url string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	args := m.Called(ctx, url, format, progress)
	return args.Get(0).(*DownloadResult), args.Error(1)
}

func (m *MockExtractor) GetBestFormat(info *MediaInfo, qualityPref string, audioOnly bool) (MediaFormat, error) {
	args := m.Called(info, qualityPref, audioOnly)
	return args.Get(0).(MediaFormat), args.Error(1)
}

func TestRegistry_RegisterAndFind(t *testing.T) {
	registry := NewRegistry()

	mockExt := new(MockExtractor)
	mockExt.On("Name").Return("test")
	mockExt.On("CanHandle", "https://test.com/video").Return(true)
	mockExt.On("CanHandle", "https://unknown.com/video").Return(false)

	registry.Register(mockExt)

	// Test List
	list := registry.List()
	assert.Contains(t, list, "test")

	// Test FindForURL
	ext, err := registry.FindForURL("https://test.com/video")
	assert.NoError(t, err)
	assert.Equal(t, mockExt, ext)

	// Test not found
	_, err = registry.FindForURL("https://unknown.com/video")
	assert.Error(t, err)
}

func TestRegistry_MultipleExtractors(t *testing.T) {
	registry := NewRegistry()

	// YouTube-like extractor
	ytMock := new(MockExtractor)
	ytMock.On("Name").Return("youtube")
	ytMock.On("CanHandle", "https://youtube.com/watch?v=123").Return(true)
	ytMock.On("CanHandle", "https://tiktok.com/video").Return(false)

	// TikTok-like extractor
	ttMock := new(MockExtractor)
	ttMock.On("Name").Return("tiktok")
	ttMock.On("CanHandle", "https://youtube.com/watch?v=123").Return(false)
	ttMock.On("CanHandle", "https://tiktok.com/video").Return(true)

	registry.Register(ytMock)
	registry.Register(ttMock)

	// YouTube URL should get YouTube extractor
	ext, err := registry.FindForURL("https://youtube.com/watch?v=123")
	assert.NoError(t, err)
	assert.Equal(t, "youtube", ext.Name())

	// TikTok URL should get TikTok extractor
	ext, err = registry.FindForURL("https://tiktok.com/video")
	assert.NoError(t, err)
	assert.Equal(t, "tiktok", ext.Name())
}

func TestYouTubeQualityRank(t *testing.T) {
	ext := NewYouTubeExtractor(YouTubeConfig{})

	tests := []struct {
		quality string
		rank    int
	}{
		{"4320p", 80},
		{"8k", 80},
		{"2160p", 70},
		{"4k", 70},
		{"1440p", 60},
		{"2k", 60},
		{"1080p", 50},
		{"720p", 40},
		{"480p", 30},
		{"360p", 20},
		{"240p", 10},
		{"144p", 5},
		{"audio_only", 1},
		{"unknown", 0},
	}

	for _, tc := range tests {
		rank := ext.qualityRank(tc.quality)
		assert.Equal(t, tc.rank, rank, "quality: %s", tc.quality)
	}
}

func TestParseHeight(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"1080p", 1080},
		{"720p", 720},
		{"480", 480},
		{"1080", 1080},
		{"audio", 0},
	}

	for _, tc := range tests {
		result := parseHeight(tc.input)
		assert.Equal(t, tc.expected, result, "input: %s", tc.input)
	}
}
