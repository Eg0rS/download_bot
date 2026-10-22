package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type platformExtractor struct {
	name      string
	domains   []string
	subtitles bool
	langs     []string
}

func (e *platformExtractor) Name() string { return e.name }
func (e *platformExtractor) Close() error { return nil }
func (e *platformExtractor) CanHandle(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || ValidateURL(raw) != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, domain := range e.domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func (e *platformExtractor) validate(ctx context.Context, raw string) error {
	if !e.CanHandle(raw) {
		return fmt.Errorf("unsupported %s URL", e.name)
	}
	u, _ := url.Parse(raw)
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return fmt.Errorf("no addresses found")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return fmt.Errorf("private addresses are not allowed")
		}
	}
	return nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if len(p) > remaining {
		b.exceeded = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
func runDLP(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	out := &limitedBuffer{limit: 8 * 1024 * 1024}
	stderr := &limitedBuffer{limit: 16 * 1024}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if out.exceeded {
		return nil, fmt.Errorf("metadata exceeds limit")
	}
	return out.Bytes(), nil
}
func baseArgs() []string {
	return []string{"--ignore-config", "--no-playlist", "--playlist-items", "1", "--no-warnings", "--socket-timeout", "20", "--retries", "2", "--no-cache-dir", "--js-runtimes", "node"}
}
func (e *platformExtractor) Extract(ctx context.Context, raw string) (*MediaInfo, error) {
	if err := e.validate(ctx, raw); err != nil {
		return nil, err
	}
	args := append(baseArgs(), "--skip-download", "--dump-single-json", "--", raw)
	data, err := runDLP(ctx, args...)
	if err != nil {
		return nil, err
	}
	var doc struct {
		ID          string
		Title       string
		Description string
		Duration    float64
		Thumbnail   string
		Uploader    string
		ViewCount   int64 `json:"view_count"`
		Formats     []struct {
			ID     string `json:"format_id"`
			Ext    string
			Height int
			Width  int
			FPS    float64
			VCodec string
			ACodec string
			URL    string
			Size   int64 `json:"filesize"`
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid metadata: %w", err)
	}
	info := &MediaInfo{ID: doc.ID, Title: doc.Title, Description: doc.Description, Duration: int(doc.Duration), Thumbnail: doc.Thumbnail, Uploader: doc.Uploader, ViewCount: doc.ViewCount, Platform: e.name, URL: raw}
	for _, f := range doc.Formats {
		info.Formats = append(info.Formats, MediaFormat{FormatID: f.ID, Ext: f.Ext, Height: f.Height, Width: f.Width, FPS: int(f.FPS), VCodec: f.VCodec, ACodec: f.ACodec, URL: f.URL, FileSize: f.Size, IsVideo: f.VCodec != "" && f.VCodec != "none", IsAudio: f.ACodec != "" && f.ACodec != "none" && (f.VCodec == "" || f.VCodec == "none")})
	}
	return info, nil
}

func (e *platformExtractor) GetBestFormat(info *MediaInfo, quality string, audioOnly bool) (MediaFormat, error) {
	if info == nil {
		return MediaFormat{}, fmt.Errorf("missing media info")
	}
	if audioOnly || quality == "audio" || quality == "mp3" || quality == "audio_only" || e.name == "soundcloud" {
		return MediaFormat{FormatID: "bestaudio/best", Ext: "mp3", Quality: "audio", IsAudio: true}, nil
	}
	selector := "bv*[ext=mp4]+ba[ext=m4a]/b[ext=mp4]/bv*+ba/b"
	if quality != "" && quality != "best" {
		height := parseHeight(quality)
		if height <= 0 {
			return MediaFormat{}, fmt.Errorf("invalid quality: %s", quality)
		}
		bound := "[height<=" + strconv.Itoa(height) + "]"
		selector = "bv*[ext=mp4]" + bound + "+ba[ext=m4a]/b[ext=mp4]" + bound + "/bv*" + bound + "+ba/b" + bound
	}
	return MediaFormat{FormatID: selector, Ext: "mp4", Quality: quality, IsVideo: true}, nil
}

func (e *platformExtractor) Download(ctx context.Context, raw string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	if err := e.validate(ctx, raw); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(tempDir(), 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(tempDir(), "job_")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dir)
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Bound disk use even for streams with no Content-Length or separate audio/video.
	watchDone := make(chan struct{})
	watchStopped := make(chan struct{})
	defer func() { close(watchDone); <-watchStopped }()
	go func() {
		defer close(watchStopped)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				var size int64
				_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
					if err == nil && !d.IsDir() {
						if st, err := d.Info(); err == nil {
							size += st.Size()
						}
					}
					return nil
				})
				if size > maxDownloadBytes()*3 {
					cancel()
					return
				}
			}
		}
	}()
	args := append(baseArgs(), "--no-progress", "--max-filesize", strconv.FormatInt(maxDownloadBytes(), 10), "--format", format.FormatID, "--output", filepath.Join(dir, "media.%(ext)s"), "--print", "after_move:%(filepath)j")
	if format.IsAudio {
		args = append(args, "--extract-audio", "--audio-format", "mp3", "--audio-quality", "192K")
	} else {
		args = append(args, "--merge-output-format", "mp4", "--remux-video", "mp4")
		if e.subtitles {
			langs := strings.Join(e.langs, ",")
			if langs == "" {
				langs = "ru,en"
			}
			args = append(args, "--write-subs", "--sub-langs", langs, "--embed-subs")
		}
	}
	args = append(args, "--", raw)
	data, err := runDLP(ctx, args...)
	if err != nil {
		return nil, err
	}
	var dest string
	if err := json.Unmarshal(bytes.TrimSpace(data), &dest); err != nil {
		return nil, fmt.Errorf("download produced no file (possibly exceeds size limit)")
	}
	rel, err := filepath.Rel(dir, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("invalid output path")
	}
	stat, err := os.Stat(dest)
	if err != nil {
		return nil, err
	}
	if stat.Size() > maxDownloadBytes() {
		return nil, fmt.Errorf("file exceeds Telegram upload limit")
	}
	format.Ext = strings.TrimPrefix(filepath.Ext(dest), ".")
	if progress != nil {
		progress(stat.Size(), stat.Size(), 0, 0)
	}
	success = true
	return &DownloadResult{FilePath: dest, FileSize: stat.Size(), Format: format, DownloadedAt: time.Now()}, nil
}
