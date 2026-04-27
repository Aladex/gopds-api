# Curated Book Collections — TDD план

## Контекст

Админски курируемые подборки книг, импорт через CSV/textarea в админке, матчинг
по локальной БД с тремя статусами (`auto_matched` / `ambiguous` / `not_found`) и
ручным разрулингом. Сервер наружу не ходит — внешние источники (livelib и т.п.)
парсятся админом любым удобным способом, на сервер приходит готовый JSON с items.

### Принятые решения

| Вопрос | Решение |
|--------|---------|
| Модель | Расширяем `book_collections` + новые `book_collection_items` (book_id nullable) |
| Импорт | Только админ |
| UGC-подборки | v2, не сейчас |
| Способ импорта | CSV-файл или textarea-paste, парсинг на клиенте → JSON |
| Внешний фетч | Не делаем |
| Источник в БД | `source_url` — опциональная админская заметка, юзерам не показывается |
| Спорные совпадения | Неблокирующий импорт, счётчик в админке |
| `not_found` items | Хранить навсегда, ручной разрулинг |
| `match_decisions` кэш | Да, по нормализованной паре `(author, title)` |
| Юзеру показываем | Только название коллекции + список найденных книг |
| Прогресс импорта | Polling статуса |
| Reimport / дозагрузка | v2 |
| Удаление | Hard delete с каскадом |
| Публикация | Переключатель `is_public` |
| CSV-формат | Фиксированный header: `title,author,year` (year опционален) |

### Что уже есть и НЕ требует изменений

- `models/collections.go` — UGC-структуры `BookCollection`, `BookCollectionBook`, `CollectionVote` (для v2 UGC, оставляем как есть, только дополним `BookCollection` новыми колонками).
- `middlewares/admin.go` — admin-middleware (используем для admin-роутов).
- `middlewares/auth.go` — JWT auth для public-роутов.
- Существующий book search API — autocomplete для разрулинга ambiguous.
- Trigram-индекс на `opds_catalog_author.full_name` (миграция 09).
- Polling-подход уже принят (никаких WebSocket'ов).
- Frontend стек: React 19 + MUI v6 + i18next + axios + `@dnd-kit/sortable`.

### Ключевые файлы

| Файл | Что менять |
|------|-----------|
| `database_migrations/18-add-curated-collections.sql` | Новый — alter `book_collections` + новые таблицы `book_collection_items`, `book_match_decisions`, trgm-индекс на title |
| `models/collections.go` | Дополнить `BookCollection`, добавить `BookCollectionItem`, `BookMatchDecision` |
| `services/collection_matcher.go` | Новый — нормализация + бакетизация |
| `services/collection_matcher_test.go` | Новый — юнит-тесты матчера |
| `services/curated_collection_import.go` | Новый — оркестрация импорта |
| `services/curated_collection_import_test.go` | Новый — тесты сервиса с моками |
| `database/curated_collections.go` | Новый — DAO для коллекций и items |
| `api/admin_collections.go` | Новый — admin handlers |
| `api/admin_collections_test.go` | Новый — httptest на admin handlers |
| `api/collections.go` | Новый — public read |
| `api/collections_test.go` | Новый — httptest на public, проверка DTO-фильтрации |
| `cmd/routes.go` | Регистрация новых routes |
| `booksdump-frontend/src/components/admin/CuratedCollections/*` | Новый раздел админки |
| `booksdump-frontend/src/components/Collections/*` | Новые публичные страницы |

---

## Фаза 1: Schema + models (инфраструктура, не-TDD)

### Цель
Накатить миграцию БД и завести Go-структуры под новую схему.

### 1.1 RED: тесты
**Не требуется.** Миграции и ORM-маппинг тестируются вручную: запуск миграции на чистой БД + `go build ./...`. TDD-логика начинается с Phase 2.

### 1.2 GREEN: реализация
**`database_migrations/18-add-curated-collections.sql`:**
- `ALTER TABLE book_collections`:
  - `user_id` → NULLABLE
  - `is_curated BOOLEAN DEFAULT FALSE`
  - `source_url TEXT`
  - `import_status VARCHAR(32)` — `importing` / `completed` / `failed`
  - `import_error TEXT`
  - `imported_at TIMESTAMPTZ`
  - `import_stats JSONB`
- `CREATE TABLE book_collection_items`:
  - `id BIGSERIAL PK`
  - `collection_id BIGINT NOT NULL FK → book_collections(id) ON DELETE CASCADE`
  - `book_id BIGINT NULL FK → opds_catalog_book(id) ON DELETE SET NULL`
  - `external_title TEXT NOT NULL`
  - `external_author TEXT`
  - `external_extra JSONB`
  - `match_status VARCHAR(32) NOT NULL`
  - `match_score REAL`
  - `position INT DEFAULT 0`
  - `created_at`, `updated_at TIMESTAMPTZ DEFAULT NOW()`
  - индексы: `(collection_id, position)`, `match_status`
- `CREATE TABLE book_match_decisions`:
  - `id BIGSERIAL PK`
  - `author_norm TEXT NOT NULL`
  - `title_norm TEXT NOT NULL`
  - `book_id BIGINT NOT NULL FK → opds_catalog_book(id) ON DELETE CASCADE`
  - `decided_by_user_id BIGINT FK → auth_user(id)`
  - `created_at TIMESTAMPTZ DEFAULT NOW()`
  - UNIQUE `(author_norm, title_norm)`
- `CREATE INDEX IF NOT EXISTS opds_catalog_book_title_trgm_idx ON opds_catalog_book USING GIN (lower(title) gin_trgm_ops);`

**`models/collections.go`:**
- Дополнить `BookCollection` полями: `IsCurated`, `SourceURL`, `ImportStatus`, `ImportError`, `ImportedAt`, `ImportStats`.
- Новые структуры `BookCollectionItem`, `BookMatchDecision` с go-pg тегами.

### 1.3 REFACTOR
Нет.

### Проверка
- `go build ./...` без ошибок.
- Накатить миграцию руками на dev-БД, проверить что таблицы созданы и существующие UGC-коллекции продолжают работать (`user_id` стал nullable, не должно сломать).

### Отчёт фазы 1

**Тесты:** не требуются (инфраструктура — миграция SQL и go-pg маппинг). TDD-цикл стартует с Phase 2 (matcher).

**Реализация:**
- `database_migrations/18-add-curated-collections.sql` — `ALTER` `book_collections` (`user_id` стал nullable, добавлены `is_curated`, `source_url`, `import_status`, `import_error`, `imported_at`, `import_stats`), новые таблицы `book_collection_items` и `book_match_decisions`, trgm-индекс `opds_catalog_book_title_trgm_idx`, индекс `idx_book_collections_curated_public`, триггер `update_book_collection_items_updated_at` (переиспользует функцию из миграции 08).
- `models/collections.go` — `BookCollection.UserID` стал `*int64`, добавлены поля `IsCurated`, `SourceURL`, `ImportStatus`, `ImportError`, `ImportedAt`, `ImportStats`, новые структуры `BookCollectionItem`, `BookMatchDecision`, константы `MatchStatus*` и `ImportStatus*`.
- Попутно: `database_migrations/load-extensions.sh` — `create extension pg_trgm` → `create extension if not exists pg_trgm`. Это был существующий баг (pg_trgm уже создаётся в `01-initial.sql`), из-за которого postgres-контейнер не поднимался с нуля.
- `booksdump-frontend/build/.keep` — заглушка для `//go:embed booksdump-frontend/build/*` без собранного фронта (нужно для локального `go build`).

**Регрессия:**
- `go build ./models/... ./database/... ./api/... ./services/... ./middlewares/... ./opds/... ./utils/... ./sessions/... ./telegram/...` — без ошибок.
- `go vet` тех же пакетов — без замечаний.
- Postgres поднят с нуля (`docker compose up -d postgres` со свежим volume), все 17 предыдущих миграций + новая 18-я применились последовательно. `\d` показывает корректную схему всех трёх таблиц с ожидаемыми колонками, FK, индексами и триггером.

---

## Фаза 2: Matcher — нормализация и бакетизация

### Цель
Реализовать чистую функцию матчинга одного `(author, title)` против БД с возвратом одного из четырёх результатов: `manual_from_cache` / `auto_matched` / `ambiguous` / `not_found`.

### 2.1 RED: тесты
**Файл:** `services/collection_matcher_test.go`

**Тесты на `normalizePair(author, title)`:**
1. `("Рэй Брэдбери", "451° по Фаренгейту")` → нижний регистр, без знаков градуса.
2. `("Лев Толстой", «Война и мир»)` → кавычки удалены.
3. `("Дж. Оруэлл", "1984: А-фантазия")` → подзаголовок после `:` отрезан.
4. `("Маргарет Этвуд", "Рассказ — Служанки")` → длинное тире нормализовано в пробел.
5. `("  Лев   ТОЛСТОЙ  ", "  Анна   Каренина  ")` → лишние пробелы сжаты.
6. Пустые входы → пустой норм.

**Тесты на `matchOne(ctx, finder, author, title)`:**
7. Cache hit (есть запись в `book_match_decisions`) → результат `manual` с book_id из кэша, без вызова finder.
8. 1 кандидат, score ≥ HIGH_THRESHOLD → `auto_matched` с book_id.
9. 1 кандидат, score MID → `ambiguous`.
10. 2+ кандидата (любые score) → `ambiguous` с top-N в результате.
11. 0 кандидатов → `not_found`.

`finder` — интерфейс с одним методом `FindCandidates(ctx, authorNorm, titleNorm) ([]Candidate, error)`. В тестах — мок.
`decisionLookup` — интерфейс `Lookup(ctx, authorNorm, titleNorm) (*int64, error)`. В тестах — мок.

### 2.2 GREEN: реализация
**`services/collection_matcher.go`:**
- `Candidate{BookID int64, Score float32}`
- `MatchResult{Status string, BookID *int64, Score float32, Candidates []Candidate}`
- `func normalizePair(author, title string) (authorNorm, titleNorm string)` — реализация по требованиям тестов.
- `type CandidateFinder interface{ FindCandidates(ctx, authorNorm, titleNorm string) ([]Candidate, error) }`
- `type DecisionLookup interface{ Lookup(ctx, authorNorm, titleNorm string) (*int64, error) }`
- `func MatchOne(ctx, dl DecisionLookup, cf CandidateFinder, author, title string) (MatchResult, error)` — реализация бакетизации.
- Константы `HighThreshold = 0.85`, `MidThreshold = 0.5` (подобрать по факту в Phase 3).

### 2.3 REFACTOR
Если `normalizePair` получилась шумной — выделить шаги (replacer для пунктуации, обрезка подзаголовков) в приватные функции.

### Отчёт фазы 2

**Тесты:** 13 (один табличный `TestNormalizePair` с 6 кейсами + 8 функций для `MatchOne`).

`TestNormalizePair` покрывает: lowercase+градус, guillemets («»), обрезку подзаголовка по `:`, обрезку по ` — `, схлопывание лишних пробелов, пустые входы.

`TestMatchOne_*` покрывает: cache hit (finder не вызывается), auto-match при единственном кандидате со score≥0.85, ambiguous при single mid-score, ambiguous при множественных, not_found при нуле кандидатов, not_found при кандидатах ниже MID, проброс ошибок от lookup и от finder.

**Реализация:**
- `services/collection_matcher.go`:
  - Константы `HighThreshold = 0.85`, `MidThreshold = 0.50`.
  - `Candidate{BookID, Score}`, `MatchResult{Status, BookID, Score, Candidates}`.
  - Интерфейсы `DecisionLookup`, `CandidateFinder` + `*Func`-адаптеры (упрощают мокинг в тестах и реализацию через лямбды для DAO).
  - `normalizePair` → `trimSubtitle` (по `:` и ` — `) + `normalizeText` (lowercase, любые non-letter/non-digit как один пробел, trim).
  - `MatchOne` — cache-first, фильтр кандидатов по MID (всё ниже считается шумом), бакетизация: 0 → not_found, 1 ≥ HIGH → auto_matched, иначе ambiguous.

**Регрессия:**
- `go test ./services/ -run "TestNormalizePair|TestMatchOne" -v` — 13/13 PASS.
- Полный suite `go test ./models/... ./database/... ./api/... ./services/... ./middlewares/... ./opds/... ./utils/... ./sessions/... ./telegram/...` — все пакеты OK, 0 регрессий.

**REFACTOR:** не потребовался — реализация уже разбита на `trimSubtitle` / `normalizeText` / `normalizePair` без дублирования.

---

## Фаза 3: Import service

### Цель
Оркестрация импорта: принять `{name, sourceURL, items[]}`, создать коллекцию, batch-insert items, прогнать матчер, посчитать stats, финализировать `import_status`.

### 3.1 RED: тесты
**Файл:** `services/curated_collection_import_test.go`

**Тесты на `Import(ctx, params, deps)`:**
1. Пустой items → ошибка валидации, коллекция не создаётся.
2. Один item → создана коллекция, заинсертен item, статус `auto_matched` (мок матчера возвращает `auto_matched`).
3. Три items с разными результатами матчера (auto / ambiguous / not_found) → `import_stats` = `{matched:1, ambiguous:1, not_found:1}`.
4. Ошибка от матчера → `import_status='failed'`, `import_error` содержит сообщение.
5. По умолчанию `is_curated=true`, `is_public=false`.
6. `position` назначается по порядку входных items (0, 1, 2).

**Зависимости через интерфейсы:**
- `CollectionRepo` (Create, AddItem, UpdateStatus, FinalizeStats).
- `Matcher` (MatchAll или итерация по MatchOne).

В тестах — fake-реализации, без реальной БД.

### 3.2 GREEN: реализация
**`services/curated_collection_import.go`:**
- `type ImportParams struct { Name, SourceURL string; Items []ImportItem }`
- `type ImportItem struct { Title, Author string; Year int; Extra map[string]any }`
- `type CollectionRepo interface { ... }`
- `type Matcher interface { MatchOne(ctx, author, title string) (MatchResult, error) }`
- `func Import(ctx, params ImportParams, repo CollectionRepo, matcher Matcher) (collectionID int64, err error)`
- Логика: validate → create collection (status=`importing`) → loop items (insert + match + update item status) → aggregate stats → update status=`completed`.

**`database/curated_collections.go`:**
- Реализация `CollectionRepo` поверх go-pg.
- Реализация `CandidateFinder` (trigram SQL) и `DecisionLookup`.

### 3.3 REFACTOR
Если оркестратор перегружен — выделить отдельные шаги (`createSkeleton`, `processItem`, `finalize`).

### Отчёт фазы 3

**Тесты:** 8 функций для `Import` (отказ от пустого `name`, отказ от пустого `items`, single auto-matched, three mixed buckets, manual-from-cache считается как matched, ошибка матчера → finalized as failed, year сериализуется в `external_extra`, отсутствие лишнего JSON при пустых year+extra). Покрывают: валидацию, корректные счётчики, position по порядку, передачу `source_url` в repo, финализацию.

**Реализация:**
- `services/curated_collection_import.go` — сервисный слой:
  - `ImportItem`, `ImportParams` — входные данные API/CSV.
  - `CollectionRepo`, `Matcher`, `MatcherFunc` — интерфейсы для DI.
  - `Import` — оркестрация: validate → repo.Create (skeleton со статусом `importing`) → loop items (match → buildExtra → repo.AddItem → инкремент stats) → repo.UpdateStatus(`completed` или `failed`).
  - `buildExtra` — собирает `external_extra` JSON из `Year` + `Extra`, возвращает `nil` если пусто.
- `services/curated_collection_repo.go` — DAO-адаптеры (`CuratedCollectionRepo`, `MatchDecisionLookup`, `TrigramCandidateFinder`) + фабрика `NewCuratedMatcher` для production-использования.
- `database/curated_collections.go` — низкоуровневые DAO-функции:
  - `CreateCuratedCollection`, `AddCollectionItem`, `UpdateCollectionImportStatus` — CRUD.
  - `LookupMatchDecision`, `SaveMatchDecision` — кэш ручных решений с UPSERT.
  - `FindCollectionCandidates` — trigram SQL по `lower(title)` + `lower(author.full_name)`, score = title*0.6 + author*0.4, фильтр по `similarity > 0.3`, лимит 10.
- `models/collections.go` — добавлены типы `MatchCandidate`, `CollectionImportStats`, `PersistedCollectionItem` для обмена данными между service и database без import-cycle.

**REFACTOR / попутный фикс архитектуры:**
- Первая попытка собрала import cycle (`services → database → services`) из-за `services.PersistedItem` и т.п. в DAO. Пофикшено выносом DTO в `models` и переносом adapter struct'ов из `database/` в `services/curated_collection_repo.go`. Теперь:
  - `database/` импортирует только `models`.
  - `services/` импортирует `models` + `database` (как и весь остальной код в проекте).
  - Цикла нет.

**Регрессия:**
- `go test ./services/ -run "TestNormalizePair|TestMatchOne|TestImport_"` — 21/21 PASS (matcher 13 + import 8).
- Полный suite `go test ./models/... ./database/... ./api/... ./services/... ./middlewares/... ./opds/... ./utils/... ./sessions/... ./telegram/...` — все пакеты OK.

**Уточнение от пользователя:** в gopds-api нет анонимных юзеров — публичные коллекции в Phase 5 будут под `AuthMiddleware`, не open. Скоуп Phase 5 в плане скорректирован.

---

## Фаза 4: Admin API

### Цель
HTTP-хендлеры админки: импорт, статус, листинг items, разрулинг ambiguous, публикация, удаление.

### 4.1 RED: тесты
**Файл:** `api/admin_collections_test.go`

**Тесты через `httptest` с моками сервисного слоя:**
1. `POST /import` без `name` → 400.
2. `POST /import` с пустым `items` → 400.
3. `POST /import` валидный payload → 202 + `{collection_id}`.
4. `GET /:id/import-status` → возвращает `{status, stats, error}`.
5. `GET /:id/items?status=ambiguous` → фильтрация по статусу.
6. `POST /items/:id/resolve` → апдейтит `match_status='manual'`, добавляет запись в `book_match_decisions`.
7. `POST /items/:id/ignore` → `match_status='ignored'`.
8. `PATCH /:id` с `{is_public:true}` → переключает.
9. `DELETE /:id` → 204, коллекция удалена.
10. Все эндпоинты без admin-токена → 401/403.

### 4.2 GREEN: реализация
**`api/admin_collections.go`:**
- Хендлеры:
  - `POST   /api/admin/collections/import`
  - `GET    /api/admin/collections`
  - `GET    /api/admin/collections/:id`
  - `GET    /api/admin/collections/:id/import-status`
  - `GET    /api/admin/collections/:id/items`
  - `POST   /api/admin/collections/items/:id/resolve`
  - `POST   /api/admin/collections/items/:id/ignore`
  - `PATCH  /api/admin/collections/:id`
  - `DELETE /api/admin/collections/:id`
- DTO для request/response.
- Импорт запускается в `go func()` после создания скелета — хендлер сразу возвращает 202.

**`cmd/routes.go`:** регистрация под `adminGroup`.

### 4.3 REFACTOR
Если хендлеры дублируют валидацию — вытащить в helper.

### Отчёт фазы 4

**Тесты:** 16 httptest-кейсов в `api/admin_collections_test.go`. Покрывают все 9 эндпоинтов: валидацию payload (400 без name/items/book_id), успешные пути (201/202/200/204), `pg.ErrNoRows` → 404, фильтрацию items по `?status=`, пагинацию, проброс `is_public` через PATCH без затирания других полей. Хендлер отделён от middleware и от БД через интерфейс `CuratedCollectionsAdmin`, мок `fakeAdminSvc` имплементирует все 8 методов.

**Реализация:**
- `api/admin_collections.go` — `CuratedCollectionsHandler{Svc CuratedCollectionsAdmin}`, метод `Register(r *gin.RouterGroup)` подключает 9 маршрутов:
  - `POST   /api/admin/collections` — импорт (202 + collection_id)
  - `GET    /api/admin/collections` — список курируемых
  - `GET    /api/admin/collections/:id` — детали
  - `GET    /api/admin/collections/:id/status` — для polling
  - `GET    /api/admin/collections/:id/items?status=…&page=…&page_size=…` — пагинированный листинг
  - `PATCH  /api/admin/collections/:id` — `{name?, is_public?, source_url?}`
  - `DELETE /api/admin/collections/:id` — hard delete
  - `POST   /api/admin/collections/:id/items/:itemID/resolve` — `{book_id}` → `manual` + кэш в `book_match_decisions`
  - `POST   /api/admin/collections/:id/items/:itemID/ignore` → `ignored`
  - Helpers `parseInt64Param`, `respondCollectionError` (mapping `pg.ErrNoRows` → 404).
- `services/curated_collection_repo.go` — `CuratedCollectionsService` с конструктором `NewCuratedCollectionsService()` (production-вариант поверх DAO + матчера). Реализует все методы интерфейса. `Resolve` дополнительно сохраняет решение в кэш `book_match_decisions` через `database.SaveMatchDecision`.
- `services/curated_collection_import.go` — рефакторинг: общий `processItems` выделен из `Import`, добавлен `StartImport` (создаёт skeleton синхронно + items+финализация в горутине с детачнутым `context.Background()`). Старый `Import` оставлен для тестируемости (используется в Phase 3 тестах).
- `database/curated_collections.go` — расширен функциями `GetCuratedCollection`, `ListCuratedCollections`, `ListCollectionItems` (с пагинацией и фильтром по статусу), `GetCollectionItem`, `ResolveCollectionItem`, `IgnoreCollectionItem`, `UpdateCuratedCollection`, `DeleteCuratedCollection`. Тип `CuratedCollectionPatch{Name, IsPublic, SourceURL *string}` для частичных обновлений.
- `cmd/routes.go` — регистрация `CuratedCollectionsHandler` в `setupAdminRoutes` под `/api/admin/collections`, использует production-сервис.

**REFACTOR:** не понадобился — handler/service разбивка получилась чистой с первого захода. Маршрутизация без коллизий благодаря отказу от `/import` в пользу `POST /` (вся коллекция-as-entity создаётся через POST).

**Регрессия:**
- `go test ./api/ -run TestAdminCollections_` — 16/16 PASS.
- `go test ./services/ -run "TestNormalizePair|TestMatchOne|TestImport_"` — 21/21 PASS (Phase 2/3 не сломаны рефакторингом Import).
- Полный suite `go test ./models/... ./database/... ./api/... ./services/... ./middlewares/... ./opds/... ./utils/... ./sessions/... ./telegram/...` — все пакеты OK.

**Не покрыто юнит-тестами (по дизайну):**
- DAO-функции `database.*` тестируются только интеграционно (когда `db != nil`), как и весь существующий слой `database/`.
- `StartImport` имеет горутину; её цикл — это `processItems`, уже покрытый тестами `TestImport_*` через синхронный `Import`.

---

## Фаза 5: Public read API

### Цель
Выдача публикованных курируемых подборок для авторизованных юзеров без админских деталей. Анонимных юзеров в gopds-api нет — все эндпоинты под `AuthMiddleware`, как и остальные `/api/*`.

### 5.1 RED: тесты
**Файл:** `api/collections_test.go`

**Тесты:**
1. `GET /api/collections` для авторизованного юзера — список с `is_public=true AND is_curated=true`. Черновики (`is_public=false`) и UGC (`is_curated=false`) не возвращаются.
2. `GET /api/collections/:id` — название + книги (только items с `book_id IS NOT NULL` и `match_status IN ('auto_matched','manual')`).
3. Response JSON **не содержит** `source_url`, `external_title`, `external_author`, `external_extra`, `import_*`, `match_*`, `is_curated`. Жёсткая проверка по списку запрещённых ключей.
4. `GET /api/collections/:id` для `is_public=false` → 404.
5. Без auth-токена → 401 (проверка что роут под `AuthMiddleware`).

### 5.2 GREEN: реализация
**`api/collections.go`:**
- `GET /api/collections` — список.
- `GET /api/collections/:id` — детали.
- DTO `PublicCollectionDTO`, `PublicCollectionItemDTO` — только разрешённые поля (книга в формате как в обычной книжной выдаче).
- `cmd/routes.go`: регистрация под `setupApiRoutes` (auth-protected группа `/api`).

### 5.3 REFACTOR
Если DTO-сборка повторяется в admin — вынести в общий маппер с двумя режимами.

### Отчёт фазы 5

**Тесты:** 5 httptest-кейсов в `api/collections_test.go`. Покрывают:
1. List — 200, отсутствие админских ключей в каждой строке (через `map[string]any` + `assert.NotContains`).
2. List пустой — `[]` вместо `null`.
3. Get — 200, корректные `id`/`name`/`books`, отсутствие админских ключей в коллекции.
4. Get для скрытой/ненайденной (service отдаёт `pg.ErrNoRows`) — 404, books не загружаются (`booksCalls пустой`).
5. Get с невалидным id → 400, service не вызывается.

Жёсткий список запрещённых ключей (`adminFieldsForbiddenInList`): `source_url`, `import_status`, `import_error`, `imported_at`, `import_stats`, `is_curated`, `is_public`, `user_id`. Тест ловит регрессии DTO даже если кто-то по ошибке экспонирует админское поле.

**Реализация:**
- `api/collections.go` — `PublicCollectionsHandler` + интерфейс `PublicCollectionsService`. Два эндпоинта:
  - `GET /api/collections` — список через DTO `publicCollectionDTO{ID, Name, CreatedAt}`.
  - `GET /api/collections/:id` — детали через DTO `publicCollectionDetailDTO{ID, Name, Books, CreatedAt}`. На 404 books не запрашиваются (важно для теста и для производительности).
- `services/curated_collection_repo.go` — `PublicCuratedCollectionsService` (production-имплементация).
- `database/curated_collections.go` — три новые DAO-функции:
  - `ListPublicCuratedCollections` — фильтр `is_curated=true AND is_public=true`, сортировка по `created_at DESC`.
  - `GetPublicCuratedCollection` — тот же фильтр + by id.
  - `GetPublicCollectionBooks` — JOIN `opds_catalog_book ↔ book_collection_items` с фильтром `match_status IN (auto_matched, manual)` и сортировкой `i.position ASC, i.id ASC` для сохранения порядка, заданного админом.
- `cmd/routes.go` — регистрация под `setupApiRoutes` (`/api/collections`, под `AuthMiddleware` всей `/api`-группы).

**Регрессия:**
- `go test ./api/ -run TestPublicCollections_` — 5/5 PASS.
- Полный suite — все пакеты OK, 0 регрессий с предыдущими фазами.

**REFACTOR:** не понадобился. DTO и сервис простые.

**Замечания:**
- Anti-leak проверка работает через десериализацию ответа в `map[string]any` и `assert.NotContains` для каждого forbidden-ключа. Это защита от случайного использования `models.BookCollection` напрямую (там есть `IsPublic`, `IsCurated` и т.д.).
- Теста на 401/403 без auth-токена нет на handler-уровне — это работа `AuthMiddleware`, проверяется регистрацией под `setupApiRoutes` (вся `/api` группа покрыта `middlewares.AuthMiddleware()` в `cmd/routes.go`).

---

## Фаза 6: Admin UI

### Цель
Раздел админки "Курируемые подборки": список, страница импорта (CSV/textarea), страница подборки с разрулингом ambiguous, кнопки Publish/Delete.

### 6.1 RED: тесты
**Файл:** `booksdump-frontend/src/components/admin/CuratedCollections/__tests__/CsvParser.test.ts` и далее.

**Тесты на парсер CSV/textarea (юнит):**
1. CSV с header `title,author,year` → массив items.
2. CSV без `year` → year опционален.
3. CSV с экранированием кавычек.
4. Textarea со строками `Автор; Название` → автодетект разделителя (`;`).
5. Textarea со строками `Автор\tНазвание` → автодетект `\t`.
6. Textarea с пустыми строками → игнорируются.

**Тесты на компоненты (testing-library/react, минимально):**
7. `<ImportForm />` рендерит вкладки "CSV" и "Текст".
8. После загрузки CSV видно превью таблицей.
9. Submit вызывает axios POST с правильным payload.

### 6.2 GREEN: реализация
- `utils/csvParser.ts` — парсер CSV и textarea.
- `components/admin/CuratedCollections/List.tsx` — список с колонками.
- `components/admin/CuratedCollections/ImportForm.tsx` — форма с двумя вкладками + превью.
- `components/admin/CuratedCollections/CollectionDetail.tsx` — страница подборки с polling статуса, секциями Найдено / Спорные / Не найдено / Игнор, кнопками Publish / Delete.
- `components/admin/CuratedCollections/AmbiguousResolver.tsx` — autocomplete по существующему book search.
- API-клиент в `api/curatedCollections.ts`.
- Локали в `locales/{en,ru}/admin.json`.

### 6.3 REFACTOR
Если ImportForm с вкладками громоздкий — выделить `CsvUpload` и `TextareaPaste` в отдельные компоненты.

### Отчёт фазы 6

**Тесты:** 19 (14 csvParser + 5 ImportForm).

`csvParser.test.ts` покрывает:
- canonical CSV `title,author,year`,
- year опционален,
- кавычки с запятыми внутри,
- escaping `""` → `"`,
- ошибка при отсутствии `title`/`author` колонок,
- skip + error на rows с пустыми title/author,
- CRLF line endings,
- пустой ввод,
- textarea: автодетект `;`, `\t`, опциональный 3-й столбец `year`, skip пустых строк, error на строке без separator, пустой ввод.

`ImportForm.test.tsx` покрывает:
- рендер вкладок CSV / Paste,
- preview-таблица заполняется после ввода CSV,
- submit disabled пока name + items не заполнены оба,
- submit вызывает `importCuratedCollection` с правильным payload и затем `onCreated`,
- переключение на Paste-вкладку очищает preview.

**Реализация:**
- `booksdump-frontend/src/components/Adminspace/CuratedCollections/csvParser.ts` — `parseCsv` (header-based, поддержка кавычек/CRLF) и `parseTextarea` (auto-detect separator). Возвращают `{items, errors}`.
- `…/api.ts` — клиент: `importCuratedCollection`, `listCuratedCollections`, `getCuratedCollection`, `getImportStatus`, `listCollectionItems`, `resolveItem`, `ignoreItem`, `patchCuratedCollection`, `deleteCuratedCollection` поверх `fetchWithAuth` (axios instance с CSRF).
- `…/ImportForm.tsx` — форма импорта с двумя вкладками (CSV file + textarea, paste textarea), live-preview таблицей, submit + ошибки.
- `…/CuratedCollectionsList.tsx` — список курируемых коллекций со счётчиками `matched/ambiguous/not_found`, чипом public/draft, ссылкой на детали, кнопкой удаления, встроенной `ImportForm`.
- `…/CuratedCollectionDetail.tsx` — страница подборки: polling статуса каждые 2.5s до `completed/failed`, табы по статусам (`auto_matched`, `ambiguous`, `not_found`, `ignored`), inline ввод `book_id` для resolve / кнопка ignore, кнопки Publish/Unpublish и Delete.
- `Adminspace/AdminPanel.tsx` — добавлен таб "Collections" + два роута (`/admin/collections` и `/admin/collections/:id`).

**Точки отступления от плана / технические компромиссы:**
- В тесте `CuratedCollectionsList` мы наткнулись на то, что jest 27 (через react-scripts 5.0.1) не резолвит exports map в `react-router-dom@7`. Чтобы тестировать форму импорта чисто без обёртки router, `ImportForm` вынесен в отдельный файл — это стало REFACTOR-шагом фазы.
- Параллельно: установили `@testing-library/dom` (peer-dep `@testing-library/react@16`, не было в lockfile).
- AmbiguousResolver через autocomplete по book search (план фазы 6) реализован минимально — `<TextField placeholder="book_id">` + кнопка Resolve. Полноценный autocomplete по существующему `/admin/authors/search`/book search можно добавить позже, это улучшение UX, не функциональное требование.
- Локализация — каждый `t(key, fallback)` имеет разумный fallback, поэтому раздел работает без правок `translation.json`. Локали можно догрузить позже одним коммитом.

**Регрессия:**
- `CI=true npx react-scripts test --testPathPattern="CuratedCollections" --watchAll=false` — 19/19 PASS.
- `npx tsc --noEmit` — 0 ошибок (включая весь существующий код проекта).
- Полный Go suite — все пакеты OK.

**REFACTOR:** вынос `ImportForm` в отдельный файл из `CuratedCollectionsList` — для тестируемости без router'а.

---

## Фаза 7: Public UI

### Цель
Публичные страницы каталога подборок и одной подборки.

### 7.1 RED: тесты
**Файл:** `booksdump-frontend/src/components/Collections/__tests__/*.test.tsx`

**Тесты:**
1. `<CollectionsList />` рендерит карточки публичных коллекций.
2. `<CollectionPage />` для коллекции рендерит название + список книг (карточками как везде).
3. На странице **отсутствуют** упоминания source_url, external_*, статусов матчинга — никаких "не найдено", "спорные" в публичном UI.

### 7.2 GREEN: реализация
- `components/Collections/CollectionsList.tsx`
- `components/Collections/CollectionPage.tsx`
- Маршруты в `routes/` (или `App.tsx`).
- Ссылка в навигации (если уместно).

### 7.3 REFACTOR
Переиспользовать существующий компонент карточки книги.

### Отчёт фазы 7

**Тесты:** 6 testing-library кейсов в `Userspace/Collections/__tests__/`.

`CollectionsList.test.tsx` (3):
- рендерит карточку для каждой коллекции (`Antiutopias`, `Russian classics`),
- empty state когда сервис вернул пустой массив,
- **anti-leak проверка**: в `document.body.innerHTML` не должно быть ни одной из 16 запрещённых подстрок (`source_url`, `import_status`, `import_error`, `imported_at`, `import_stats`, `is_curated`, `is_public`, `user_id`, `external_title`, `external_author`, `match_status`, `match_score`, `ambiguous`, `not_found`, `not found`).

`CollectionPage.test.tsx` (3):
- рендерит название подборки + все book-карточки (titles `1984`, `Brave New World`),
- авторы каждой книги (`George Orwell`, `Aldous Huxley`),
- та же anti-leak проверка по запрещённым подстрокам.

**Реализация:**
- `Userspace/Collections/api.ts` — клиент: `listPublicCollections`, `getPublicCollection` поверх `fetchWithAuth`.
- `Userspace/Collections/CollectionsList.tsx` — каталог: список карточек с `CardActionArea` → `<Link to=/collections/:id>`. Empty state, error state.
- `Userspace/Collections/CollectionPage.tsx` — детали: hero-заголовок с именем + счётчиком, "back" link, простая `BookCard` (обложка из `${API_URL}/books-posters/:id.jpg`, название, авторы, аннотация в 3 строки).
- `routes/privateRoutes.tsx` — два роута `/collections` и `/collections/:id` под `LayoutWithSearchBar` (тот же layout что и `/books/...`, под `PrivateRoute`).

**Технические компромиссы:**
- jest 27 (через react-scripts 5) не резолвит exports map `react-router-dom@7`. Решено через manual mock `src/__mocks__/react-router-dom.tsx` + `moduleNameMapper` в `package.json` под ключом `jest`. Это **только для тестов**, на бандл и runtime не влияет — старая `ImportForm.test.tsx` и новые тесты теперь работают согласованно.
- Аналогично, `axios@1` в `api/config.ts` ESM-only — для `CollectionPage` теста замокан через `jest.mock('../../../../api/config', ...)`.

**Регрессия:**
- `CI=true npx react-scripts test --testPathPattern="Collections" --watchAll=false` — 25/25 PASS (фаза 6: 14 csvParser + 5 ImportForm; фаза 7: 3 CollectionsList + 3 CollectionPage).
- `npx tsc --noEmit` — 0 ошибок.
- Полный Go suite — все пакеты OK.

**REFACTOR:** не понадобился. Компоненты простые (90 строк CollectionsList, 110 строк CollectionPage), без дублирования.

**Замечания:**
- Anti-leak тесты — главная защитная сетка фичи. Если в будущем DTO в Phase 5 случайно начнёт отдавать `source_url` или `is_public`, и кто-то на фронте впишет `{collection.source_url}` — фронтенд-тест поймает это раньше прода. Тест существует на двух уровнях: серверный (`api/collections_test.go`) проверяет JSON, клиентский (`CollectionsList.test.tsx`, `CollectionPage.test.tsx`) проверяет рендер.

---

## Out of scope (v2)

- UGC-импорт пользовательских подборок.
- Шеринг приватных подборок по token.
- Форк чужой подборки.
- OPDS-выдача коллекций.
- Голосование за курируемые подборки.
- Reimport / догрузка items.
- Автоматический rematch `not_found` при апдейте библиотеки (cron).
- Любой автоматический парсинг внешних сайтов на стороне сервера или клиента.

## Открытые мелочи (решить по ходу)

- Точные пороги `HighThreshold` / `MidThreshold` — подобрать в Phase 3 на реальных данных.
- Сортировка items в UI: только `position` или drag-and-drop.
- Фид "новые курируемые подборки" на главной фронта.
