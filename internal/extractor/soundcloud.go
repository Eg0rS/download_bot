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

type SoundCloudExtractor struct {
	browser *rod.Browser
	config  SoundCloudConfig
}

type SoundCloudConfig struct {
	Enabled bool
}

func NewSoundCloudExtractor(config SoundCloudConfig) (*SoundCloudExtractor, error) {
	url := launcher.New().Bin("/usr/bin/chromium").NoSandbox(true).MustLaunch()
	browser := rod.New().ControlURL(url).MustConnect()

	return &SoundCloudExtractor{
		browser: browser,
		config:  config,
	}, nil
}

func (e *SoundCloudExtractor) Name() string {
	return "soundcloud"
}

func (e *SoundCloudExtractor) CanHandle(url string) bool {
	return strings.Contains(url, "soundcloud.com")
}

func (e *SoundCloudExtractor) Extract(ctx context.Context, url string) (*MediaInfo, error) {
	page := e.browser.MustPage(url).MustWaitLoad()
	defer page.MustClose()

	// Wait for player to load
	page.MustWaitStable(time.Second * 2)

	// Try to find audio source
	var audioURL string

	// Method 1: Check for streaming URL in network requests (complex)
	// Method 2: Parse from page data
	// SoundCloud embeds track data in window.__sc_hydration

	// Try to get from meta tags
	metaEl, _ := page.Element(`meta[property="og:audio"]`)
	if metaEl != nil {
		content, _ := metaEl.Property("content")
		if content != nil {
			audioURL = content.String()
		}
	}

	// Fallback: twitter:player:stream
	if audioURL == "" {
		metaEl, _ = page.Element(`meta[name="twitter:player:stream"]`)
		if metaEl != nil {
			content, _ := metaEl.Property("content")
			if content != nil {
				audioURL = content.String()
			}
		}
	}

	// Fallback: look for stream URL in scripts
	if audioURL == "" {
		scripts, _ := page.Elements("script")
		for _, s := range scripts {
			text, _ := s.Property("innerText")
			if text != nil && strings.Contains(text.String(), ".m3u8") {
				re := regexp.MustCompile(`https?://[^"'\s]+\.m3u8[^"'\s]*`)
				match := re.FindString(text.String())
				if match != "" {
					audioURL = match
					break
				}
			}
		}
	}

	if audioURL == "" {
		return nil, fmt.Errorf("soundcloud: no audio stream found")
	}

	// Extract metadata
	title := ""
	titleEl, _ := page.Element(`h1[itemprop="name"], h1.trackTitle, .soundTitle__title`)
	if titleEl != nil {
		title = titleEl.MustText()
	}

	author := ""
	authorEl, _ := page.Element(`a[itemprop="byArtist"], .soundTitle__username`)
	if authorEl != nil {
		author = authorEl.MustText()
	}

	duration := 0
	durEl, _ := page.Element(`meta[itemprop="duration"]`)
	if durEl != nil {
		content, _ := durEl.Property("content")
		if content != nil {
			// PT3M45S format
			duration = parseISO8601Duration(content.String())
		}
	}

	artwork := ""
	artEl, _ := page.Element(`meta[property="og:image"]`)
	if artEl != nil {
		content, _ := artEl.Property("content")
		if content != nil {
			artwork = content.String()
		}
	}

	trackID := extractSoundCloudID(url)

	info := &MediaInfo{
		ID:       trackID,
		Title:    title,
		Duration: duration,
		Thumbnail: artwork,
		Uploader: author,
		Platform: "soundcloud",
		URL:      url,
		Formats: []MediaFormat{{
			FormatID: "hls",
			Ext:      "mp3",
			Quality:  "original",
			URL:      audioURL,
			IsAudio:  true,
			Protocol: "hls",
		}},
	}

	logrus.Debugf("SoundCloud: extracted track %s", trackID)
	return info, nil
}

func extractSoundCloudID(url string) string {
	// https://soundcloud.com/user/track-name-123456
	re := regexp.MustCompile(`soundcloud\.com/[^/]+/([^/?]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) > 1 {
		return matches[1]
	}
	return "unknown"
}

func parseISO8601Duration(s string) int {
	// PT3M45S -> 225 seconds
	s = strings.TrimPrefix(s, "PT")
	var minutes, seconds int
	fmt.Sscanf(s, "%dM%dS", &minutes, &seconds)
	return minutes*60 + seconds
}

func (e *SoundCloudExtractor) GetBestFormat(info *MediaInfo, qualityPref string, audioOnly bool) (MediaFormat, error) {
	if len(info.Formats) == 0 {
		return MediaFormat{}, errors.New("no formats available")
	}
	return info.Formats[0], nil
}

func (e *SoundCloudExtractor) Download(ctx context.Context, url string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	// HLS stream - need ffmpeg to download
	return nil, nil // TODO: implement with ffmpeg-go
}

func (e *SoundCloudExtractor) Close() error {
	return e.browser.Close()
}