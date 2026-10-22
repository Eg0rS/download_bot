package extractor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rod/rod"
	"github.com/rod/rod/lib/launcher"
	"github.com/sirupsen/logrus"
)

type TikTokExtractor struct {
	browser *rod.Browser
	config  TikTokConfig
}

type TikTokConfig struct {
	Watermark bool
}

func NewTikTokExtractor(config TikTokConfig) (*TikTokExtractor, error) {
	// Launch headless Chrome
	url := launcher.New().Bin("/usr/bin/chromium").NoSandbox(true).MustLaunch()
	browser := rod.New().ControlURL(url).MustConnect()

	return &TikTokExtractor{
		browser: browser,
		config:  config,
	}, nil
}

func (e *TikTokExtractor) Name() string {
	return "tiktok"
}

func (e *TikTokExtractor) CanHandle(url string) bool {
	return strings.Contains(url, "tiktok.com") || strings.Contains(url, "vm.tiktok.com")
}

func (e *TikTokExtractor) Extract(ctx context.Context, url string) (*MediaInfo, error) {
	page := e.browser.MustPage(url).MustWaitLoad()
	defer page.MustClose()

	// Wait for video element
	videoEl, err := page.ElementR("video", "source")
	if err != nil {
		// Try alternative selectors
		videoEl, err = page.Element("video")
		if err != nil {
			return nil, fmt.Errorf("tiktok: video element not found: %w", err)
		}
	}

	// Get video URL from src
	videoURL, err := videoEl.Property("src")
	if err != nil || videoURL == nil {
		return nil, fmt.Errorf("tiktok: video src not found")
	}

	// Extract metadata from page
	title := page.MustElement(`h1[data-e2e="video-desc"]`).MustText()
	// fallback
	if title == "" {
		title = page.MustElement("h1").MustText()
	}

	// Try to get author
	authorEl, _ := page.Element(`h2[data-e2e="video-author-uniqueid"]`)
	author := ""
	if authorEl != nil {
		author = authorEl.MustText()
	}

	// Extract video ID from URL
	videoID := extractTikTokID(url)

	info := &MediaInfo{
		ID:          videoID,
		Title:       title,
		Duration:    0, // unknown without downloading
		Thumbnail:   "",
		Uploader:    author,
		Platform:    "tiktok",
		URL:         url,
		Formats: []MediaFormat{{
			FormatID: "default",
			Ext:      "mp4",
			Quality:  "original",
			URL:      videoURL.String(),
			IsVideo:  true,
			Protocol: "https",
		}},
	}

	logrus.Debugf("TikTok: extracted video %s", videoID)
	return info, nil
}

func extractTikTokID(url string) string {
	// https://www.tiktok.com/@user/video/1234567890
	re := regexp.MustCompile(`/video/(\d+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	// vm.tiktok.com/xxxx
	re2 := regexp.MustCompile(`vm\.tiktok\.com/([^/?]+)`)
	matches = re2.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	return "unknown"
}

func (e *TikTokExtractor) GetBestFormat(info *MediaInfo, qualityPref string, audioOnly bool) (MediaFormat, error) {
	if len(info.Formats) == 0 {
		return MediaFormat{}, errors.New("no formats available")
	}
	return info.Formats[0], nil
}

func (e *TikTokExtractor) Download(ctx context.Context, url string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	downloader := NewHTTPDownloader("/tmp/media-downloader")
	dest := downloader.GetTempFile(format.Ext)

	err := downloader.Download(ctx, format.URL, dest, progress)
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

func (e *TikTokExtractor) Close() error {
	return e.browser.Close()
}