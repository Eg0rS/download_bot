# Media Downloader Bot

Telegram-бот для скачивания медиа с популярных платформ: YouTube, TikTok, Instagram, Twitter/X, SoundCloud и прямых ссылок.

## 🚀 Возможности

| Платформа | Видео | Аудио | Субтитры | Особенности |
|-----------|-------|-------|----------|-------------|
| **YouTube** | ✅ | ✅ | ✅ | Плейлисты, Shorts,Live, выбор качества |
| **TikTok** | ✅ | ✅ | ❌ | Без водяного знака |
| **Instagram** | ✅ | ❌ | ❌ | Reels, посты, IGTV |
| **Twitter/X** | ✅ | ❌ | ❌ | Видео, GIF |
| **SoundCloud** | ❌ | ✅ | ❌ | Треки, плейлисты |
| **Прямые ссылки** | ✅ | ✅ | ❌ | Любые медиафайлы |

## 📦 Быстрый старт (Docker)

```bash
# 1. Клонируй репозиторий
git clone <repo>
cd media-downloader-bot

# 2. Настрой переменные окружения
cp .env.example .env
# Отредактируй .env — обязательно укажи BOT_TOKEN

# 3. Запусти
docker compose up -d

# 4. Смотри логи
docker compose logs -f bot
```

## ⚙️ Конфигурация

Основные настройки в `config.yaml` (перекрываются переменными из `.env`):

```yaml
app:
  max_file_size_mb: 2048  # Лимит Telegram Premium

telegram:
  bot_token: ""  # Из BOT_TOKEN
  admin_ids: []  # Из ADMIN_IDS

downloader:
  max_concurrent: 3          # Одновременных загрузок
  timeout_minutes: 30        # Таймаут одной загрузки

platforms:
  youtube:
    quality_preference: "best"  # best, 1080p, 720p, audio_only
    download_subtitles: true
  tiktok:
    watermark: false
```

## 🎮 Использование

Пришли боту ссылку:

```
https://youtu.be/dQw4w9WgXcQ
```

С указанием качества:

```
https://youtu.be/dQw4w9WgXcQ 720p
https://youtu.be/dQw4w9WgXcQ audio
```

Несколько ссылок сразу (каждая с новой строки):

```
https://youtu.be/xxx 1080p
https://tiktok.com/@user/video/123
https://instagram.com/reel/ABC/
```

## 🛠 Команды

| Команда | Описание |
|---------|----------|
| `/start` | Приветствие и справка |
| `/help` | Подробная помощь |
| `/status` | Статус текущей загрузки |
| `/cancel` | Отменить загрузку |
| `/queue` | Очередь загрузок |
| `/stats` | Статистика (админы) |
| `/broadcast` | Рассылка (админы) |

## 🏗 Архитектура

```
├── cmd/bot              # Точка входа
├── internal/
│   ├── bot/             # Telegram-бот (telebot.v3)
│   ├── config/          # Конфигурация (YAML + env)
│   ├── extractor/       # Экстракторы платформ
│   │   ├── youtube.go   # kkdai/youtube
│   │   ├── tiktok.go    # rod (headless Chrome)
│   │   ├── instagram.go # rod
│   │   ├── twitter.go   # rod
│   │   ├── generic.go   # Прямые ссылки (gen2brain/dl)
│   │   └── registry.go  # Реестр + диспетчер
│   ├── models/          # GORM модели (SQLite)
│   └── queue/           # Redis очередь + worker pool
├── Dockerfile           # Multi-stage build
├── docker-compose.yml   # Redis + Bot
├── config.yaml          # Конфиг по умолчанию
├── .env.example         # Пример переменных
└── Makefile             # Удобные команды
```

## 🔧 Разработка

```bash
# Зависимости
make deps

# Линт
make lint

# Тесты
make test

# Сборка
make build

# Запуск локально
make run
```

### Добавление новой платформы

1. Создай `internal/extractor/<platform>.go` реализуя интерфейс `Extractor`
2. Зарегистрируй в `main.go` при инициализации `registry`
3. Добавь конфиг в `config.yaml` и `internal/config/config.go`

## 📝 Лицензия

MIT — используй на свой страх и риск. Соблюдай ToS платформ и авторские права.

## ⚠️ Важные замечания

- **YouTube**: использует внутренний API, может ломаться при изменениях на стороне Google
- **TikTok/Instagram/Twitter**: требуют headless Chrome (Chromium в Docker) — медленнее и тяжелее
- **Размер файла**: Telegram Bot API ограничивает 50 МБ для ботов, 2 ГБ для Premium — бот проверяет лимит
- **Авторские права**: скачивай только разрешенный контент для личного использования