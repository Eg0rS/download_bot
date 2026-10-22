package extractor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/rod/rod"
	"github.com/rod/rod/lib/launcher"
	"github.com/sirupsen/logrus"
)

type InstagramExtractor struct {
	browser *rod.Browser
	config  InstagramConfig
}

type InstagramConfig struct {
	Enabled bool
}

func NewInstagramExtractor(config InstagramConfig) (*InstagramExtractor, error) {
	url := launcher.New().Bin("/usr/bin/chromium").NoSandbox(true).MustLaunch()
	browser := rod.New().ControlURL(url).MustConnect()

	return &InstagramExtractor{
		browser: browser,
		config:  config,
	}, nil
}

func (e *InstagramExtractor) Name() string {
	return "instagram"
}

func (e *InstagramExtractor) CanHandle(url string) bool {
	return strings.Contains(url, "instagram.com") && (strings.Contains(url, "/reel/") || strings.Contains(url, "/p/") || strings.Contains(url, "/tv/"))
}

func (e *InstagramExtractor) Extract(ctx context.Context, url string) (*MediaInfo, error) {
	page := e.browser.MustPage(url).MustWaitLoad()
	defer page.MustClose()

	// Wait for video or image
	var videoURL string
	var isVideo bool

	// Try to find video element
	videoEl, err := page.Element("video")
	if err == nil {
		src, _ := videoEl.Property("src")
		if src != nil && src.String() != "" {
			videoURL = src.String()
			isVideo = true
		}
	}

	// Fallback: check for image (carousel or single post)
	if !isVideo {
		imgEl, err := page.Element(`article img[style*="object-fit"]`)
		if err != nil {
			imgEl, _ = page.Element("article img")
		}
		if imgEl != nil {
			src, _ := imgEl.Property("src")
			if src != nil {
				videoURL = src.String()
			}
		}
	}

	if videoURL == "" {
		return nil, fmt.Errorf("instagram: no media found")
	}

	// Extract metadata
	title := ""
	descEl, _ := page.Element(`h1, article header + div`)
	if descEl != nil {
		title = descEl.MustText()
		if len(title) > 200 {
			title = title[:200]
		}
	}

	authorEl, _ := page.Element(`header a[href^="/"]`)
	author := ""
	if authorEl != nil {
		author = authorEl.MustText()
	}

	postID := extractInstagramID(url)

	ext := "mp4"
	if !isVideo {
		ext = "jpg"
	}

	info := &MediaInfo{
		ID:       postID,
		Title:    title,
		Duration: 0,
		Uploader: author,
		Platform: "instagram",
		URL:      url,
		Formats: []MediaFormat{{
			FormatID: "default",
			Ext:      ext,
			Quality:  "original",
			URL:      videoURL,
			IsVideo:  isVideo,
			Protocol: "https",
		}},
	}

	logrus.Debugf("Instagram: extracted %s %s", postID, ext)
	return info, nil
}

func extractInstagramID(url string) string {
	// /reel/ABC123/ or /p/ABC123/ or /tv/ABC123/
	re := regexp.MustCompile(`/(reel|p|tv)/([^/?]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 2 {
		return matches[2]
	}
	return "unknown"
}

func (e *InstagramExtractor) GetBestFormat(info *MediaInfo, qualityPref string, audioOnly bool) (MediaFormat, error) {
	if len(info.Formats) == 0 {
		return MediaFormat{}, errors.New("no formats available")
	}
	return info.Formats[0], nil
}

func (e *InstagramExtractor) Download(ctx context.Context, url string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	return nil, nil // TODO: implement
}

func (e *InstagramExtractor) Close() error {
	return e.browser.Close()
}