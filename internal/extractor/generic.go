package extractor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gen2brain/dl"
	"github.com/sirupsen/logrus"
)

type GenericExtractor struct {
	client *http.Client
	config GenericConfig
}

type GenericConfig struct {
	Enabled bool
}

func NewGenericExtractor(config GenericConfig) *GenericExtractor {
	return &GenericExtractor{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		config: config,
	}
}

func (e *GenericExtractor) Name() string {
	return "generic"
}

func (e *GenericExtractor) CanHandle(url string) bool {
	return e.config.Enabled && (strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://"))
}

func (e *GenericExtractor) Extract(ctx context.Context, url string) (*MediaInfo, error) {
	// Try to get headers to determine content type and size
	req, _ := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("generic: HEAD request failed: %w", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	contentLength := resp.Header.Get("Content-Length")
	var size int64
	fmt.Sscanf(contentLength, "%d", &size)

	isVideo := strings.HasPrefix(contentType, "video/")
	isAudio := strings.HasPrefix(contentType, "audio/")
	ext := "bin"
	if isVideo {
		ext = "mp4"
	} else if isAudio {
		ext = "mp3"
	} else if strings.HasPrefix(contentType, "image/") {
		ext = "jpg"
	}

	// Extract filename from URL or Content-Disposition
	title := url
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		// parse filename from Content-Disposition
	}

	info := &MediaInfo{
		ID:       fmt.Sprintf("generic-%d", time.Now().Unix()),
		Title:    title,
		Duration: 0,
		Platform: "generic",
		URL:      url,
		Formats: []MediaFormat{{
			FormatID:  "direct",
			Ext:       ext,
			Quality:   "original",
			URL:       url,
			FileSize:  size,
			IsVideo:   isVideo,
			IsAudio:   isAudio,
			Protocol:  "https",
		}},
	}

	logrus.Debugf("Generic: extracted %s (%s, %d bytes)", url, contentType, size)
	return info, nil
}

func (e *GenericExtractor) GetBestFormat(info *MediaInfo, qualityPref string, audioOnly bool) (MediaFormat, error) {
	if len(info.Formats) == 0 {
		return MediaFormat{}, errors.New("no formats available")
	}
	return info.Formats[0], nil
}

func (e *GenericExtractor) Download(ctx context.Context, url string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	downloader := NewHTTPDownloader("/tmp/media-downloader")
	dest := downloader.GetTempFile(format.Ext)

	err := downloader.Download(ctx, url, dest, progress)
	if err != nil {
		return nil, err
	}

	info, _ := os.Stat(dest)
	return &DownloadResult{
		FilePath:     dest,
		Format:       format,
		FileSize:     info.Size(),
		DownloadedAt: time.Now(),
	}, nil
}