package extractor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gen2brain/dl"
	"github.com/u2takey/ffmpeg-go"
	"github.com/sirupsen/logrus"
)

type HTTPDownloader struct {
	client *http.Client
	tempDir string
}

func NewHTTPDownloader(tempDir string) *HTTPDownloader {
	return &HTTPDownloader{
		client: &http.Client{
			Timeout: 30 * time.Minute,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		tempDir: tempDir,
	}
}

func (d *HTTPDownloader) Download(ctx context.Context, url, dest string, progress ProgressFunc) error {
	// Use gen2brain/dl for resumable download
	dlClient := dl.NewDownloader()
	dlClient.SetTimeout(30 * time.Minute)

	return dlClient.Download(url, dest, func(downloaded, total int64, speed float64) {
		if progress != nil {
			var eta time.Duration
			if speed > 0 && total > 0 {
				eta = time.Duration(float64(total-downloaded)/speed) * time.Second
			}
			progress(downloaded, total, speed, eta)
		}
	})
}

func (d *HTTPDownloader) DownloadHLS(ctx context.Context, m3u8URL, dest string, progress ProgressFunc) error {
	// Use ffmpeg to download HLS stream
	// ffmpeg -i "url" -c copy output.mp4
	args := ffmpeg.KwArgs{
		"i": m3u8URL,
		"c": "copy",
		"f": "mp4",
		"y": "",
	}

	// Progress monitoring via ffmpeg stderr is complex
	// For now, run without progress
	cmd := ffmpeg.Input(m3u8URL, ffmpeg.KwArgs{"protocol_whitelist": "file,http,https,tcp,tls,crypto"}).Output(dest, args)
	
	// Run with progress via stderr parsing (simplified)
	return cmd.Run()
}

func (d *HTTPDownloader) MergeVideoAudio(ctx context.Context, videoPath, audioPath, dest string, progress ProgressFunc) error {
	// ffmpeg -i video.mp4 -i audio.m4a -c:v copy -c:a aac output.mp4
	// Use progress via stderr parsing
	cmd := ffmpeg.Input(videoPath).Input(audioPath).
		Output(dest, ffmpeg.KwArgs{
			"c:v": "copy",
			"c:a": "aac",
			"b:a": "128k",
			"y":   "",
		})

	// Run with progress monitoring
	return d.runWithProgress(cmd, progress)
}

func (d *HTTPDownloader) runWithProgress(cmd *ffmpeg.Stream, progress ProgressFunc) error {
	if progress == nil {
		return cmd.Run()
	}

	// ffmpeg outputs progress to stderr in format:
	// frame= 123 fps= 45 q=28.0 size= 1024kB time=00:00:05.00 bitrate= 123.4kbits/s speed=2.5x
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	// Parse stderr for progress
	buf := make([]byte, 1024)
	for {
		n, err := stderr.Read(buf)
		if n > 0 {
			line := string(buf[:n])
			if strings.Contains(line, "time=") {
				// Extract time=HH:MM:SS.xx
				if idx := strings.Index(line, "time="); idx >= 0 {
					timeStr := line[idx+5 : idx+15] // HH:MM:SS.xx
					if t, err := parseFFmpegTime(timeStr); err == nil {
						progress(t, 0, 0, 0) // Report current time as progress
					}
				}
			}
		}
		if err != nil {
			break
		}
	}

	return cmd.Wait()
}

func parseFFmpegTime(s string) (int64, error) {
	// HH:MM:SS.xx
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid time format")
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	sec, _ := strconv.ParseFloat(parts[2], 64)
	return int64(h*3600 + m*60 + sec), nil
}

func (d *HTTPDownloader) ConvertToMP3(ctx context.Context, src, dest string) error {
	return ffmpeg.Input(src).Output(dest, ffmpeg.KwArgs{
		"c:a": "libmp3lame",
		"b:a": "192k",
		"y":   "",
	}).Run()
}

func (d *HTTPDownloader) GetTempFile(ext string) string {
	return filepath.Join(d.tempDir, fmt.Sprintf("dl_%d_%s.%s", time.Now().UnixNano(), randomString(8), ext))
}

func (d *HTTPDownloader) Cleanup(paths ...string) {
	for _, p := range paths {
		if p != "" {
			os.Remove(p)
		}
	}
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}

// Complete download with adaptive format merging (for YouTube)
func (d *HTTPDownloader) DownloadAdaptive(ctx context.Context, videoURL, audioURL string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	videoTemp := d.GetTempFile("video.mp4")
	audioTemp := d.GetTempFile("audio.m4a")
	outputTemp := d.GetTempFile(format.Ext)

	defer d.Cleanup(videoTemp, audioTemp)

	// Download video stream
	if err := d.Download(ctx, videoURL, videoTemp, func(dl, tot int64, sp float64, eta time.Duration) {
		if progress != nil {
			// Report 50% weight for video
			progress(dl/2, tot/2, sp, eta)
		}
	}); err != nil {
		return nil, fmt.Errorf("video download failed: %w", err)
	}

	// Download audio stream
	if err := d.Download(ctx, audioURL, audioTemp, func(dl, tot int64, sp float64, eta time.Duration) {
		if progress != nil {
			// Report 50% weight for audio
			progress(dl/2, tot/2, sp, eta)
		}
	}); err != nil {
		return nil, fmt.Errorf("audio download failed: %w", err)
	}

	// Merge
	if err := d.MergeVideoAudio(ctx, videoTemp, audioTemp, outputTemp); err != nil {
		return nil, fmt.Errorf("merge failed: %w", err)
	}

	// Get file info
	info, _ := os.Stat(outputTemp)

	return &DownloadResult{
		FilePath:     outputTemp,
		Format:       format,
		FileSize:     info.Size(),
		DownloadedAt: time.Now(),
	}, nil
}