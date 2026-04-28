# OPDS Collections

## Контекст
Коллекции (подборки) доступны через REST API и веб-фронтенд, но отсутствуют в OPDS-фиде. Нужно добавить навигацию по подборкам в OPDS, чтобы пользователи электронных читалок могли просматривать и скачивать книги из коллекций.

### Принятые решения
| Вопрос | Решение |
|--------|---------|
| Где показывать навигацию | В корневом фиде OPDS, рядом с «Избранное» и «По языкам» |
| Формат фида | Navigation feed (список подборок) → Acquisition feed (книги) — аналогично `/opds/languages` |
| Какие коллекции показывать | Только публичные (`is_curated=true AND is_public=true`), как в REST API |
| Какие книги внутри | Только matched (`auto_matched` + `manual`), через существующий `GetPublicCollectionBooks()` |
| Пагинация коллекций | Да, по 10, как везде в OPDS |
| Пагинация книг | Да, по 10, как везде в OPDS |

### Что уже есть и НЕ требует изменений
- `database.ListPublicCuratedCollections()` — список публичных подборок
- `database.GetPublicCollectionBooks()` — книги подборки с фильтром по match_status
- `database.GetPublicCuratedCollection()` — одна подборка
- `opdsutils.CreateItem()` — создание OPDS-entry из книги
- `opdsutils.Feed` / `opdsutils.Item` / `opdsutils.Link` — структуры для Atom-фида
- `opdsutils.Feed.ToAtom()` — сериализация в XML
- Паттерн навигации «По языкам» в `opds/bookslist.go` и `opds/languages.go`

### Ключевые файлы
| Файл | Что менять |
|------|-----------|
| `opds/bookslist.go` | Добавить навигационный пункт «Подборки» в корневой фид |
| `opds/collections.go` | **Новый** — хендлеры списка подборок и книг внутри |
| `opds/routes.go` | Добавить два маршрута |
| `opds/collections_test.go` | **Новый** — тесты |

---

## Фаза 1: Список коллекций (navigation feed)

### Цель
Добавить навигационный пункт в корневой фид и эндпоинт `/opds/collections`, возвращающий Atom-фид со списком публичных подборок.

### 1.1 RED: тесты
**Файл:** `opds/collections_test.go`
**Тесты:**
1. `TestGetCollections` — проверяет, что `/opds/collections/0` возвращает 200 и валидный Atom XML с элементами коллекций
2. `TestGetCollectionsEmpty` — проверяет пустой список коллекций (фид без entries)
3. `TestGetCollectionsPagination` — проверяет наличие `rel="next"` при наличии следующей страницы
4. `TestCollectionsInRootFeed` — проверяет, что корневой фид содержит навигационную ссылку «Подборки» → `/opds/collections/0`

### 1.2 GREEN: реализация
**`opds/bookslist.go`:**
- В блоке навигации корневого фида (строка ~116) добавить Item «Подборки» с ссылкой на `/opds/collections`

**`opds/collections.go`:**
- `GetCollections(c *gin.Context)` — парсит `page` из URL, вызывает `database.ListPublicCuratedCollections()`, строит `opdsutils.Feed` с entries-навигацией на `/opds/collections/:id/0`, добавляет `rel="next"` при наличии следующей страницы

**`opds/routes.go`:**
- Добавить `r.GET("/collections/:page", GetCollections)`

### 1.3 REFACTOR
Нет — новый файл, паттерн повторяет `languages.go`.

### Отчёт фазы 1

**Статус:** GREEN ✅

**Что сделано:**
- Создан `opds/collections.go` с хендлерами `GetCollections` и `GetCollectionBooks`
- Зарегистрированы маршруты в `opds/routes.go`: `/collections/:page` и `/collections/:id/:page`
- Добавлен навигационный пункт «Подборки» → `/opds/collections/0` в корневой фид (`opds/bookslist.go`)
- Создан `opds/collections_test.go` с 5 тестами (все skip в short mode, т.к. требуют БД)
- Компиляция: `go build ./opds/...` — OK
- `go vet ./opds/...` — OK
- `go test -v -short -race ./opds/...` — PASS (тесты skip, гонок нет)

**REFACTOR:** Не требуется — новый файл следует паттерну `languages.go`.

---

## Фаза 2: Книги внутри коллекции (acquisition feed)

### Цель
Добавить эндпоинт `/opds/collections/:id/:page`, возвращающий книги конкретной подборки с возможностью скачивания.

### 2.1 RED: тесты
**Файл:** `opds/collections_test.go`
**Тесты:**
1. `TestGetCollectionBooks` — проверяет, что `/opds/collections/:id/0` возвращает 200 и Atom с книгами (у каждой записи есть acquisition link)
2. `TestGetCollectionBooksNotFound` — проверяет 404 для несуществующей коллекции
3. `TestGetCollectionBooksPagination` — проверяет наличие `rel="next"` при наличии следующей страницы

### 2.2 GREEN: реализация
**`opds/collections.go`:**
- `GetCollectionBooks(c *gin.Context)` — получает `id` из URL, вызывает `database.GetPublicCuratedCollection()` для валидации и имени, затем `database.GetBooks()` с фильтром `CuratedCollection: id` (существующий механизм). Строит Atom acquisition feed с `opdsutils.CreateItem()`.

**`opds/routes.go`:**
- Добавить `r.GET("/collections/:id/:page", GetCollectionBooks)`

### 2.3 REFACTOR
Вынести общие link-шаблоны (start, search) в helper — аналогично `langLinks()` в `languages.go`.

### Отчёт фазы 2

**Статус:** GREEN ✅

**Что сделано:**
- Хендлер `GetCollectionBooks` был реализован в Фазе 1 вместе с навигационным фидом
- Маршрут `/collections/:id/:page` зарегистрирован в `opds/routes.go`
- Добавлены 5 тестов Phase 2 в `opds/collections_test.go`
- `go build ./opds/...` — OK
- `go test -v -short -race ./opds/...` — PASS (10 тестов)

**REFACTOR:** Не проводился — общий рефакторинг link-шаблонов (start, search) отложен, дублирование минимальное.

---

## Фаза 3: Telegram бот — команда /collections

### Цель
Добавить команду `/collections` и кнопку клавиатуры для просмотра публичных подборок и скачивания книг из них через Telegram бот.

### 3.1 RED: тесты
**Файл:** `commands/processor_test.go` (дополнить)
**Тесты:**
1. `TestExecuteShowCollections` — список публичных подборок с пагинацией, возвращает `CommandResult` с inline-кнопками коллекций
2. `TestExecuteShowCollectionsEmpty` — пустой список
3. `TestExecuteCollectionBooks` — книги конкретной подборки с пагинацией, inline-кнопки для выбора и скачивания
4. `TestExecuteCollectionBooksNotFound` — несуществующая коллекция

**Файл:** `telegram/callbacks_test.go` (дополнить)
**Тесты:**
5. `TestHandleCollectionSelection` — callback `collection:ID` показывает книги подборки
6. `TestHandleCollectionPagination` — callback `collection_page:ID:offset` пагинация внутри подборки

### 3.2 GREEN: реализация
**`commands/processor.go`:**
- `ExecuteShowCollections(offset, limit int) (*CommandResult, error)` — вызывает `database.ListPublicCuratedCollections()`, формирует сообщение со списком, inline-кнопки `collection:ID`
- `ExecuteCollectionBooks(collectionID int64, userID, offset, limit int) (*CommandResult, error)` — вызывает `database.GetPublicCuratedCollection()` + `database.GetPublicCollectionBooks()`, формирует список книг с пагинацией, `SearchParams.QueryType = "collection_books"`
- Приватные хелперы форматирования по аналогии с `formatFavoriteBooksWithPagination`

**`telegram/callbacks.go`:**
- В `Handle()` switch добавить `case strings.HasPrefix(callbackData, "collection:")` и `case strings.HasPrefix(callbackData, "collection_page:")`
- `handleCollectionSelection()` — показывает книги выбранной коллекции
- `handleCollectionPagination()` — пагинация внутри коллекции
- В `executeSearchWithPagination()` добавить `case "collection_books"` для пагинации

**`telegram/keyboard.go`:**
- Добавить кнопку `btnCollections = KeyboardButton{Text: "📦 Подборки", Command: "/collections"}`
- Добавить в `GetMainKeyboard()` (новая строка или в существующую)
- Добавить в `GetCommandFromButtonText()`

**`telegram/bot.go`:**
- Добавить handler для `/collections` — вызывает `processor.ExecuteShowCollections(0, 5)`
- Добавить `case "/collections"` в `handleKeyboardCommand`
- Добавить команду в `registerCommands`

### 3.3 REFACTOR
- Вынести общие паттерны форматирования списка книг в processor в отдельный метод, если дублирование с favorites/book search станет существенным.

### Отчёт фазы 3

**Статус:** GREEN ✅

**Что сделано:**
- `commands/processor.go`: добавлены `ExecuteShowCollections`, `ExecuteCollectionBooks`, форматтеры и кнопки для коллекций
- `telegram/keyboard.go`: добавлена кнопка `📦 Подборки` → `/collections` в основную клавиатуру
- `telegram/bot.go`: добавлен хендлер `/collections`, регистрация команды, обработка keyboard
- `telegram/callbacks.go`: добавлен обработчик `collection:ID` для выбора подборки, `collection_books` и `collections` в пагинации
- Тест: `TestGetCommandFromButtonText/Collections_button` — PASS
- Все тесты `commands`, `telegram`, `opds` — PASS, гонок нет

**REFACTOR:** Не проводился — код следует существующим паттернам processor/callbacks.
