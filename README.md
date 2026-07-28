<p align="center">
  <img src="https://raw.githubusercontent.com/Aladex/gopds-api/master/logo/logo.png" width="350" alt="GoPDS">
</p>

# GoPDS

GoPDS is a self-hosted ebook library with a Go API, a React interface, and
OPDS feeds for e-readers. It manages FB2 books stored in ZIP archives and
converts them to EPUB or MOBI on demand.

## Features

- Catalogue browsing and search by book, author, series, genre, and language
- Personal favorites and administrator-managed curated collections
- Invite registration, email activation, password reset, and Redis sessions
- Authenticated OPDS 1.x-style feeds with search and OpenSearch
- ZIP/FB2 scanning, cover extraction, duplicate and language detection
- Scan and conversion progress over WebSocket
- In-process FB2-to-EPUB 3 conversion with EPUB 2 NCX compatibility
- MOBI conversion through the bundled KindleGen executable
- Administration for users, invites, genres, collections, covers, and scanning
- Per-user Telegram bots with search, favorites, collections, and downloads
- Optional OpenAI-assisted Telegram search and book language detection
- Responsive English/Russian interface with light and dark themes

Search uses PostgreSQL substring matching and `pg_trgm` similarity, not
PostgreSQL full-text search.

## Stack

- Go 1.26, Gin, go-pg, PostgreSQL 15, Redis
- React 19, TypeScript, Vite 8, Tailwind CSS 4, Radix UI
- JWT, CSRF protection, WebSocket, Swagger UI
- Docker and Docker Compose

## Quick start

Requirements: Docker Engine and the Compose plugin.

Copy the example configuration:

```bash
cp config.yaml.example config.yaml
```

For the supplied Compose file, update at least these fields in `config.yaml`:

```yaml
project_domain: "127.0.0.1"
project_url: "http://localhost:8085"
secret_key: "replace-with-a-random-secret"

server:
  host: "0.0.0.0"
  port: 8085

postgres:
  dbuser: "gopds"
  dbpass: "gopds_password"
  dbname: "gopds"
  dbhost: "postgres:5432"

redis:
  host: "redis"
  port: 6379

sessions:
  key: "replace-with-a-random-session-key"
  refresh: "replace-with-a-random-refresh-key"

app:
  devel_mode: true
  cdn: "http://localhost:8085"
  files_path: "/gopds/books"
  users_path: "/gopds/users"
  book_cdn_key: "replace-with-a-random-book-key"
  posters_path: "/gopds/covers"
  file_book_cdn: "http://localhost:8085"
  mobi_conversion_dir: "/gopds/mobi"
```

Keep the remaining example sections if you need scanning, SMTP, or donation
settings. The mounted file is the source of truth for the current Compose
setup.

Start the dependencies first to avoid an application/database startup race:

```bash
docker compose up -d --wait postgres redis
docker compose up -d --build gopds-api
curl http://127.0.0.1:8085/api/status
```

Open <http://127.0.0.1:8085>. Stop the stack with:

```bash
docker compose down
```

PostgreSQL, Redis, books, and covers use named volumes. `/gopds/users` and
`/gopds/mobi` are writable but are not persisted by the current Compose file.
Do not use the example secrets or database password in a public deployment.

## Configuration

GoPDS reads `config.yaml` from its working directory. Settings can also be
provided as environment variables using the `GOPDS_` prefix and underscores
for nested keys:

```bash
export GOPDS_POSTGRES_DBHOST=127.0.0.1:5432
export GOPDS_POSTGRES_DBUSER=gopds
export GOPDS_POSTGRES_DBPASS=gopds_password
export GOPDS_POSTGRES_DBNAME=gopds
export GOPDS_REDIS_HOST=127.0.0.1
export GOPDS_SESSIONS_KEY=replace-me
export GOPDS_SESSIONS_REFRESH=replace-me
export GOPDS_SECRET_KEY=replace-me
```

See [`config.yaml.example`](config.yaml.example) for the configuration shape.

- SMTP is required for activation and password-reset emails.
- `OPENAI_API_KEY` enables OpenAI features; `OPENAI_MODEL` selects the model
  and currently defaults to `gpt-4o-mini`.
- Telegram webhooks require a publicly reachable HTTPS base URL.
- `app.allowed_origins` adds browser origins accepted by CORS and WebSocket
  origin checks.

## Development

Requirements: Go 1.26.5, Node.js 24, Yarn Classic, PostgreSQL, and Redis.

Prepare generated Swagger files and the embedded frontend placeholder, then
run the backend:

```bash
make bootstrap
make dev
```

Run the Vite frontend in another terminal:

```bash
cd booksdump-frontend
yarn install --frozen-lockfile
VITE_API_URL=http://127.0.0.1:8085 yarn start
```

Open <http://127.0.0.1:3000> to match the default backend CORS origin.
`VITE_API_URL` configures HTTP requests, but WebSocket connections use the
page origin; live progress therefore needs a same-origin build or a
development proxy.

Build the frontend, Swagger package, and `bin/gopds`:

```bash
make build
```

## Tests and quality

```bash
make test-backend       # Short Go suite; no database required
make test-frontend      # Vitest suite
make verify             # Frontend build, Go build, tests, and coverage
make test-integration   # Full Go suite against PostgreSQL
make lint-new           # Go lint for changes relative to the lint base
make lint-frontend-new  # ESLint errors in changed frontend files
make fmt-frontend-check # Prettier check
make security           # gosec
```

`make lint` and `make lint-frontend` inspect the whole repository and may
report pre-existing backlog. CI uses the change-scoped lint targets.

The optional development dataset helpers are:

```bash
make db-dump  # Reads the production catalogue; requires expected kubectl access
make db-reset # Replaces the local Compose database and creates synthetic users
```

Inspect their scripts and target configuration before running either command.

## Database migrations

Files in `database_migrations/` run in filename order. Fresh Compose database
volumes apply them through PostgreSQL initialization. For an existing database:

```bash
make migrate-plan # Preview pending files
make migrate-up   # Apply pending files
```

The runner records applied files in `schema_migrations` and executes each new
file in its own transaction. A database created before the ledger is baselined
on the first run instead of replaying its schema. Migrations are forward-only;
to reverse a change, add and test a new migration.

## API and OPDS

- Status: <http://127.0.0.1:8085/api/status>
- Swagger UI: <http://127.0.0.1:8085/swagger/index.html>
- OPDS root: <http://127.0.0.1:8085/opds/>
- OpenSearch: <http://127.0.0.1:8085/opds-opensearch.xml>

OPDS uses HTTP Basic authentication. Swagger covers only annotated REST
handlers; route registration under `cmd/gopds/` and the individual packages is
the complete source of truth.

## Repository layout

```text
cmd/gopds/              Application entry point and routes
cmd/migrate/            Database migration command
api/                    REST and WebSocket handlers
opds/                   OPDS feeds
services/               Application services
database/               Database access
database_migrations/    Ordered SQL migrations
internal/converter/     FB2-to-EPUB converter
internal/swaggerdocs/   Generated Swagger package
telegram/               Telegram integration
booksdump-frontend/     React application
scripts/                Development database helpers
```

## License

[MIT](LICENSE)
