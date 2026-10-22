package extractor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type TikTokConfig struct{ Watermark bool }
type TikTokExtractor struct {
	*platformExtractor
	client *http.Client
}

const tikTokUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

var tikTokPostPath = regexp.MustCompile(`^/@[^/]+/(photo|video)/(\d+)/?$`)

func NewTikTokExtractor(cfg TikTokConfig) (*TikTokExtractor, error) {
	client := safeHTTPClient()
	client.Timeout = 40 * time.Second
	client.Jar, _ = cookiejar.New(nil)
	return &TikTokExtractor{platformExtractor: &platformExtractor{name: "tiktok", domains: []string{"tiktok.com"}}, client: client}, nil
}

func (e *TikTokExtractor) requestPage(ctx context.Context, raw string) (*http.Response, error) {
	if !e.CanHandle(raw) {
		return nil, fmt.Errorf("unsupported TikTok URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", tikTokUserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("TikTok вернул HTTP %d; попробуйте позже", resp.StatusCode)
	}
	if !e.CanHandle(resp.Request.URL.String()) {
		resp.Body.Close()
		return nil, fmt.Errorf("TikTok перенаправил на страницу авторизации или неподдерживаемый сайт")
	}
	return resp, nil
}

func (e *TikTokExtractor) Extract(ctx context.Context, raw string) (*MediaInfo, error) {
	if !e.CanHandle(raw) {
		return nil, fmt.Errorf("unsupported TikTok URL")
	}
	u, _ := url.Parse(raw)
	match := tikTokPostPath.FindStringSubmatch(u.Path)
	if len(match) == 0 {
		// Short links can point to either a video or a photo post.
		resp, err := e.requestPage(ctx, raw)
		if err != nil {
			return nil, err
		}
		u = resp.Request.URL
		resp.Body.Close()
		match = tikTokPostPath.FindStringSubmatch(u.Path)
	}
	if len(match) == 0 || match[1] != "photo" {
		return e.platformExtractor.Extract(ctx, u.String())
	}
	return e.extractPhotos(ctx, u, match[2])
}

func (e *TikTokExtractor) extractPhotos(ctx context.Context, u *url.URL, id string) (*MediaInfo, error) {
	canonical := *u
	// TikTok exposes photo-post metadata in the video-detail hydration scope.
	canonical.Path = strings.Replace(canonical.Path, "/photo/", "/video/", 1)
	canonical.RawPath = ""
	canonical.RawQuery = ""
	canonical.Fragment = ""
	resp, err := e.requestPage(ctx, canonical.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	const maxPageBytes = 8 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPageBytes {
		return nil, fmt.Errorf("страница TikTok превышает допустимый размер")
	}
	return parseTikTokPhotos(data, id, u.String())
}

func (e *TikTokExtractor) GetBestFormat(info *MediaInfo, quality string, audioOnly bool) (MediaFormat, error) {
	if info != nil && len(info.Images) > 0 {
		if audioOnly || quality == "audio" || quality == "mp3" || quality == "audio_only" {
			return MediaFormat{}, fmt.Errorf("Это фотоальбом TikTok. Пришлите ссылку без audio, чтобы скачать фотографии.")
		}
		return MediaFormat{FormatID: "photo_album", Ext: "jpg", Quality: "photos", IsPhoto: true, Images: info.Images}, nil
	}
	return e.platformExtractor.GetBestFormat(info, quality, audioOnly)
}

func (e *TikTokExtractor) Download(ctx context.Context, raw string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	if format.IsPhoto {
		return e.downloadPhotos(ctx, raw, format, progress)
	}
	return e.platformExtractor.Download(ctx, raw, format, progress)
}
