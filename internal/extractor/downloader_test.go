package extractor

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestValidateURL(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "http://127.0.0.1/", "http://[::1]/", "http://[::ffff:127.0.0.1]/", "http://10.0.0.1/", "http://169.254.169.254/", "http://100.100.100.200/", "https://user:pass@example.org/", "http://localhost./", "https://a.local/"} {
		require.Error(t, ValidateURL(raw), raw)
	}
	require.NoError(t, ValidateURL("https://example.org/video.mp4"))
}

func TestHTTPDownloadLimitsAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		size      int64
		body      string
		wantError bool
	}{
		{"ok", 200, 3, "abc", false},
		{"http error", 500, 3, "abc", true},
		{"declared oversize", 200, 4, "abcd", true},
		{"streamed oversize", 200, -1, "abcd", true},
		{"truncated", 200, 3, "ab", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			d := NewHTTPDownloader(dir)
			d.maxBytes = 3
			d.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, ContentLength: tc.size, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			dest := filepath.Join(dir, "download.mp4")
			err := d.Download(context.Background(), "https://example.org/video.mp4", dest, nil)
			if tc.wantError {
				require.Error(t, err)
				_, err = os.Stat(dest)
				require.True(t, os.IsNotExist(err))
			} else {
				require.NoError(t, err)
				data, err := os.ReadFile(dest)
				require.NoError(t, err)
				require.Equal(t, "abc", string(data))
			}
		})
	}
}

func TestPlatformMatchingAndAudio(t *testing.T) {
	ext := NewYouTubeExtractor(YouTubeConfig{})
	require.True(t, ext.CanHandle("https://www.youtube.com/watch?v=1"))
	require.False(t, ext.CanHandle("https://youtube.com.evil.org/video"))
	require.False(t, ext.CanHandle("https://evil.org/?url=youtube.com"))
	f, err := ext.GetBestFormat(&MediaInfo{}, "audio", true)
	require.NoError(t, err)
	require.True(t, f.IsAudio)
	require.False(t, f.IsVideo)
	require.Equal(t, "mp3", f.Ext)
	f, err = ext.GetBestFormat(&MediaInfo{}, "720p", false)
	require.NoError(t, err)
	require.Contains(t, f.FormatID, "height<=720")
	_, err = ext.GetBestFormat(&MediaInfo{}, "garbage", false)
	require.Error(t, err)
}

func TestRegistryReplace(t *testing.T) {
	r := NewRegistry()
	old := NewYouTubeExtractor(YouTubeConfig{})
	replacement := NewYouTubeExtractor(YouTubeConfig{DownloadSubtitles: true})
	r.Register(old)
	r.Register(replacement)
	got, err := r.FindForURL("https://youtu.be/test")
	require.NoError(t, err)
	require.Same(t, replacement, got)
	require.Len(t, r.ordered, 1)
}
