package bot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	tele "gopkg.in/telebot.v3"
	"media-downloader-bot/internal/extractor"
	"media-downloader-bot/internal/models"
)

func TestPhotoDelivery(t *testing.T) {
	for _, test := range []struct {
		count  int
		groups []int
	}{
		{1, []int{1}}, {2, []int{2}}, {10, []int{10}}, {11, []int{9, 2}}, {21, []int{10, 9, 2}},
	} {
		t.Run(strconv.Itoa(test.count), func(t *testing.T) {
			var mu sync.Mutex
			var groups []int
			var files []string
			var captions []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					http.Error(w, "multipart", 400)
					return
				}
				defer r.MultipartForm.RemoveAll()
				var media []struct {
					Type    string `json:"type"`
					Media   string `json:"media"`
					Caption string `json:"caption"`
				}
				single := strings.HasSuffix(r.URL.Path, "/sendPhoto")
				if single {
					media = append(media, struct {
						Type    string `json:"type"`
						Media   string `json:"media"`
						Caption string `json:"caption"`
					}{"photo", "attach://photo", r.FormValue("caption")})
				} else {
					if !strings.HasSuffix(r.URL.Path, "/sendMediaGroup") {
						t.Errorf("unexpected method %s", r.URL.Path)
					}
					if err := json.Unmarshal([]byte(r.FormValue("media")), &media); err != nil {
						t.Error(err)
						http.Error(w, "media", 400)
						return
					}
				}
				groups = append(groups, len(media))
				messages := make([]map[string]any, 0, len(media))
				for _, m := range media {
					if m.Type != "photo" {
						t.Errorf("wrong media type: %s", m.Type)
					}
					key := strings.TrimPrefix(m.Media, "attach://")
					file, _, err := r.FormFile(key)
					var data []byte
					if err == http.ErrMissingFile && len(r.MultipartForm.Value[key]) > 0 {
						// Telebot sends photos with an empty multipart filename.
						data = []byte(r.MultipartForm.Value[key][0])
						err = nil
					} else if err != nil {
						t.Error(err)
						http.Error(w, "file", 400)
						return
					} else {
						data, err = io.ReadAll(file)
						file.Close()
					}
					if err != nil {
						t.Error(err)
					}
					files = append(files, string(data))
					captions = append(captions, m.Caption)
					messages = append(messages, map[string]any{"message_id": len(files), "chat": map[string]any{"id": 123, "type": "private"}, "photo": []map[string]any{{"file_id": "uploaded", "file_unique_id": "unique", "width": 32, "height": 48}}})
				}
				var result any = messages
				if single {
					result = messages[0]
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
			}))
			defer server.Close()
			client, err := tele.NewBot(tele.Settings{Token: "test", Offline: true, URL: server.URL, Client: server.Client()})
			require.NoError(t, err)
			db := setupTestDB()
			task := &models.DownloadTask{ChatID: 123, Status: models.StatusUploading}
			require.NoError(t, db.Create(task).Error)
			b := &Bot{bot: client, cfg: setupTestConfig(), db: db}
			result := &extractor.DownloadResult{Title: "Фотоальбом", Format: extractor.MediaFormat{IsPhoto: true}}
			dir := t.TempDir()
			for i := 0; i < test.count; i++ {
				path := filepath.Join(dir, fmt.Sprintf("photo_%03d.jpg", i))
				content := fmt.Sprintf("photo-%d", i)
				require.NoError(t, os.WriteFile(path, []byte(content), 0600))
				result.Photos = append(result.Photos, extractor.DownloadedPhoto{FilePath: path, FileSize: int64(len(content)), Width: 32, Height: 48})
				result.FileSize += int64(len(content))
			}
			require.NoError(t, b.sendFile(task, result))
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, test.groups, groups)
			require.Len(t, files, test.count)
			for i, content := range files {
				require.Equal(t, fmt.Sprintf("photo-%d", i), content)
			}
			require.Contains(t, captions[0], "Фотоальбом")
			for _, caption := range captions[1:] {
				require.Empty(t, caption)
			}
			require.Equal(t, test.count, task.MessageID)
		})
	}
}
