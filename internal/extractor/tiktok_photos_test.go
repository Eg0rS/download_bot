package extractor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func photoPage(id string, urls ...string) []byte {
	images := make([]any, 0, len(urls))
	for _, u := range urls {
		images = append(images, map[string]any{"imageURL": map[string]any{"urlList": []string{u}}, "imageWidth": 32, "imageHeight": 48})
	}
	item := map[string]any{"id": id, "desc": "Фотоальбом 🌅", "author": map[string]any{"uniqueId": "creator"}, "imagePost": map[string]any{"images": images}}
	data, _ := json.Marshal(map[string]any{"__DEFAULT_SCOPE__": map[string]any{"webapp.video-detail": map[string]any{"statusCode": 0, "itemInfo": map[string]any{"itemStruct": item}}}})
	return append(append([]byte(`<html><script type="application/json" id='__UNIVERSAL_DATA_FOR_REHYDRATION__'>`), data...), []byte("</script></html>")...)
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 48))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestTikTokPhotoMetadata(t *testing.T) {
	info, err := parseTikTokPhotos(photoPage("123", "https://cdn.example.org/1.webp", "https://cdn.example.org/2.jpg"), "123", "https://www.tiktok.com/@u/photo/123")
	require.NoError(t, err)
	require.Len(t, info.Images, 2)
	require.Equal(t, "Фотоальбом 🌅", info.Title)
	require.Equal(t, "creator", info.Uploader)
	require.Equal(t, "https://cdn.example.org/2.jpg", info.Images[1].URLs[0])
	_, err = parseTikTokPhotos(photoPage("123", "http://169.254.169.254/image"), "123", "")
	require.Error(t, err)
	_, err = parseTikTokPhotos(photoPage("123", "https://cdn.example.org/1.jpg"), "456", "")
	require.Error(t, err)
	_, err = parseTikTokPhotos([]byte("<html>Challenge</html>"), "123", "")
	require.Error(t, err)
	_, err = parseTikTokPhotos(photoPage("123"), "123", "")
	require.Error(t, err)
}

func TestTikTokShortPhotoLinkAndDownload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEMP_DIR", dir)
	ext, err := NewTikTokExtractor(TikTokConfig{})
	require.NoError(t, err)
	var requests []string
	pngData := testPNG(t)
	ext.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		status := 200
		var body []byte
		switch req.URL.Host {
		case "vt.tiktok.com":
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://www.tiktok.com/@u/photo/123?tracking=1"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		case "www.tiktok.com":
			require.Equal(t, "/@u/video/123", req.URL.Path)
			require.Empty(t, req.URL.RawQuery)
			body = photoPage("123", "https://cdn.example.org/first", "https://cdn.example.org/second")
		case "cdn.example.org":
			require.Equal(t, "https://www.tiktok.com/", req.Header.Get("Referer"))
			body = pngData
		default:
			t.Fatalf("unexpected host %s", req.URL.Host)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: req}, nil
	})}
	// The resolved /photo/ page is discarded, then the /video/ hydration page is read.
	base := ext.client.Transport
	ext.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "www.tiktok.com" && strings.Contains(req.URL.Path, "/photo/") {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("photo landing")), Request: req}, nil
		}
		return base.RoundTrip(req)
	})
	info, err := ext.Extract(context.Background(), "https://vt.tiktok.com/short/")
	require.NoError(t, err)
	require.Len(t, info.Images, 2)
	format, err := ext.GetBestFormat(info, "best", false)
	require.NoError(t, err)
	require.True(t, format.IsPhoto)
	_, err = ext.GetBestFormat(info, "audio", true)
	require.Error(t, err)
	result, err := ext.Download(context.Background(), "https://vt.tiktok.com/short/", format, nil)
	require.NoError(t, err)
	require.Len(t, result.Photos, 2)
	require.Equal(t, result.Photos[0].FilePath, result.FilePath)
	for i, p := range result.Photos {
		require.Equal(t, fmt.Sprintf("photo_%03d.jpg", i+1), filepath.Base(p.FilePath))
		data, err := os.ReadFile(p.FilePath)
		require.NoError(t, err)
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		require.NoError(t, err)
		require.Equal(t, 32, cfg.Width)
		require.Equal(t, 48, cfg.Height)
	}
	require.Greater(t, result.FileSize, int64(0))
	require.NotEmpty(t, requests)
}

func TestTikTokPhotoFailureCleansWholeAlbum(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEMP_DIR", dir)
	ext, _ := NewTikTokExtractor(TikTokConfig{})
	data := testPNG(t)
	ext.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		status := 200
		body := data
		if req.URL.Path == "/second" {
			status = 403
			body = nil
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: req}, nil
	})}
	format := MediaFormat{IsPhoto: true, Images: []ImageSource{{URLs: []string{"https://cdn.example.org/first"}}, {URLs: []string{"https://cdn.example.org/second"}}}}
	_, err := ext.Download(context.Background(), "https://www.tiktok.com/@u/photo/123", format, nil)
	require.Error(t, err)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestTikTokPhotoSizeAndCancellation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TEMP_DIR", dir)
	input := filepath.Join(dir, "source")
	require.NoError(t, os.WriteFile(input, testPNG(t), 0600))
	_, err := normalizeTikTokPhoto(input, filepath.Join(dir, "result.jpg"), 10)
	require.Error(t, err)
	ext, _ := NewTikTokExtractor(TikTokConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ext.Download(ctx, "https://www.tiktok.com/@u/photo/123", MediaFormat{IsPhoto: true, Images: []ImageSource{{URLs: []string{"https://cdn.example.org/first"}}}}, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestTikTokWebPToJPEG(t *testing.T) {
	dir := t.TempDir()
	data, err := base64.StdEncoding.DecodeString("UklGRhwAAABXRUJQVlA4TA8AAAAvB0ACAAcQ/Y/+ByKi/wEA")
	require.NoError(t, err)
	input, output := filepath.Join(dir, "input.webp"), filepath.Join(dir, "output.jpg")
	require.NoError(t, os.WriteFile(input, data, 0600))
	photo, err := normalizeTikTokPhoto(input, output, maxTelegramPhotoBytes)
	require.NoError(t, err)
	require.Equal(t, 8, photo.Width)
	require.Equal(t, 10, photo.Height)
	encoded, err := os.ReadFile(output)
	require.NoError(t, err)
	_, err = jpeg.Decode(bytes.NewReader(encoded))
	require.NoError(t, err)
}
