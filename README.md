# Media Downloader Bot

Telegram-бот на Go для загрузки медиа с YouTube, TikTok, Instagram, Twitter/X, SoundCloud и прямых HTTP(S)-ссылок. Экстракторы платформ используют yt-dlp и FFmpeg. Закрытые материалы и ограничения платформ могут требовать отдельной авторизации; целые плейлисты не загружаются.

![Иконка бота](assets/bot-avatar.png)

## Запуск

Нужны Docker Engine и Docker Compose v2.

```sh
cp .env.example .env
# Укажите BOT_TOKEN в .env.
docker compose up -d --build --wait
docker compose logs -f bot
curl --fail http://127.0.0.1:8080/health
```

Токен хранится в игнорируемом .env. Публичный Telegram Bot API допускает загрузку файлов до 50 МБ; Premium пользователя этот лимит не увеличивает. Параметры MAX_FILE_SIZE_MB, DOWNLOADER_MAX_CONCURRENT и DOWNLOADER_TIMEOUT_MINUTES задаются в .env. Webhook пока не поддерживается: используется long polling. Health endpoint доступен только на loopback сервера.

Отправьте боту одну ссылку на строку:

```text
https://youtu.be/VIDEO_ID 720p
https://youtu.be/VIDEO_ID audio
https://example.org/video.mp4
```

Команды: /start, /help, /status, /cancel, /queue. /stats и /broadcast доступны только ADMIN_IDS. /cancel отменяет свои ожидающие и скачиваемые задачи в текущем чате; начавшуюся отправку файла в Telegram отменить нельзя.

## Разработка и Git flow

- master — production, каждый push после успешного CI запускает деплой.
- develop — интеграция изменений.
- feature/* — задачи от develop, pull request в develop.
- release/* — подготовка релиза из develop, pull request в master; затем синхронизация develop.
- hotfix/* — срочные исправления от master, затем перенос в develop.

```sh
go mod download
go vet ./...
go test -race -count=1 -timeout=120s ./...
go build ./cmd/bot
```

Для SQLite нужен CGO и C-компилятор. Для интеграционных тестов очереди Redis должен слушать localhost:6379. В CI Redis запускается сервисом; без Redis локальные интеграционные тесты пропускаются.

## CI/CD

Workflow: [.github/workflows/ci.yml](.github/workflows/ci.yml).

CI проверяет форматирование, зависимости, go vet, тесты с детектором гонок и сборку. На push в master runner собирает Docker-образ с тегом SHA коммита, передаёт его по SSH и запускает Compose. Production job сериализован; сервер дополнительно использует flock.

Секреты GitHub Actions:

| Секрет | Содержимое |
| --- | --- |
| DEPLOY_HOST | IP сервера |
| DEPLOY_SSH_KEY | Отдельный приватный ключ деплоя |
| DEPLOY_KNOWN_HOSTS | Закреплённый SSH host key сервера |

На сервере: /opt/media-downloader/.env с правами 600; releases/ содержит версии, current указывает на успешный релиз. Redis и SQLite хранятся в именованных Docker volumes. Токен не передаётся в CI или Docker image.

SSH-ключ ограничен командой deploy <SHA> через [deploy/deploy-ssh.sh](deploy/deploy-ssh.sh). [deploy/deploy.sh](deploy/deploy.sh) загружает image, выполняет docker compose up --wait и проверяет /health. При ошибке возвращает предыдущий образ. Первый деплой не имеет предыдущего релиза для отката. Автоматический откат не откатывает схему SQLite: несовместимые миграции требуют отдельного плана.

## Иконка

[assets/bot-avatar.png](assets/bot-avatar.png) — аватар, созданный встроенным image_gen. Промпт сохранён в [assets/README.md](assets/README.md).

## Результаты ревью

Исправлены несуществующие зависимости и ошибки компиляции, пустые реализации скачивания, гонки карты задач, отмена без context cancellation, игнорирование таймаута, потеря порядка и дублирование записей очереди, зависание теста worker pool, утечки файлов, потеря метаданных и отсутствие HTTP healthcheck.

Выдача задач в Redis атомарно переносит их в processing. При запуске единственного экземпляра бота незавершённые задания возвращаются в очередь. Задачи сохраняются в SQLite до постановки в очередь; права на отмену проверяются по пользователю и чату. Доставка имеет семантику at least once: если процесс завершится после отправки файла, но до записи результата в SQLite, повторная отправка возможна.

Прямые загрузки проверяют DNS на каждом подключении и редиректе, запрещают приватные адреса, контролируют размер потока и удаляют неполные файлы. yt-dlp допускается только для перечисленных доменов платформ; сетевые запросы внутри yt-dlp и FFmpeg требуют доверия к самим платформам.
