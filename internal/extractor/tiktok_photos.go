package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	_ "golang.org/x/image/webp"
)

const maxTikTokPhotos = 100
const maxTelegramPhotoBytes int64 = 10 * 1024 * 1024

var tikTokHydrationScript = regexp.MustCompile(`(?is)<script\b[^>]*\bid\s*=\s*["']__UNIVERSAL_DATA_FOR_REHYDRATION__["'][^>]*>(.*?)</script\s*>`)

type tikTokPhotoItem struct {
	ID          string `json:"id"`
	Description string `json:"desc"`
	Private     bool   `json:"privateItem"`
	Author      struct {
		Name string `json:"uniqueId"`
	} `json:"author"`
	ImagePost struct {
		Title  string `json:"title"`
		Images []struct {
			ImageURL struct {
				URLs []string `json:"urlList"`
			} `json:"imageURL"`
			Width  int `json:"imageWidth"`
			Height int `json:"imageHeight"`
		} `json:"images"`
	} `json:"imagePost"`
}

func parseTikTokPhotos(page []byte, id, raw string) (*MediaInfo, error) {
	match := tikTokHydrationScript.FindSubmatch(page)
	if len(match) < 2 {
		return nil, fmt.Errorf("TikTok не отдал данные фотоальбома: возможно, требуется проверка доступа. Попробуйте позже.")
	}
	var data struct {
		Scope struct {
			Detail struct {
				Status   int `json:"statusCode"`
				ItemInfo struct {
					Item tikTokPhotoItem `json:"itemStruct"`
				} `json:"itemInfo"`
			} `json:"webapp.video-detail"`
		} `json:"__DEFAULT_SCOPE__"`
	}
	if err := json.Unmarshal(match[1], &data); err != nil {
		return nil, fmt.Errorf("не удалось прочитать данные фотоальбома TikTok: %w", err)
	}
	item := data.Scope.Detail.ItemInfo.Item
	if data.Scope.Detail.Status != 0 || item.Private {
		return nil, fmt.Errorf("Фотоальбом TikTok недоступен или требует авторизации")
	}
	if item.ID != id {
		return nil, fmt.Errorf("TikTok не вернул запрошенный фотоальбом")
	}
	if len(item.ImagePost.Images) == 0 {
		return nil, fmt.Errorf("В посте TikTok нет доступных фотографий")
	}
	if len(item.ImagePost.Images) > maxTikTokPhotos {
		return nil, fmt.Errorf("В фотоальбоме больше %d фотографий", maxTikTokPhotos)
	}
	title := item.Description
	if title == "" {
		title = item.ImagePost.Title
	}
	if title == "" {
		title = "TikTok " + id
	}
	info := &MediaInfo{ID: id, Title: title, Uploader: item.Author.Name, Platform: "tiktok", URL: raw}
	for i, img := range item.ImagePost.Images {
		source := ImageSource{Width: img.Width, Height: img.Height}
		for _, candidate := range img.ImageURL.URLs {
			if ValidateURL(candidate) == nil {
				source.URLs = append(source.URLs, candidate)
			}
		}
		if len(source.URLs) == 0 {
			return nil, fmt.Errorf("У фотографии %d нет безопасной ссылки для скачивания", i+1)
		}
		info.Images = append(info.Images, source)
	}
	return info, nil
}

func (e *TikTokExtractor) downloadPhotos(ctx context.Context, raw string, format MediaFormat, progress ProgressFunc) (*DownloadResult, error) {
	if !e.CanHandle(raw) {
		return nil, fmt.Errorf("unsupported TikTok URL")
	}
	if len(format.Images) == 0 || len(format.Images) > maxTikTokPhotos {
		return nil, fmt.Errorf("недопустимое количество фотографий")
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
	result := &DownloadResult{Format: format, DownloadedAt: time.Now()}
	d := NewHTTPDownloader(dir)
	d.client = e.client
	d.headers = http.Header{"User-Agent": []string{tikTokUserAgent}, "Referer": []string{"https://www.tiktok.com/"}}
	for i, source := range format.Images {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := maxDownloadBytes() - result.FileSize
		if remaining <= 0 {
			return nil, fmt.Errorf("Фотоальбом превышает допустимый размер")
		}
		d.maxBytes = min(maxTelegramPhotoBytes, remaining)
		input := filepath.Join(dir, "source-image")
		var downloadErr error
		for _, candidate := range source.URLs {
			if downloadErr = d.Download(ctx, candidate, input, nil); downloadErr == nil {
				break
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		if len(source.URLs) == 0 {
			return nil, fmt.Errorf("У фотографии %d нет ссылки", i+1)
		}
		if downloadErr != nil {
			return nil, fmt.Errorf("скачивание фотографии %d: %w", i+1, downloadErr)
		}
		output := filepath.Join(dir, fmt.Sprintf("photo_%03d.jpg", i+1))
		photo, err := normalizeTikTokPhoto(input, output, d.maxBytes)
		_ = os.Remove(input)
		if err != nil {
			return nil, fmt.Errorf("подготовка фотографии %d: %w", i+1, err)
		}
		result.Photos = append(result.Photos, photo)
		result.FileSize += photo.FileSize
		if progress != nil {
			progress(int64(i+1), int64(len(format.Images)), 0, 0)
		}
	}
	result.FilePath = result.Photos[0].FilePath
	success = true
	return result, nil
}

func normalizeTikTokPhoto(input, output string, limit int64) (DownloadedPhoto, error) {
	data, err := os.ReadFile(input)
	if err != nil {
		return DownloadedPhoto{}, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return DownloadedPhoto{}, fmt.Errorf("неподдерживаемый формат изображения: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 || cfg.Width+cfg.Height > 10000 || cfg.Width > 20*cfg.Height || cfg.Height > 20*cfg.Width {
		return DownloadedPhoto{}, fmt.Errorf("размеры изображения превышают ограничения Telegram")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return DownloadedPhoto{}, err
	}
	for _, quality := range []int{90, 75, 60} {
		file, err := os.OpenFile(output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return DownloadedPhoto{}, err
		}
		err = jpeg.Encode(file, img, &jpeg.Options{Quality: quality})
		closeErr := file.Close()
		if err != nil {
			return DownloadedPhoto{}, err
		}
		if closeErr != nil {
			return DownloadedPhoto{}, closeErr
		}
		stat, err := os.Stat(output)
		if err != nil {
			return DownloadedPhoto{}, err
		}
		if stat.Size() <= limit {
			return DownloadedPhoto{FilePath: output, FileSize: stat.Size(), Width: cfg.Width, Height: cfg.Height}, nil
		}
	}
	return DownloadedPhoto{}, fmt.Errorf("фотография превышает допустимый размер")
}
