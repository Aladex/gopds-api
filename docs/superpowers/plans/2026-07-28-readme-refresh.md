# README Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the overstated and repetitive README with a concise, fact-checked English guide, and translate explanatory Russian code comments to English without changing user-facing content.

**Architecture:** Documentation-only changes. `README.md` becomes the single public overview and operational entry point. Source edits are limited to comment text; executable statements, strings, fixtures, locales, prompts, and examples remain unchanged.

**Tech Stack:** Markdown, Go, TypeScript/TSX, HTML, CSS, SQL, Make, Docker Compose.

## Global Constraints

- Keep `README.md` in English.
- Do not change executable behavior.
- Do not change Russian user-facing strings, locale files, prompts, test data, book titles, author names, or search examples.
- Translate only explanatory comments whose prose is Russian.
- Do not add dependencies, Docker images, commits, tags, or pushes.
- Do not claim formal EPUB or OPDS compliance without an external conformance check.
- Do not claim benchmark results that have no reproducible benchmark in the repository.

---

### Task 1: Rewrite the public README

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: current Make targets, configuration schema, Docker Compose manifest, routes, and implemented feature set.
- Produces: a concise setup and development guide that matches the repository.

- [ ] **Step 1: Replace the repeated feature catalogue**

Keep a short overview of implemented catalogue, conversion, OPDS, administration, frontend, and Telegram capabilities. Remove repeated roadmap entries, unsupported reading-list claims, and marketing adjectives.

- [ ] **Step 2: Correct technical descriptions**

Describe search as substring and PostgreSQL trigram similarity search. Describe the EPUB converter as an in-process Go converter targeting EPUB 3 with NCX compatibility. Describe themes as session-scoped and Swagger as covering annotated REST endpoints.

- [ ] **Step 3: Document runnable workflows**

Document `make build`, `make bootstrap`, `make dev`, frontend commands, tests, linting, and the configuration changes required when using Docker Compose. Explicitly document `OPENAI_API_KEY`, `OPENAI_MODEL`, kindlegen, and migration limitations.

- [ ] **Step 4: Remove stale sections**

Remove the duplicated format-conversion description, completed roadmap, unsupported performance claims, outdated API path groups, and generic contributing boilerplate that adds no project-specific guidance.

### Task 2: Translate Russian explanatory comments

**Files:**
- Modify: `telegram/conversation.go`
- Modify: `database_migrations/18-add-curated-collections.sql`
- Modify: `booksdump-frontend/index.html`
- Modify: `booksdump-frontend/src/transitions.css`
- Modify: `booksdump-frontend/src/app/App.tsx`
- Modify: `booksdump-frontend/src/context/AuthContext.tsx`
- Modify: `booksdump-frontend/src/index.tsx`

**Interfaces:**
- Consumes: the existing source exactly as written.
- Produces: English-only explanatory comments with byte-for-byte unchanged executable statements and user-facing content.

- [ ] **Step 1: Translate backend and migration comments**

Translate the Redis conversation metadata comment and curated-collection migration explanations while preserving identifiers, SQL, quoted example titles, and migration semantics.

- [ ] **Step 2: Translate frontend comments**

Translate HTML, CSS, TSX, and inline comments about FOUC, rendering, providers, authentication, favourites, language changes, logout, and StrictMode.

- [ ] **Step 3: Preserve content exceptions**

Keep Cyrillic in Russian UI labels, locale files, Telegram responses, LLM prompt examples, fixtures, tests, and English comments that quote Russian book titles or search input.

### Task 3: Verify the documentation-only change

**Files:**
- Verify: `README.md`
- Verify: all files modified by Task 2

**Interfaces:**
- Consumes: the final working tree.
- Produces: evidence that formatting and tests pass and no Russian explanatory comments remain.

- [ ] **Step 1: Scan comments**

Run targeted ripgrep searches for Cyrillic after comment delimiters. Manually confirm every remaining match is quoted content or a user-facing example rather than explanatory prose.

- [ ] **Step 2: Check formatting**

Run `make fmt-frontend-check`.

- [ ] **Step 3: Run focused tests**

Run `go test -count=1 ./telegram` and `go test -count=1 ./...` only if the repository's generated inputs are already present. Run `cd booksdump-frontend && yarn test`.

- [ ] **Step 4: Validate README commands and links**

Run `docker compose config --quiet`, inspect Make targets referenced by README, and verify repository-relative README links exist.

- [ ] **Step 5: Review the diff**

Confirm the diff contains no executable code changes, no generated files, and no accidental edits to Russian content.
