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

type TwitterExtractor struct {
	browser *rod.Browser
	config  TwitterConfig
}

type TwitterConfig struct {
	Enabled bool
}

func NewTwitterExtractor(config TwitterConfig) (*TwitterExtractor, error) {
	url := launcher.New().Bin("/usr/bin/chromium").NoSandbox(true).MustLaunch()
	browser := rod.New().ControlURL(url).MustConnect()

	return &TwitterExtractor{
		browser: browser,
		config:  config,
	}, nil
}

func (e *TwitterExtractor) Name() string {
	return "twitter"
}

func (e *TwitterExtractor) CanHandle(url string) bool {
	return strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com")
}

func (e *TwitterExtractor) Extract(ctx context.Context, url string) (*MediaInfo, error) {
	page := e.browser.MustPage(url).MustWaitLoad()
	defer page.MustClose()

	// Twitter requires more waiting for JS to load video
	page.MustWaitStable(time.Second * 3)

	var videoURL string
	var isVideo bool

	// Try to find video player
	videoEl, err := page.Element(`div[data-testid="videoPlayer"] video`)
	if err != nil {
		videoEl, err = page.Element("video")
	}
	if videoEl != nil {
		src, _ := videoEl.Property("src")
		if src != nil && src.String() != "" {
			videoURL = src.String()
			isVideo = true
		}
	}

	// Fallback: check for GIF (which is actually video)
	if !isVideo {
		gifEl, _ := page.Element(`div[data-testid="tweetPhoto"] video`)
		if gifEl != nil {
			src, _ := gifEl.Property("src")
			if src != nil {
				videoURL = src.String()
				isVideo = true
			}
		}
	}

	if videoURL == "" {
		return nil, fmt.Errorf("twitter: no video found")
	}

	// Extract tweet text as title
	title := ""
	tweetEl, _ := page.Element(`article[data-testid="tweet"]`)
	if tweetEl != nil {
		title = tweetEl.MustText()
		if len(title) > 200 {
			title = title[:200]
		}
	}

	// Author
	authorEl, _ := page.Element(`article[data-testid="tweet"] a[href^="/"]`)
	author := ""
	if authorEl != nil {
		author = authorEl.MustText()
	}

	tweetID := extractTweetID(url)

	info := &MediaInfo{
		ID:       tweetID,
		Title:    title,
		Duration: 0,
		Uploader: author,
		Platform: "twitter",
		URL:      url,
		Formats: []MediaFormat{{
			FormatID: "default",
			Ext:      "mp4",
			Quality:  "original",
			URL:      videoURL,
			IsVideo:  true,
			Protocol: "https",
		}},
	}

	logrus.Debugf("Twitter: extracted tweet %s", tweetID)
	return info, nil
}

func extractTweetID(url string) string {
	// https://twitter.com/user/status/1234567890 or x.com/user/status/1234567890
	re := regexp.MustCompile(`status/(\d+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	return "unknown"
}

func (e *TwitterExtractor) GetBestFormat(info *MediaInfo, qualityPref string, audioOnly bool) (MediaFormat, error) {
	if len(info.Formats) == 0 {
		return MediaFormat{}, errors.New("no formats available")
	}
	return info.Formats[0], nil
}

func (e *TwitterExtractor) Download(ctx context.Context, url string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	return nil, nil // TODO: implement
}

func (e *TwitterExtractor) Close() error {
	return e.browser.Close()
}