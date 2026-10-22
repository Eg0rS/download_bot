package extractor

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

type GenericConfig struct{ Enabled bool }
type GenericExtractor struct {
	client *http.Client
	config GenericConfig
}

func NewGenericExtractor(config GenericConfig) *GenericExtractor {
	return &GenericExtractor{safeHTTPClient(), config}
}
func (e *GenericExtractor) Name() string { return "generic" }
func (e *GenericExtractor) CanHandle(raw string) bool {
	return e.config.Enabled && ValidateURL(raw) == nil
}
func (e *GenericExtractor) Extract(ctx context.Context, raw string) (*MediaInfo, error) {
	if err := ValidateURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	isVideo, isAudio := strings.HasPrefix(ct, "video/"), strings.HasPrefix(ct, "audio/")
	if !isVideo && !isAudio && !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("URL does not point to supported media")
	}
	u, _ := url.Parse(raw)
	title := path.Base(u.Path)
	ext := strings.TrimPrefix(path.Ext(u.Path), ".")
	if exts, _ := mime.ExtensionsByType(ct); len(exts) > 0 {
		ext = strings.TrimPrefix(exts[0], ".")
	}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		title = path.Base(params["filename"])
	}
	return &MediaInfo{Title: title, Platform: "generic", URL: raw, Formats: []MediaFormat{{FormatID: "direct", Ext: ext, Quality: "original", URL: raw, FileSize: resp.ContentLength, IsAudio: isAudio, IsVideo: isVideo}}}, nil
}
func (e *GenericExtractor) GetBestFormat(info *MediaInfo, quality string, audioOnly bool) (MediaFormat, error) {
	if info == nil || len(info.Formats) == 0 {
		return MediaFormat{}, fmt.Errorf("no formats available")
	}
	if audioOnly && !info.Formats[0].IsAudio {
		return MediaFormat{}, fmt.Errorf("audio extraction is supported for platform links")
	}
	return info.Formats[0], nil
}
func (e *GenericExtractor) Download(ctx context.Context, raw string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	d := NewHTTPDownloader(tempDir())
	dest := d.GetTempFile(format.Ext)
	if err := d.Download(ctx, raw, dest, progress); err != nil {
		return nil, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		d.Cleanup(dest)
		return nil, err
	}
	return &DownloadResult{FilePath: dest, Format: format, FileSize: info.Size(), DownloadedAt: time.Now()}, nil
}
