package bot

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	tele "gopkg.in/telebot.v3"
	gorm "gorm.io/gorm"

	"media-downloader-bot/internal/config"
	"media-downloader-bot/internal/extractor"
	"media-downloader-bot/internal/models"
	"media-downloader-bot/internal/queue"
)

type Bot struct {
	bot       *tele.Bot
	cfg       *config.Config
	db        *gorm.DB
	registry  ExtractorRegistry
	taskQueue TaskQueue
	mu        sync.Mutex
	active    map[uint]activeTask
	stopping  bool
}

type ExtractorRegistry interface {
	Download(context.Context, string, string, bool, extractor.ProgressFunc) (*extractor.DownloadResult, error)
}
type TaskQueue interface {
	Enqueue(queue.DownloadRequest) error
	Stats() (int, int, error)
}
type activeTask struct {
	userID, chatID int64
	cancel         context.CancelFunc
}

func NewBot(cfg *config.Config, db *gorm.DB, registry ExtractorRegistry, taskQueue TaskQueue) (*Bot, error) {
	pref := tele.Settings{
		Token:  cfg.Telegram.BotToken,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
		Client: &http.Client{Timeout: 2 * time.Minute},
		OnError: func(err error, c tele.Context) {
			logrus.Errorf("Bot error: %s", strings.ReplaceAll(err.Error(), cfg.Telegram.BotToken, "[redacted]"))
		},
	}

	if cfg.Telegram.Webhook.Enabled {
		pref.Poller = &tele.Webhook{
			Listen: ":" + strconv.Itoa(cfg.Telegram.Webhook.Port),
			Endpoint: &tele.WebhookEndpoint{
				PublicURL: cfg.Telegram.Webhook.Host + cfg.Telegram.Webhook.Path,
			},
		}
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("Telegram initialization: %s", strings.ReplaceAll(err.Error(), cfg.Telegram.BotToken, "[redacted]"))
	}
	if !cfg.Telegram.Webhook.Enabled {
		if err := b.RemoveWebhook(); err != nil {
			return nil, fmt.Errorf("remove webhook: %s", strings.ReplaceAll(err.Error(), cfg.Telegram.BotToken, "[redacted]"))
		}
	}

	bot := &Bot{
		bot:       b,
		cfg:       cfg,
		db:        db,
		registry:  registry,
		taskQueue: taskQueue,
		active:    make(map[uint]activeTask),
	}

	bot.registerHandlers()
	return bot, nil
}

func (b *Bot) registerHandlers() {
	// Commands
	b.bot.Handle("/start", b.handleStart)
	b.bot.Handle("/help", b.handleHelp)
	b.bot.Handle("/status", b.handleStatus)
	b.bot.Handle("/cancel", b.handleCancel)
	b.bot.Handle("/queue", b.handleQueue)

	// Admin commands
	b.bot.Handle("/stats", b.adminOnly(b.handleStats))
	b.bot.Handle("/broadcast", b.adminOnly(b.handleBroadcast))

	// URL handling
	b.bot.Handle(tele.OnText, b.handleText)

	// Callback queries (inline buttons)
	b.bot.Handle(tele.OnCallback, b.handleCallback)
}

func (b *Bot) Start() {
	logrus.Info("Starting bot...")

	b.bot.Start()
}

func (b *Bot) Stop() {
	logrus.Info("Stopping bot...")
	b.bot.Stop()

	b.CancelActive()
	logrus.Info("Bot stopped")
}

func (b *Bot) handleStart(c tele.Context) error {
	return c.Send("?? Media Downloader Bot\n\n?????? ?????? ?? YouTube, TikTok, Instagram, Twitter/X, SoundCloud ??? ?????????.\n????? ???????? ????????: 720p, 1080p, best ??? audio.\n\n/help ? ???????\n/status ? ????????? ????????\n/cancel ? ???????? ???? ????????\n/queue ? ???????")
}

func (b *Bot) handleHelp(c tele.Context) error {
	return c.Send(fmt.Sprintf("?????? ???? ?????? ?? ??????.\n\n???????:\nhttps://youtu.be/VIDEO 720p\nhttps://youtu.be/VIDEO audio\n\n???????????? ??????: %d ??.\n????????????? ????????: %d.\n???????: %d ???.\n????????? ??????? ?? ???????????. ??? ???????? ?????????? ????? ??????????? ??????????? ?? ?????????.",
		b.cfg.App.MaxFileSizeMB, b.cfg.Downloader.MaxConcurrent, b.cfg.Downloader.TimeoutMinutes))
}

func (b *Bot) handleText(c tele.Context) error {
	text := c.Text()
	chatID := c.Chat().ID

	// Parse URLs and quality hints
	lines := strings.Split(text, "\n")
	var tasks []queue.DownloadRequest

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		url := parts[0]
		quality := "best"
		audioOnly := false

		if len(parts) > 1 {
			q := strings.ToLower(parts[1])
			if q == "audio" || q == "mp3" {
				audioOnly = true
				quality = "audio"
			} else {
				quality = q
			}
		}

		// Basic URL validation
		if extractor.ValidateURL(url) != nil {
			continue
		}

		tasks = append(tasks, queue.DownloadRequest{
			UserID:    c.Sender().ID,
			ChatID:    chatID,
			MessageID: c.Message().ID,
			URL:       url,
			Quality:   quality,
			AudioOnly: audioOnly,
		})
	}

	if len(tasks) == 0 {
		return c.Reply("❌ Не нашел ссылок в сообщении. Пришли ссылку на видео/аудио.")
	}

	accepted := 0
	// Persist before enqueue so pending tasks can be cancelled and survive restarts.
	for _, task := range tasks {
		t := &models.DownloadTask{UserID: task.UserID, ChatID: task.ChatID, MessageID: task.MessageID,
			URL: task.URL, Quality: task.Quality, Format: map[bool]string{true: "audio", false: "video"}[task.AudioOnly], Status: models.StatusPending}
		if err := b.db.Create(t).Error; err != nil {
			logrus.Error(err)
			continue
		}
		task.TaskID = t.ID
		if err := b.taskQueue.Enqueue(task); err != nil {
			b.db.Model(t).Updates(map[string]interface{}{"status": models.StatusFailed, "error": "Queue unavailable"})
			logrus.Errorf("Failed to enqueue task: %v", err)
			c.Reply(fmt.Sprintf("❌ Ошибка добавления в очередь: %v", err))
			continue
		}
		accepted++
	}

	if accepted == 0 {
		return c.Reply("❌ Не удалось добавить задачи в очередь.")
	}
	if accepted == 1 {
		return c.Reply("✅ Добавлено в очередь. Ожидайте...")
	}
	return c.Reply(fmt.Sprintf("✅ Добавлено %d задач в очередь.", accepted))
}

func (b *Bot) handleCallback(c tele.Context) error {
	data := c.Callback().Data

	switch {
	case strings.HasPrefix(data, "cancel:"):
		taskID := strings.TrimPrefix(data, "cancel:")
		id, _ := strconv.ParseUint(taskID, 10, 32)
		return b.handleCancelTask(c, uint(id))

	case strings.HasPrefix(data, "retry:"):
		taskID := strings.TrimPrefix(data, "retry:")
		id, _ := strconv.ParseUint(taskID, 10, 32)
		return b.handleRetryTask(c, uint(id))

	case strings.HasPrefix(data, "quality:"):
		// quality:taskID:quality
		parts := strings.Split(data, ":")
		if len(parts) == 3 {
			id, _ := strconv.ParseUint(parts[1], 10, 32)
			return b.handleQualitySelect(c, uint(id), parts[2])
		}
	}

	return c.Respond()
}

func (b *Bot) handleCancelTask(c tele.Context, taskID uint) error {
	task, err := b.getTask(taskID)
	if err != nil {
		return c.Respond(&tele.CallbackResponse{Text: "Задача не найдена", ShowAlert: true})
	}

	if task.UserID != c.Sender().ID && !b.isAdmin(c.Sender().ID) {
		return c.Respond(&tele.CallbackResponse{Text: "Не твоя задача", ShowAlert: true})
	}

	b.cancelTask(task)
	return c.Respond(&tele.CallbackResponse{Text: "Отменено", ShowAlert: false})
}

func (b *Bot) handleRetryTask(c tele.Context, taskID uint) error {
	task, err := b.getTask(taskID)
	if err != nil {
		return c.Respond(&tele.CallbackResponse{Text: "Задача не найдена", ShowAlert: true})
	}

	if task.UserID != c.Sender().ID && !b.isAdmin(c.Sender().ID) {
		return c.Respond(&tele.CallbackResponse{Text: "Не твоя задача", ShowAlert: true})
	}

	if task.Status != models.StatusFailed && task.Status != models.StatusDone {
		return c.Respond(&tele.CallbackResponse{Text: "Задача уже выполняется"})
	}
	task.ID = 0
	task.Status = models.StatusPending
	task.Error = ""
	task.RetryCount++
	if err := b.db.Create(task).Error; err != nil {
		return err
	}

	if err := b.taskQueue.Enqueue(queue.DownloadRequest{
		TaskID:    task.ID,
		UserID:    task.UserID,
		ChatID:    task.ChatID,
		MessageID: task.MessageID,
		URL:       task.URL,
		Quality:   task.Quality,
		AudioOnly: task.Format == "audio",
	}); err != nil {
		b.db.Model(task).Updates(map[string]interface{}{"status": models.StatusFailed, "error": "Queue unavailable"})
		return err
	}

	return c.Respond(&tele.CallbackResponse{Text: "В очереди", ShowAlert: false})
}

func (b *Bot) handleQualitySelect(c tele.Context, taskID uint, quality string) error {
	task, err := b.getTask(taskID)
	if err != nil {
		return c.Respond(&tele.CallbackResponse{Text: "Задача не найдена", ShowAlert: true})
	}

	if task.UserID != c.Sender().ID && !b.isAdmin(c.Sender().ID) {
		return c.Respond(&tele.CallbackResponse{Text: "Не твоя задача", ShowAlert: true})
	}

	if task.Status != models.StatusPending {
		return c.Respond(&tele.CallbackResponse{Text: "Уже в процессе", ShowAlert: true})
	}

	if quality != "best" && quality != "audio" && parseQualityHeight(quality) == 0 {
		return c.Respond(&tele.CallbackResponse{Text: "Неизвестное качество"})
	}
	if err := b.db.Model(task).Where("status = ?", models.StatusPending).Update("quality", quality).Error; err != nil {
		return err
	}

	return c.Respond(&tele.CallbackResponse{Text: "Качество: " + quality, ShowAlert: false})
}

func (b *Bot) handleStatus(c tele.Context) error {
	chatID := c.Chat().ID
	userID := c.Sender().ID

	// Check recent tasks from DB
	var tasks []models.DownloadTask
	b.db.Where("chat_id = ? AND user_id = ?", chatID, userID).
		Order("created_at DESC").Limit(5).Find(&tasks)

	if len(tasks) == 0 {
		return c.Reply("📭 Нет активных или недавних загрузок.")
	}

	text := "📋 *Последние загрузки:*\n\n"
	for _, t := range tasks {
		text += b.formatTaskStatus(&t) + "\n"
	}

	return c.Send(text)
}

func (b *Bot) handleCancel(c tele.Context) error {
	var tasks []models.DownloadTask
	if err := b.db.Where("chat_id = ? AND user_id = ? AND status IN ?", c.Chat().ID, c.Sender().ID,
		[]string{models.StatusPending, models.StatusDownloading, models.StatusProcessing}).Find(&tasks).Error; err != nil {
		return err
	}
	for i := range tasks {
		b.cancelTask(&tasks[i])
	}
	return c.Reply(fmt.Sprintf("Отменено загрузок: %d", len(tasks)))
}

func (b *Bot) handleQueue(c tele.Context) error {
	// Show queue status from Redis
	pending, processing, err := b.taskQueue.Stats()
	if err != nil {
		return c.Reply("❌ Ошибка получения очереди")
	}

	text := fmt.Sprintf("📊 *Очередь загрузок:*\n\n⏳ В очереди: %d\n⚙️ В процессе: %d\n\n", pending, processing)

	// Show user's tasks in queue
	userID := c.Sender().ID
	var tasks []models.DownloadTask
	b.db.Where("user_id = ? AND status IN ?", userID, []string{models.StatusPending, models.StatusDownloading}).
		Order("created_at").Limit(10).Find(&tasks)

	if len(tasks) > 0 {
		text += "*Твои задачи:*\n"
		for _, t := range tasks {
			text += b.formatTaskStatus(&t) + "\n"
		}
	}

	return c.Send(text)
}

// Admin handlers
func (b *Bot) adminOnly(next tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		if !b.isAdmin(c.Sender().ID) {
			return c.Reply("🚫 Доступно только администраторам")
		}
		return next(c)
	}
}

func (b *Bot) isAdmin(userID int64) bool {
	for _, id := range b.cfg.Telegram.AdminIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func (b *Bot) handleStats(c tele.Context) error {
	var stats struct {
		TotalTasks  int64
		DoneTasks   int64
		FailedTasks int64
		UniqueUsers int64
		TotalSizeMB int64
	}

	b.db.Model(&models.DownloadTask{}).Count(&stats.TotalTasks)
	b.db.Model(&models.DownloadTask{}).Where("status = ?", models.StatusDone).Count(&stats.DoneTasks)
	b.db.Model(&models.DownloadTask{}).Where("status = ?", models.StatusFailed).Count(&stats.FailedTasks)
	b.db.Model(&models.DownloadTask{}).Distinct("user_id").Count(&stats.UniqueUsers)
	b.db.Model(&models.DownloadTask{}).Where("status = ?", models.StatusDone).Select("COALESCE(SUM(file_size), 0)").Scan(&stats.TotalSizeMB)

	text := fmt.Sprintf(`📊 *Статистика бота*

📥 Всего задач: %d
✅ Успешно: %d
❌ Ошибки: %d
👥 Уникальных пользователей: %d
💾 Скачано: %.2f GB

🔧 *Настройки:*
• Макс. одновременных: %d
• Макс. размер файла: %d MB
• Таймаут: %d мин`,
		stats.TotalTasks, stats.DoneTasks, stats.FailedTasks, stats.UniqueUsers,
		float64(stats.TotalSizeMB)/(1024*1024*1024),
		b.cfg.Downloader.MaxConcurrent, b.cfg.App.MaxFileSizeMB, b.cfg.Downloader.TimeoutMinutes)

	return c.Send(text)
}

func (b *Bot) handleBroadcast(c tele.Context) error {
	// /broadcast <message>
	args := strings.Fields(c.Text())
	if len(args) < 2 {
		return c.Reply("Использование: /broadcast <сообщение>")
	}
	msg := strings.Join(args[1:], " ")

	var users []models.DownloadTask
	b.db.Model(&models.DownloadTask{}).Distinct("chat_id").Find(&users)

	sent := 0
	for _, u := range users {
		_, err := b.bot.Send(&tele.Chat{ID: u.ChatID}, msg, tele.ModeMarkdown)
		if err == nil {
			sent++
		}
		time.Sleep(100 * time.Millisecond) // rate limit
	}

	return c.Reply(fmt.Sprintf("📢 Рассылка отправлена %d пользователям", sent))
}

// Task processing
// ProcessTask is public so workers can call it
func (b *Bot) ProcessTask(task *models.DownloadTask) (processErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(b.cfg.Downloader.TimeoutMinutes)*time.Minute)
	b.mu.Lock()
	if b.stopping {
		b.mu.Unlock()
		cancel()
		return context.Canceled
	}
	// Share the cancellation lock while checking persisted state: /cancel may
	// have cancelled this pending task after the worker fetched it from SQLite.
	var persisted models.DownloadTask
	if err := b.db.First(&persisted, task.ID).Error; err != nil {
		b.mu.Unlock()
		cancel()
		return err
	}
	if persisted.Status == models.StatusFailed || persisted.Status == models.StatusDone {
		b.mu.Unlock()
		cancel()
		return nil
	}
	b.active[task.ID] = activeTask{userID: task.UserID, chatID: task.ChatID, cancel: cancel}
	b.mu.Unlock()
	defer func() {
		cancel()
		b.mu.Lock()
		delete(b.active, task.ID)
		if b.stopping {
			processErr = context.Canceled
		}
		b.mu.Unlock()
	}()

	// Update status
	b.updateTaskStatus(task, models.StatusDownloading, 0, "")

	// Progress callback
	progress := func(downloaded, total int64, speed float64, eta time.Duration) {
		if total > 0 {
			pct := int(float64(downloaded) / float64(total) * 100)
			b.updateTaskStatus(task, models.StatusDownloading, pct, "")
		}
	}

	// Download
	result, err := b.registry.Download(ctx, task.URL, task.Quality, task.Format == "audio", progress)
	if err != nil {
		b.handleError(task, err)
		return
	}
	if result == nil {
		b.handleError(task, fmt.Errorf("download returned no file"))
		return
	}
	defer b.cleanupFile(result.FilePath)
	if ctx.Err() != nil {
		b.handleError(task, ctx.Err())
		return
	}
	var current models.DownloadTask
	if err := b.db.First(&current, task.ID).Error; err != nil {
		b.handleError(task, err)
		return
	}
	if current.Status == models.StatusFailed {
		return
	}

	// Update with file info
	task.FilePath = result.FilePath
	task.FileSize = result.FileSize
	task.Duration = result.Duration
	task.Title = result.Title
	b.updateTaskStatus(task, models.StatusUploading, 100, "")

	// Send file
	if err := b.sendFile(task, result); err != nil {
		b.handleError(task, err)
		return
	}

	// Success
	b.updateTaskStatus(task, models.StatusDone, 100, "")
	return
}

func (b *Bot) sendFile(task *models.DownloadTask, result *extractor.DownloadResult) error {
	filePath := result.FilePath
	fileSize := result.FileSize

	// Check file size limit
	maxSize := int64(b.cfg.App.MaxFileSizeMB) * 1024 * 1024
	if fileSize > maxSize {
		return fmt.Errorf("file too large: %d MB > %d MB limit", fileSize/(1024*1024), b.cfg.App.MaxFileSizeMB)
	}

	// Caption
	caption := fmt.Sprintf("✅ %s\n📥 %s\n📊 %s\n⏱ %s",
		truncate(result.Title, 500),
		formatBytes(fileSize),
		result.Format.Quality,
		formatDuration(result.Duration))

	// Send based on type
	var sent *tele.Message
	var err error

	if result.Format.IsVideo && result.Format.Ext == "mp4" {
		video := &tele.Video{File: tele.FromDisk(filePath), Caption: caption}
		sent, err = b.bot.Send(&tele.Chat{ID: task.ChatID}, video)
	} else if result.Format.IsAudio {
		audio := &tele.Audio{File: tele.FromDisk(filePath), Caption: caption, Title: result.Title}
		sent, err = b.bot.Send(&tele.Chat{ID: task.ChatID}, audio)
	} else {
		doc := &tele.Document{File: tele.FromDisk(filePath), FileName: filepath.Base(filePath), Caption: caption}
		sent, err = b.bot.Send(&tele.Chat{ID: task.ChatID}, doc)
	}

	if err != nil {
		return err
	}

	// Update message ID for status edits
	task.MessageID = sent.ID
	b.db.Model(task).Update("message_id", task.MessageID)
	return nil
}

func (b *Bot) handleError(task *models.DownloadTask, err error) {
	if b.cfg.Telegram.BotToken != "" {
		err = fmt.Errorf("%s", strings.ReplaceAll(err.Error(), b.cfg.Telegram.BotToken, "[redacted]"))
	}
	b.mu.Lock()
	stopping := b.stopping
	b.mu.Unlock()
	if stopping {
		return
	}
	var current models.DownloadTask
	if b.db.First(&current, task.ID).Error == nil && current.Error == "Cancelled by user" {
		return
	}
	logrus.Errorf("Task %d failed: %v", task.ID, err)
	task.Error = err.Error()
	b.updateTaskStatus(task, models.StatusFailed, 0, err.Error())

	// Send error to user with retry button
	btn := tele.InlineButton{Text: "🔄 Повторить", Data: fmt.Sprintf("retry:%d", task.ID)}
	markup := &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{{btn}}}

	b.bot.Send(&tele.Chat{ID: task.ChatID},
		fmt.Sprintf("❌ Ошибка загрузки\n\n%s\n\n%s", truncate(task.Title, 200), truncate(err.Error(), 1500)), markup)
}

func (b *Bot) cancelTask(task *models.DownloadTask) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.db.Model(&models.DownloadTask{}).Where("id = ? AND status IN ?", task.ID,
		[]string{models.StatusPending, models.StatusDownloading, models.StatusProcessing}).Updates(map[string]interface{}{"status": models.StatusFailed, "error": "Cancelled by user"})
	if active, ok := b.active[task.ID]; ok {
		active.cancel()
	}
}

func (b *Bot) updateTaskStatus(task *models.DownloadTask, status string, progress int, error string) {
	task.Status = status
	task.Progress = progress
	if error != "" {
		task.Error = error
	}
	b.db.Model(&models.DownloadTask{}).Where("id = ? AND status != ?", task.ID, models.StatusFailed).
		Updates(map[string]interface{}{"status": status, "progress": progress, "error": task.Error,
			"title": task.Title, "file_path": task.FilePath, "file_size": task.FileSize, "duration": task.Duration, "platform": task.Platform})
}

func (b *Bot) getTask(id uint) (*models.DownloadTask, error) {
	var task models.DownloadTask
	if err := b.db.First(&task, id).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

func (b *Bot) sendTaskStatus(c tele.Context, task *models.DownloadTask) error {
	text := b.formatTaskStatus(task)

	var markup *tele.ReplyMarkup
	if task.Status == models.StatusDownloading || task.Status == models.StatusProcessing {
		btn := tele.InlineButton{Text: "❌ Отменить", Data: fmt.Sprintf("cancel:%d", task.ID)}
		markup = &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{{btn}}}
	} else if task.Status == models.StatusFailed {
		btn := tele.InlineButton{Text: "🔄 Повторить", Data: fmt.Sprintf("retry:%d", task.ID)}
		markup = &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{{btn}}}
	}

	return c.Send(text, markup)
}

func (b *Bot) formatTaskStatus(task *models.DownloadTask) string {
	statusIcon := map[string]string{
		models.StatusPending:     "⏳",
		models.StatusDownloading: "⬇️",
		models.StatusProcessing:  "⚙️",
		models.StatusUploading:   "⬆️",
		models.StatusDone:        "✅",
		models.StatusFailed:      "❌",
	}[task.Status]

	platformIcon := map[string]string{
		"youtube":    "🔴",
		"tiktok":     "🎵",
		"instagram":  "📷",
		"twitter":    "🐦",
		"soundcloud": "🎧",
		"generic":    "🔗",
	}[task.Platform]

	progressBar := ""
	if task.Status == models.StatusDownloading {
		p := task.Progress
		if p < 0 {
			p = 0
		}
		if p > 100 {
			p = 100
		}
		filled := p / 10
		progressBar = fmt.Sprintf(" [%s%s] %d%%", strings.Repeat("█", filled), strings.Repeat("░", 10-filled), p)
	}

	title := truncate(task.Title, 50)

	return fmt.Sprintf("%s %s %s%s\n🔗 %s", statusIcon, platformIcon, title, progressBar, task.URL)
}

func (b *Bot) cleanupFile(path string) {
	if path != "" {
		os.Remove(path)
		if strings.HasPrefix(filepath.Base(filepath.Dir(path)), "job_") {
			_ = os.RemoveAll(filepath.Dir(path))
		}
	}
}

func truncate(text string, max int) string {
	r := []rune(text)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return text
}

func parseQualityHeight(q string) int {
	h, _ := strconv.Atoi(strings.TrimSuffix(q, "p"))
	if h < 144 || h > 4320 {
		return 0
	}
	return h
}

func (b *Bot) CancelActive() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopping = true
	for _, task := range b.active {
		task.cancel()
	}
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func formatDuration(seconds int) string {
	if seconds <= 0 {
		return "N/A"
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
