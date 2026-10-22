package bot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	gorm "gorm.io/gorm"
	tele "gopkg.in/telebot.v3"

	"media-downloader-bot/internal/config"
	"media-downloader-bot/internal/extractor"
	"media-downloader-bot/internal/models"
	"media-downloader-bot/internal/queue"
)

type Bot struct {
	bot       *tele.Bot
	cfg       *config.Config
	db        *gorm.DB
	registry  *extractor.Registry
	taskQueue *queue.TaskQueue
	active    map[int64]*models.DownloadTask // chatID -> current task
}

func NewBot(cfg *config.Config, db *gorm.DB, registry *extractor.Registry, taskQueue *queue.TaskQueue) (*Bot, error) {
	pref := tele.Settings{
		Token:  cfg.Telegram.BotToken,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
		OnError: func(err error, c tele.Context) {
			logrus.Errorf("Bot error: %v", err)
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
		return nil, err
	}

	bot := &Bot{
		bot:       b,
		cfg:       cfg,
		db:        db,
		registry:  registry,
		taskQueue: taskQueue,
		active:    make(map[int64]*models.DownloadTask),
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
	
	// Add health check endpoint if webhook mode
	if b.cfg.Telegram.Webhook.Enabled {
		b.bot.Handle("/health", func(c tele.Context) error {
			return c.Send("OK")
		})
	}
	
	b.bot.Start()
}

func (b *Bot) Stop() {
	logrus.Info("Stopping bot...")
	b.bot.Stop()
	
	// Cancel all active tasks
	for _, task := range b.active {
		b.cancelTask(task)
	}
	
	// Wait a bit for cleanup
	time.Sleep(2 * time.Second)
	logrus.Info("Bot stopped")
}

func (b *Bot) handleStart(c tele.Context) error {
	user := c.Sender()
	logrus.Infof("User %d (@%s) started bot", user.ID, user.Username)

	text := `🎬 *Media Downloader Bot*

Пришли мне ссылку на видео/аудио с популярных платформ, и я скачаю его для тебя.

📋 *Поддерживаемые платформы:*
• YouTube (видео, плейлисты, Shorts, субтитры)
• TikTok (без водяного знака)
• Instagram (Reels, посты, IGTV)
• Twitter/X (видео, GIF)
• SoundCloud (аудио)
• Прямые ссылки на медиафайлы

⚙️ *Команды:*
/start — это сообщение
/help — подробная помощь
/status — статус текущей загрузки
/cancel — отменить загрузку
/queue — показать очередь

💡 *Советы:*
• Можно присылать несколько ссылок сразу — они встанут в очередь
• Для YouTube можно указать качество: \`ссылка 720p\` или \`ссылка audio\`
• Макс. размер файла: ` + fmt.Sprintf("%d МБ", b.cfg.App.MaxFileSizeMB) + `

🔗 *Примеры:*
\`https://youtu.be/dQw4w9WgXcQ\`
\`https://www.tiktok.com/@user/video/1234567890\`
\`https://www.instagram.com/reel/ABC123/\``

	return c.Send(text, tele.ModeMarkdown)
}

func (b *Bot) handleHelp(c tele.Context) error {
	text := `📖 *Подробная помощь*

*Как пользоваться:*
1. Просто пришли ссылку — бот сам определит платформу и качество
2. Для выбора качества добавь после ссылки: \`720p\`, \`1080p\`, \`audio\`, \`best\`
3. Несколько ссылок можно прислать за раз (каждая с новой строки)

*Примеры:*
\`https://youtu.be/xxx 720p\` — видео 720p
\`https://youtu.be/xxx audio\` — только аудио (mp3)
\`https://youtu.be/xxx best\` — лучшее качество (по умолчанию)

*Ограничения:*
• Макс. размер файла: ` + fmt.Sprintf("%d МБ", b.cfg.App.MaxFileSizeMB) + ` (лимит Telegram)
• Одновременных загрузок: ` + fmt.Sprintf("%d", b.cfg.Downloader.MaxConcurrent) + `
• Таймаут загрузки: ` + fmt.Sprintf("%d мин", b.cfg.Downloader.TimeoutMinutes) + `

*Поддерживаемые форматы:*
• Видео: MP4, WebM, MKV
• Аудио: MP3, M4A, OGG
• Субтитры: SRT, VTT (встроены в видео при выборе)

*Проблемы?*
• Если видео не скачивается — попробуй позже (платформы меняют API)
• Приватные/возрастные видео могут не работать
• Для Instagram/TikTok/Twitter используется браузер — может быть медленнее

/start — главное меню
/status — проверить статус
/cancel — отменить текущую загрузку`

	return c.Send(text, tele.ModeMarkdown)
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
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
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

	// Add to queue
	for _, task := range tasks {
		if err := b.taskQueue.Enqueue(task); err != nil {
			logrus.Errorf("Failed to enqueue task: %v", err)
			c.Reply(fmt.Sprintf("❌ Ошибка добавления в очередь: %v", err))
			continue
		}
	}

	if len(tasks) == 1 {
		return c.Reply("✅ Добавлено в очередь. Ожидайте...")
	}
	return c.Reply(fmt.Sprintf("✅ Добавлено %d задач в очередь.", len(tasks)))
}

func (b *Bot) handleCallback(c tele.Context) error {
	data := c.Callback().Data
	chatID := c.Chat().ID

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

	// Reset status and re-queue
	task.Status = models.StatusPending
	task.Error = ""
	task.RetryCount++
	b.db.Save(task)

	b.taskQueue.Enqueue(queue.DownloadRequest{
		UserID:    task.UserID,
		ChatID:    task.ChatID,
		MessageID: task.MessageID,
		URL:       task.URL,
		Quality:   task.Quality,
		AudioOnly: task.Format == "audio",
	})

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

	task.Quality = quality
	b.db.Save(task)

	return c.Respond(&tele.CallbackResponse{Text: "Качество: " + quality, ShowAlert: false})
}

func (b *Bot) handleStatus(c tele.Context) error {
	chatID := c.Chat().ID
	userID := c.Sender().ID

	// Check active task
	if task, ok := b.active[chatID]; ok {
		return b.sendTaskStatus(c, task)
	}

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

	return c.Send(text, tele.ModeMarkdown)
}

func (b *Bot) handleCancel(c tele.Context) error {
	chatID := c.Chat().ID

	if task, ok := b.active[chatID]; ok {
		b.cancelTask(task)
		return c.Reply("❌ Текущая загрузка отменена.")
	}

	return c.Reply("📭 Нет активной загрузки для отмены.")
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

	return c.Send(text, tele.ModeMarkdown)
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
		TotalTasks    int64
		DoneTasks     int64
		FailedTasks   int64
		UniqueUsers   int64
		TotalSizeMB   int64
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

	return c.Send(text, tele.ModeMarkdown)
}

func (b *Bot) handleBroadcast(c tele.Context) error {
	// /broadcast <message>
	args := strings.Fields(c.Text())
	if len(args) < 2 {
		return c.Reply("Использование: /broadcast <сообщение>")
	}
	msg := strings.Join(args[1:], " ")

	var users []models.DownloadTask
	b.db.Model(&models.DownloadTask{}).Distinct("user_id, chat_id").Find(&users)

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
func (b *Bot) ProcessTask(task *models.DownloadTask) {
	chatID := task.ChatID
	b.active[chatID] = task
	defer delete(b.active, chatID)

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
	ctx := context.Background()
	result, err := b.registry.Download(ctx, task.URL, task.Quality, task.Format == "audio", progress)
	if err != nil {
		b.handleError(task, err)
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
	b.cleanupFile(result.FilePath)
}

func (b *Bot) sendFile(task *models.DownloadTask, result *extractor.DownloadResult) error {
	filePath := result.FilePath
	fileSize := result.FileSize

	// Check file size limit
	maxSize := int64(b.cfg.App.MaxFileSizeMB) * 1024 * 1024
	if fileSize > maxSize {
		return fmt.Errorf("file too large: %d MB > %d MB limit", fileSize/(1024*1024), b.cfg.App.MaxFileSizeMB)
	}

	// Prepare file
	file := &tele.Document{File: tele.FromDisk(filePath), FileName: filepath.Base(filePath)}

	// Caption
	caption := fmt.Sprintf("✅ *%s*\n📥 %s\n📊 %s\n⏱ %s",
		result.Title,
		formatBytes(fileSize),
		result.Format.Quality,
		formatDuration(result.Duration))

	// Send based on type
	var sent *tele.Message
	var err error

	if result.Format.IsVideo {
		video := &tele.Video{File: tele.FromDisk(filePath), FileName: filepath.Base(filePath), Caption: caption}
		sent, err = b.bot.Send(&tele.Chat{ID: task.ChatID}, video)
	} else if result.Format.IsAudio {
		audio := &tele.Audio{File: tele.FromDisk(filePath), FileName: filepath.Base(filePath), Caption: caption, Title: result.Title}
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
	b.db.Save(task)
	return nil
}

func (b *Bot) handleError(task *models.DownloadTask, err error) {
	logrus.Errorf("Task %d failed: %v", task.ID, err)
	task.Error = err.Error()
	b.updateTaskStatus(task, models.StatusFailed, 0, err.Error())

	// Send error to user with retry button
	btn := tele.Btn{Text: "🔄 Повторить", Data: fmt.Sprintf("retry:%d", task.ID)}
	markup := &tele.ReplyMarkup{InlineKeyboard: [][]tele.Btn{{btn}}}

	b.bot.Send(&tele.Chat{ID: task.ChatID},
		fmt.Sprintf("❌ *Ошибка загрузки*\n\n%s\n\n%s", task.Title, err.Error()),
		tele.ModeMarkdown, markup)
}

func (b *Bot) cancelTask(task *models.DownloadTask) {
	task.Status = models.StatusFailed
	task.Error = "Cancelled by user"
	b.db.Save(task)
	b.bot.Send(&tele.Chat{ID: task.ChatID}, "❌ Загрузка отменена пользователем")
}

func (b *Bot) updateTaskStatus(task *models.DownloadTask, status string, progress int, error string) {
	task.Status = status
	task.Progress = progress
	if error != "" {
		task.Error = error
	}
	b.db.Save(task)
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
		btn := tele.Btn{Text: "❌ Отменить", Data: fmt.Sprintf("cancel:%d", task.ID)}
		markup = &tele.ReplyMarkup{InlineKeyboard: [][]tele.Btn{{btn}}}
	} else if task.Status == models.StatusFailed {
		btn := tele.Btn{Text: "🔄 Повторить", Data: fmt.Sprintf("retry:%d", task.ID)}
		markup = &tele.ReplyMarkup{InlineKeyboard: [][]tele.Btn{{btn}}}
	}

	return c.Send(text, tele.ModeMarkdown, markup)
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
		"youtube":   "🔴",
		"tiktok":    "🎵",
		"instagram": "📷",
		"twitter":   "🐦",
		"soundcloud": "🎧",
		"generic":   "🔗",
	}[task.Platform]

	progressBar := ""
	if task.Status == models.StatusDownloading {
		p := task.Progress
		filled := p / 10
		progressBar = fmt.Sprintf(" [%s%s] %d%%", strings.Repeat("█", filled), strings.Repeat("░", 10-filled), p)
	}

	title := task.Title
	if len(title) > 50 {
		title = title[:47] + "..."
	}

	return fmt.Sprintf("%s %s *%s*%s\n🔗 `%s`", statusIcon, platformIcon, title, progressBar, task.URL)
}

func (b *Bot) cleanupFile(path string) {
	if path != "" {
		os.Remove(path)
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