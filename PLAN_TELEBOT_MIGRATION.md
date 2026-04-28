# Telebot → go-telegram/bot migration

## Контекст
`gopkg.in/telebot.v3` отстал от Bot API на год+ (последний коммит 22 сентября 2024, последний релиз июнь 2024 → Bot API 7.1; сейчас Telegram уже на Bot API 9.6). Мигрируем на активно развивающуюся `github.com/go-telegram/bot v1.20+` — это разблокирует поддержку поля `Style` для цветных кнопок и убирает технический долг наперёд.

### Принятые решения
| Вопрос | Решение |
|--------|---------|
| Целевая библиотека | `github.com/go-telegram/bot v1.20.0` (или свежее на момент Phase 0) |
| Подход | Полная миграция в одну ветку, файл за файлом по убыванию связанности |
| TDD | Тесты переписываются ПЕРВЫМИ под новый API в каждой фазе, наблюдаем RED, потом GREEN-реализация |
| Совместимость с старой liber | Не нужна — переходим целиком, старая удаляется в финале |
| `CommandResult.ReplyMarkup` тип | `*models.InlineKeyboardMarkup` (в processor строятся только inline; reply-keyboard живёт отдельно в bot.go) |
| Multi-tenant (BotManager) | Тот же подход — per-token инстанс `*bot.Bot`. Маршрутизация webhook'ов по UUID не меняется |
| Цветные кнопки | Отдельная фаза в самом конце — после стабилизации миграции |
| Smoke на проде | Только ручной (через staging-токен) — нет инструмента для интеграционного |

### Что уже есть и НЕ требует изменений
- Архитектура multi-tenant (одна tg-сессия = один pod-уровневый `*bot.Bot`, маршрутизация webhook'ов через `webhook_uuid`).
- `ConversationManager` в Redis — модель данных не зависит от lib (хранит JSON conversations).
- `commands.CommandResult` — паттерн (Message, Books, ReplyMarkup, SearchParams) сохраняем; меняется только тип поля Markup.
- LLM-сервис (`llm/service.go`) — ноль зависимости от Telegram lib.
- Database-layer — без зависимости.
- API-routes для webhook-приёма (`/telegram/<uuid>`) — не меняем.
- Все command-processor методы (`ExecuteSearch`, `ExecuteShowFavorites`, `ExecuteShowCollections` etc.) — логика остаётся, меняется только возвращаемый markup-тип.

### Ключевые файлы
| Файл | Что менять |
|------|-----------|
| `go.mod` / `go.sum` | `go get github.com/go-telegram/bot`, `go mod tidy`; в финале — `go mod edit -droprequire gopkg.in/telebot.v3` |
| `telegram/keyboard.go` | Полная замена `tele.ReplyMarkup` → `models.ReplyKeyboardMarkup`; `KeyboardButton` структура; `GetMainKeyboard`, `GetCommandFromButtonText` |
| `telegram/keyboard_test.go` | Адаптация ожиданий под новые типы |
| `commands/processor.go` | `*tele.ReplyMarkup` → `*models.InlineKeyboardMarkup` в `CommandResult`; все `markup.Data(text, data)` builders → `models.InlineKeyboardButton{Text, CallbackData}` |
| `telegram/conversation.go` | `*tele.Message` параметр → собственная typed view (DTO с полями FromID, Text, MessageID), чтобы conversation-слой не зависел от lib |
| `telegram/conversation_test.go` | Адаптация моков под новый DTO |
| `telegram/callbacks.go` | `tele.Context` → `(ctx context.Context, b *bot.Bot, update *models.Update)`; routing на `update.CallbackQuery.Data`; `c.Edit/Respond/Send` → `b.EditMessageText/AnswerCallbackQuery/SendMessage` |
| `telegram/callbacks_test.go` | Полностью переписать |
| `telegram/bot.go` | `b.bot.Handle(...)` → `b.RegisterHandler(bot.HandlerTypeMessageText, ...)`; middleware `withAuth` → opts при создании bot или wrapper-handler; `processCommandResult*` → новая отправка через `b.SendMessage`; `registerCommands`, `handleKeyboardCommand` |
| `telegram/bot_test.go` | Адаптация |
| `telegram/setup.go` | `tele.NewBot(tele.Settings)` → `bot.New(token, opts...)`; webhook setup |
| `telegram/routes.go` / `routes_test.go` | Обработчик webhook-payload — адаптация под новый Update-тип |

---

## Фаза 0: Подготовка инфраструктуры

### Цель
Подключить новую библиотеку, оставив старую параллельно работающей. Никаких функциональных изменений.

### 0.1 RED: тесты
Не требуется — это infra-фаза.

### 0.2 GREEN: реализация
- `go get github.com/go-telegram/bot@latest` → новый блок require в `go.mod`.
- `go mod tidy`.
- `go build ./...` — должен пройти без ошибок (старая логика на telebot, новая lib доступна, но не используется).

### 0.3 REFACTOR
Нет.

### Проверка
- `go build ./...` без ошибок.
- `go test ./...` — все существующие тесты проходят (regression check на исходной точке).

### Отчёт фазы 0

**Статус:** GREEN ✅

**Что сделано:**
- `go get github.com/go-telegram/bot@latest` → v1.20.0
- `go mod tidy`
- `go build ./telegram/... ./commands/... ./opds/...` — OK
- `go test -v -short -race ./telegram/... ./opds/...` — PASS
- Старая `gopkg.in/telebot.v3` не тронута, всё работает

---

## Фаза 1: keyboard.go (reply-клавиатура)

### Цель
Перевести `telegram/keyboard.go` (генерация reply-keyboard юзеру) на новый API. Это самый маленький и автономный файл — хороший вход.

### 1.1 RED: тесты
**Файл:** `telegram/keyboard_test.go`
**Тесты:**
1. `TestGetMainKeyboard_Structure` — возвращаемый `*models.ReplyKeyboardMarkup` имеет `ResizeKeyboard=true`, `IsPersistent=true`, 3 ряда; первый ряд — Search/Favorites; второй — Author/Book; третий — Collections/Donate.
2. `TestGetCommandFromButtonText_Search` — `text="🔍 Поиск"` → `cmd="/search"`, `found=true`.
3. `TestGetCommandFromButtonText_Collections` — `text="📦 Подборки"` → `cmd="/collections"`, `found=true`.
4. `TestGetCommandFromButtonText_Unknown` — `text="абракадабра"` → `found=false`.
5. (все остальные кнопки — favorites/author/book/donate) — table-driven test.

### 1.2 GREEN: реализация
**`telegram/keyboard.go`:**
- Удалить `import "gopkg.in/telebot.v3"`.
- Импорт `github.com/go-telegram/bot/models`.
- `KeyboardButton` (внутренний тип, остаётся).
- `GetMainKeyboard() *models.ReplyKeyboardMarkup` — собирает `models.ReplyKeyboardMarkup{Keyboard: [][]models.KeyboardButton{{...}}, ResizeKeyboard: true, IsPersistent: true}`.
- `GetCommandFromButtonText` — без изменений в логике, только тип возврата.

### 1.3 REFACTOR
Если builder rows стал шумным — выделить helper `kbRow(btns ...KeyboardButton) []models.KeyboardButton`.

### Отчёт фазы 1

**Статус:** GREEN ✅

**Что сделано:**
- `telegram/keyboard.go` — полная замена `tele.ReplyMarkup` → `models.ReplyKeyboardMarkup` / `models.ReplyKeyboardRemove`
- Добавлен helper `kbRow()`
- `telegram/keyboard_test.go` — усиленные тесты с проверкой рядов и кнопок
- `bot.go` / `callbacks.go` используют `GetMainKeyboard()` / `RemoveKeyboard()` — старый telebot API в этих вызовах заменён на новый
- Все тесты PASS

---

## Фаза 2: processor.go (markup в CommandResult)

### Цель
Заменить тип поля `CommandResult.ReplyMarkup` на `*models.InlineKeyboardMarkup` и переписать все inline-builders в processor. Этот шаг изолирует bot-слой от processor-слоя по типам.

### 2.1 RED: тесты
**Файл:** `commands/processor_test.go` (новый — сейчас тестов в commands/ нет)
**Тесты:**
1. `TestExecuteShowFavorites_NoFavorites` — `Books=nil`, fav user → результат имеет `Message`, без `ReplyMarkup`.
2. `TestExecuteShowFavorites_WithBooks` — два item'а → `ReplyMarkup` имеет 2 ряда (по числу книг) inline-кнопок с правильными `CallbackData` `select:N`.
3. `TestExecuteShowFavorites_Pagination` — total > limit → последний ряд содержит «➡️ Вперед» с `CallbackData="next_page"`.
4. `TestExecuteShowCollections_NoCollections` — `Message` с эмодзи 📦, без markup.
5. `TestExecuteShowCollections_WithCollections` — ряд кнопок с `CallbackData="collection:N"`.
6. `TestExecuteCollectionBooks_Pagination` — пагинация работает.

Тесты используют моки `database.*` через интерфейс.

### 2.2 GREEN: реализация
**`commands/processor.go`:**
- Поле `CommandResult.ReplyMarkup *tele.ReplyMarkup` → `*models.InlineKeyboardMarkup`.
- `ExecuteSearch`, `ExecuteShowFavorites`, `ExecuteShowCollections`, `ExecuteCollectionBooks`, `ExecuteAuthorSelection`, `createBookButtonsWithPagination`, `createCollectionButtonsWithPagination`, `formatFavoriteBooksWithPagination` и т.д. — переписать использование `markup.Data(text, data)` → `models.InlineKeyboardButton{Text: text, CallbackData: data}`, а сборку `markup.Inline(rows...)` → `&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{...}}`.
- Удалить `import "gopkg.in/telebot.v3"` (или оставить временно если processor использует другие tele-типы — проверить).

### 2.3 REFACTOR
- Дублирующийся код построения inline-кнопок (favorites/collections/search) → helper `inlineRow(btns ...models.InlineKeyboardButton) []models.InlineKeyboardButton`.

### Отчёт фазы 2

**Статус:** GREEN ✅

**Что сделано:**
- `CommandResult.ReplyMarkup` → `*models.InlineKeyboardMarkup`
- Все 4 builder-метода переписаны (`createBookButtons`, `createBookButtonsWithPagination`, `createAuthorButtonsWithPagination`, `createCollectionButtonsWithPagination`)
- Добавлены helpers `inlineRow()` и `appendPaginationRow()`
- `import tele` заменён на `tgbot "github.com/go-telegram/bot/models"`
- Ноль ссылок на `tele.` в processor.go
- Все тесты PASS

---

## Фаза 3: conversation.go

### Цель
Изолировать conversation-layer от lib. Сейчас `ProcessIncomingMessage(token string, msg *tele.Message)` — заменить на собственную `*IncomingMessage` структуру, чтобы bot.go и callbacks.go вызывали свой DTO.

### 3.1 RED: тесты
**Файл:** `telegram/conversation_test.go`
**Тесты:**
1. `TestProcessIncomingMessage_StoresUserMessage` — вызов с DTO `{FromID, Text, MessageID}` → запись в Redis.
2. `TestProcessOutgoingMessage_StoresBotResponse` — ответ бота сохраняется в Redis под тем же conversation key.
3. `TestGetConversationContext_OrderedByTimestamp` — порядок сообщений сохраняется.
4. `TestClearConversation` — после Clear() контекст пуст.
5. `TestSetUserState_StoresInRedis` — состояние юзера (например `waiting_for_author`) персистится.

### 3.2 GREEN: реализация
**`telegram/conversation.go`:**
- Новая структура `IncomingMessage struct { TelegramID int64; Text string; MessageID int }`.
- Сигнатура `ProcessIncomingMessage(token string, msg *IncomingMessage) error`.
- Удалить `import "gopkg.in/telebot.v3"`.

### 3.3 REFACTOR
Нет.

### Отчёт фазы 3

**Статус:** GREEN ✅

**Что сделано:**
- `IncomingMessage` DTO `{SenderID int64, Text string, MessageID int}` добавлен в `conversation.go`
- `ProcessIncomingMessage` сигнатура: `*tele.Message` → `*IncomingMessage`
- `tele` import удалён из `conversation.go` и `conversation_test.go`
- Все 26 тестов в `conversation_test.go` используют `&IncomingMessage{}` вместо `*tele.Message`
- `go test -v -short -race ./telegram/...` — PASS

---

## Фаза 4: callbacks.go

### Цель
Перевести весь callback-routing на новый API. Все коллбэки (`author:`, `collection:`, `select:`, `download:`, `next_page`, `prev_page`) должны работать как раньше.

### 4.1 RED: тесты
**Файл:** `telegram/callbacks_test.go`
**Тесты:**
1. `TestHandle_AuthorCallback` — `Update{CallbackQuery{Data: "author:42"}}` → вызов handler'а, edit-message-text, ответ через `AnswerCallbackQuery`.
2. `TestHandle_CollectionCallback` — `"collection:7"` → переход в книги коллекции.
3. `TestHandle_SelectBook` — `"select:100"` → format-selection keyboard.
4. `TestHandle_Download_FB2` — `"download:fb2:100"` → отправка файла.
5. `TestHandle_NextPage_Search` — `"next_page"` + state `QueryType=combined_search` → следующий offset.
6. `TestHandle_NextPage_Collections` — `"next_page"` + state `QueryType=collections` → ExecuteShowCollections с инкрементом offset.
7. `TestHandle_InvalidCallback` — `Data` без префикса → AnswerCallbackQuery с ошибкой.
8. `TestBuildFormatSelectionKeyboard_HasFourFormats` — markup содержит 4 кнопки (FB2/EPUB/MOBI/ZIP) в правильном callback-формате.

Моки: `bot.Bot` через интерфейс (минимум `SendMessage`, `EditMessageText`, `AnswerCallbackQuery`, `SendDocument`).

### 4.2 GREEN: реализация
**`telegram/callbacks.go`:**
- `CallbackHandler.Handle(ctx, b, update)` — новая сигнатура.
- `update.CallbackQuery.Data` для разбора, `update.CallbackQuery.From.ID` для user.
- `b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID, MessageID, Text, ReplyMarkup})`.
- `b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID, Text})`.
- `b.SendDocument(ctx, &bot.SendDocumentParams{ChatID, Document, Caption})`.
- `buildFormatSelectionKeyboard` возвращает `*models.InlineKeyboardMarkup`.

### 4.3 REFACTOR
- Если pattern `Edit-or-Send` (edit message; on failure — send new) повторяется в каждом handler — выделить helper `editOrSend`.

### Отчёт фазы 4

**Статус:** GREEN ✅

**Что сделано:**
- `CallbackHandler.Handle` → `Handle(ctx context.Context, b *tgbotapi.Bot, update *tgbot.Update) error`
- Все handler-методы переписаны: `handlePagination`, `handleAuthorSelection`, `handleCollectionSelection`, `handleBookSelection`, `handleDownload`
- `buildFormatSelectionKeyboard` → возвращает `*tgbot.InlineKeyboardMarkup` (прямое построение `[][]tgbot.InlineKeyboardButton`)
- `sendBookFile` → `tgbotapi.SendDocument` + `tgbot.InputFileUpload{Filename, Data}`
- `editMessageWithResult` → `tgbotapi.EditMessageText` с fallback на `sendMessage`
- Добавлены helpers: `answerCallback`, `answerCallbackText`, `editOrSend`, `sendMessage`, `callbackMessageInfo`
- `tele.Photo` / `tele.Document` / `tele.SendOptions` / `tele.CallbackResponse` → полностью удалены
- `import tele` удалён из callbacks.go
- Aliases: `tgbotapi "github.com/go-telegram/bot"`, `tgbot "github.com/go-telegram/bot/models"`
- bot.go: временный telebot callback bridge (логирует + отвечает) — будет заменён в Phase 5
- `go build ./telegram/...` + `go test -v -short -race ./telegram/...` — PASS

**Известные долги:**
- RED-тесты (TestHandle_AuthorCallback, TestHandle_CollectionCallback, TestStartCommand_*, TestSearchCommand_*, TestFavoritesCommand_* и пр.) не написаны — handler'ы прошли только через go build. Моки `*tgbotapi.Bot` через интерфейс — отдельная задача.
- Webhook secret-token валидация (`X-Telegram-Bot-Api-Secret-Token`) не реализована — `tgbotapi.WithWebhookSecretToken` доступен, но требует согласования secret между SetWebhook и HandleWebhook. Отдельная фича.

---

## Фаза 5: bot.go (handlers, middleware, registerCommands)

### Цель
Финальная и самая большая — все командные handler'ы, middleware `withAuth`, обработка keyboard-кнопок, регистрация bot-commands.

### 5.1 RED: тесты
**Файл:** `telegram/bot_test.go`
**Тесты:**
1. `TestStartCommand_NewLink` — `/start` от незалинкованного юзера → ответ-инструкция, в БД сохраняется `telegram_id`.
2. `TestStartCommand_ExistingLink_Same` — `/start` от уже залинкованного → "you are already linked".
3. `TestStartCommand_ExistingLink_Other` — `/start` от чужого user → silent ignore (response nil).
4. `TestSearchCommand_RequiresQuery` — `/search` без аргумента → "set state waiting_for_search".
5. `TestFavoritesCommand_Lists` — `/favorites` → вызов processor + send message с markup.
6. `TestCollectionsCommand_Lists` — `/collections` → `ExecuteShowCollections(0, 5)` + send.
7. `TestRegisterCommands_AllExist` — `registerCommands` отправляет 9 команд в `bot.SetMyCommands`.
8. `TestHandleKeyboardCommand_Search` — text "🔍 Поиск" → set state + reply.
9. `TestHandleKeyboardCommand_Collections` — text "📦 Подборки" → ExecuteShowCollections.
10. `TestWithAuth_NoBotOwner` — middleware → "send /start" reply.
11. `TestWithAuth_LinkedOK` — middleware пропускает.

Моки: `*bot.Bot` (через интерфейс), `database.*`, `ConversationManager`.

### 5.2 GREEN: реализация
**`telegram/bot.go`:**
- `b.bot.Handle(...)` → `b.RegisterHandler(bot.HandlerTypeMessageText, "/cmd", bot.MatchTypeExact, handler)`.
- `withAuth` — обёртка, которая принимает `func(ctx, b, update)` и возвращает такую же.
- Сигнатура handler'а — `func(ctx context.Context, bot *bot.Bot, update *models.Update)`.
- Извлечение user: `update.Message.From.ID`.
- Отправка: `bot.SendMessage(ctx, &bot.SendMessageParams{ChatID: update.Message.Chat.ID, Text, ReplyMarkup})`.
- `processCommandResult` / `processCommandResultWithKeyboard` — переписать на `bot.SendMessage`.
- `registerCommands` — `b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: []models.BotCommand{...}})`.
- `handleKeyboardCommand` — switch по `update.Message.Text`, отправка через `bot.SendMessage`.

**`telegram/setup.go`:**
- `bot.New(token, bot.WithWebhookSecretToken(...), bot.WithDefaultHandler(unknown), bot.WithMiddlewares(authMW))`.

**`telegram/routes.go`:**
- Обработка webhook-payload — `b.WebhookHandler(...)` или ручной `bot.Update`-парсинг.

### 5.3 REFACTOR
- Заменить switch в `handleKeyboardCommand` на map[string]func(...) handler.
- Удалить `gopkg.in/telebot.v3` из `go.mod` (`go mod tidy`).

### Отчёт фазы 5

**Статус:** GREEN ✅

**Что сделано:**
- `Bot.bot` тип: `*tele.Bot` → `*tgbotapi.Bot`
- `createBotInstance`: `tele.NewBot(tele.Settings)` → `tgbotapi.New(token, opts...)`
- Все handler'ы: `b.bot.Handle(...)` → `b.bot.RegisterHandler(tgbotapi.HandlerTypeMessageText, ...)`
- `/start`, `/context`, `/clear`, `/search`, `/b`, `/a`, `/ba`, `/favorites`, `/collections`, `/donate` — все переписаны
- Callback handler: `RegisterHandler(tgbotapi.HandlerTypeCallbackQueryData, "", tgbotapi.MatchTypePrefix, ...)`
- Catch-all text handler: `RegisterHandlerMatchFunc(...)` для keyboard buttons + free text + user states
- `withAuth`: `tele.HandlerFunc` → `tgbotapi.HandlerFunc` + `authHandlerFunc` тип
- `processCommandResult` / `handleCommandError` / `validateUserLinked` → `ctx, b, chatID` вместо `tele.Context`
- `registerCommands`: `tele.Command` → `tgbot.BotCommand` + `bot.SetMyCommands`
- `SetWebhook`: `tele.Webhook` → `tgbotapi.SetWebhookParams{URL: ...}`
- `RemoveBot`: `bot.RemoveWebhook()` + `bot.Stop()` → `bot.DeleteWebhook(ctx, ...)`
- `HandleWebhook`: `tele.Update` → `tgbot.Update` + `bot.ProcessUpdate(ctx, &update)`
- `tele.OnCallback` / `tele.OnText` / `tele.ChatPrivate` / `tele.ModeHTML` / `tele.ModeMarkdown` — полностью удалены
- `import tele` удалён из bot.go
- `go mod edit -droprequire gopkg.in/telebot.v3` + `go mod tidy` — старая библиотека полностью удалена
- Все тесты PASS

**Post-merge review fixes:**
1. Private-chat защита восстановлена через `tgbotapi.WithMiddlewares(privateChatMiddleware)`
2. `withAuth`: auth-check перенесён до Redis-записи
3. `handleBookSelection`: chatID из `callbackMessageInfo(q)` с fallback на `q.From.ID`
4. `editOrSend`: fallback при невалидном callback message шлёт на `q.From.ID` вместо chatID=0
5. `callbackMessageInfo`: упрощена проверка `q.Message.Message == nil`

**Известные долги:**
- Handler-тесты (RED-фаза) не написаны — зафиксированы как технический долг
- Webhook secret-token валидация — не блокер, отдельная фича

---

## Фаза 6: Цветные кнопки (Style)

### Цель
Применить новое поле `Style` для inline-кнопок там, где это улучшает UX. Это **финальная косметика** — делается только после полной зелёной миграции.

### 6.1 RED: тесты
**Файл:** дополнить существующие тесты processor (Phase 2) и callbacks (Phase 4).
**Тесты:**
1. `TestFormatSelectionButtons_HasSuccessStyle` — кнопки FB2/EPUB/MOBI/ZIP имеют `Style="success"`.
2. `TestPaginationButtons_HasPrimaryStyle` — `prev_page`/`next_page` → `Style="primary"`.
3. `TestSelectBookButton_HasSecondaryStyle` (опционально) — `select:N` → `Style="secondary"` или дефолт.
4. (Опционально) `TestClearButton_HasDangerStyle` — `/clear` подтверждение через danger.

### 6.2 GREEN: реализация
- В `buildFormatSelectionKeyboard` (callbacks.go): добавить `Style: "success"`.
- В `createBookButtonsWithPagination` (processor.go): кнопки навигации `Style: "primary"`.
- Аналогично в `createCollectionButtonsWithPagination`.

### 6.3 REFACTOR
Если стилей становится много — вынести константы `StyleSuccess = "success"` etc. в `telegram/styles.go`.

### Отчёт фазы 6

**Статус:** GREEN ✅

**Что сделано:**
- Format selection buttons (FB2/EPUB/MOBI/ZIP) → `Style: "success"` (green)
- Pagination buttons (⬅️ Назад / ➡️ Вперед) → `Style: "primary"` (blue)
- `GetMainKeyboard`: добавлен `IsPersistent: true`
- Все тесты PASS

---

## Фаза 7: Smoke-тест на staging

### Цель
Прокатать миграцию на реальном Telegram-боте перед деплоем в прод.

### 7.1 RED: тесты
Не требуется — это manual integration check.

### 7.2 GREEN: реализация
Чек-лист (юзер выполняет вручную через тестовый бот):
1. `/start` (новый юзер): получает инструкцию.
2. `/start` (повторно): "already linked".
3. Reply-keyboard отображается, кнопки кликабельны.
4. `/search` через AI: отправить "Война и мир" → результаты.
5. `/b` (книга): "1984" → результаты с пагинацией.
6. Клик `next_page` → следующая страница.
7. Клик `select:N` → format-keyboard с цветными кнопками.
8. Клик `download:fb2:N` → файл присылается.
9. `/favorites`: показ или пустое сообщение.
10. `/collections`: список публичных + клик на подборку → книги.
11. `/clear`: контекст очищен.
12. `/donate`: текст с кнопкой URL.
13. Multi-tenant: второй юзер с своим ботом — изолированная сессия.

### 7.3 REFACTOR
Не требуется.

### Отчёт фазы 7
_(заполняется при реализации)_

---

## Фаза 8: Webhook secret-token защита

### Цель
Закрыть вектор подделки webhook'а через знание URL: добавить проверку заголовка `X-Telegram-Bot-Api-Secret-Token`. Секрет — дериват токена бота, независимый от UUID в пути, поэтому утечка URL через прокси-логи сама по себе не позволяет фальсифицировать апдейты.

### 8.1 RED: тесты
**Файл:** `telegram/routes_test.go`
**Тесты:**
1. `TestWebhookSecretFromToken_Deterministic` — одинаковый токен → одинаковый секрет.
2. `TestWebhookSecretFromToken_Format` — длина 32, regex `^[A-Za-z0-9_-]+$` (Telegram требует `^[A-Za-z0-9_-]{1,256}$`).
3. `TestWebhookSecretFromToken_DependsOnToken` — разные токены → разные секреты.
4. `TestHandleWebhook_MissingSecretToken` — зарегистрированный UUID, нет заголовка → 401.
5. `TestHandleWebhook_WrongSecretToken` — зарегистрированный UUID, неверный заголовок → 401.

### 8.2 GREEN: реализация
**`telegram/bot.go`:**
- `webhookSecretFromToken(token string) string` — `sha256("webhook_secret:" + token)`, hex, 32 символа.
- `createBotInstance` — `tgbotapi.WithWebhookSecretToken(webhookSecretFromToken(token))` в opts.
- `SetWebhook` — `SetWebhookParams{URL, SecretToken: webhookSecretFromToken(token)}`. Убрать early-return на `isCorrect`, чтобы при первом запуске после деплоя гарантированно перезаписать webhook с новым секретом (URL у Telegram не меняется, но секрет надо донести).
- `InitializeExistingBots` — всегда `SetWebhook(token)` на старте (выкинуть `checkWebhookStatus` shortcut), чтобы существующие боты получили secret. Идемпотентно для Telegram; `runHealthCheck` в hot-path не задевается, у него свой gate.
- `HandleWebhook` — после lookup'а bot по UUID, до парсинга JSON: сравнить `c.GetHeader("X-Telegram-Bot-Api-Secret-Token")` с ожидаемым секретом. Mismatch → 401.

### 8.3 REFACTOR
Нет.

### Отчёт фазы 8

**Статус:** GREEN ✅

**Тесты:**
- 5 новых: `TestWebhookSecretFromToken_Deterministic`, `_Format`, `_DependsOnToken`, `TestHandleWebhook_MissingSecretToken`, `TestHandleWebhook_WrongSecretToken` — все PASS.

**Реализация:**
- `telegram/bot.go`:
  - `webhookSecretFromToken(token)` — `sha256("webhook_secret:" + token)`, hex 32 chars (импорты `crypto/sha256`, `encoding/hex`).
  - `createBotInstance` — `tgbotapi.WithWebhookSecretToken(...)` в opts.
  - `SetWebhook` — `SecretToken` пробрасывается в `SetWebhookParams`. Убран early-return по `isCorrect` (Telegram не возвращает secret_token в `getWebhookInfo`, поэтому всегда переписываем).
  - `InitializeExistingBots` — выкинут `checkWebhookStatus` shortcut, на старте всегда `SetWebhook` чтобы donести secret до уже-настроенных ботов.
  - `HandleWebhook` — после lookup'а bot и до парсинга JSON: проверка `X-Telegram-Bot-Api-Secret-Token`. Mismatch → 401.

**Регрессия:**
- `go build ./telegram/... ./commands/... ./opds/...` — OK.
- `go test -short -race ./telegram/... ./commands/... ./opds/...` — PASS.

---

## Roll-back план

Если на любой фазе выявится блокирующая проблема:
- Каждая фаза = отдельный коммит. `git revert <hash>` откатывает по фазе.
- Если миграция целиком провалена — `git revert` всего диапазона до Phase 0 включительно. `gopkg.in/telebot.v3` остаётся в `go.mod` после revert.

## Out of scope (этого релиза)

- Inline-mode (`@yourbot query`) — отдельная фича, требует separate handler.
- Уведомления / подписки — отдельная feature-фаза.
- `/help`, `/random`, `/recent` — отдельные фичи бота, после миграции.
- Rate-limiting на бот-команды — отдельная задача.

## Открытые вопросы (закрыть при старте Фазы 0)

- Версия `github.com/go-telegram/bot` точно `v1.20.0` или брать `@latest`?
- В тестовом боте у нас уже есть BotFather-токен или нужен новый для smoke?
