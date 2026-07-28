-- Extend book_collections with administrator-managed curated collections.
-- user_id becomes nullable because curated collections do not belong to a specific user.
ALTER TABLE public.book_collections
    ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE public.book_collections
    ADD COLUMN IF NOT EXISTS is_curated BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS source_url TEXT,
    ADD COLUMN IF NOT EXISTS import_status VARCHAR(32),
    ADD COLUMN IF NOT EXISTS import_error TEXT,
    ADD COLUMN IF NOT EXISTS imported_at TIMESTAMP WITH TIME ZONE,
    ADD COLUMN IF NOT EXISTS import_stats JSONB;

CREATE INDEX IF NOT EXISTS idx_book_collections_curated_public
    ON public.book_collections (is_curated, is_public);

-- Collection items. book_id is nullable: imports keep unmatched and ambiguous
-- entries as external title/author values without linking them to a local book.
-- Manual resolution sets book_id and changes match_status to `manual`.
CREATE TABLE IF NOT EXISTS public.book_collection_items (
    id BIGSERIAL PRIMARY KEY,
    collection_id INTEGER NOT NULL REFERENCES public.book_collections(id) ON DELETE CASCADE,
    book_id INTEGER REFERENCES public.opds_catalog_book(id) ON DELETE SET NULL,
    external_title TEXT NOT NULL,
    external_author TEXT,
    external_extra JSONB,
    match_status VARCHAR(32) NOT NULL,
    match_score REAL,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_book_collection_items_collection_position
    ON public.book_collection_items (collection_id, position);
CREATE INDEX IF NOT EXISTS idx_book_collection_items_match_status
    ON public.book_collection_items (match_status);
CREATE INDEX IF NOT EXISTS idx_book_collection_items_book_id
    ON public.book_collection_items (book_id);

DROP TRIGGER IF EXISTS update_book_collection_items_updated_at ON public.book_collection_items;
CREATE TRIGGER update_book_collection_items_updated_at
    BEFORE UPDATE ON public.book_collection_items
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Cache manual decisions as normalized (author, title) -> book_id mappings.
-- Decisions are reused across collection imports: after an administrator maps
-- "Толстой / Война и мир" to a specific book_id, the same pair in a later
-- collection receives a `manual` match without another manual selection.
CREATE TABLE IF NOT EXISTS public.book_match_decisions (
    id BIGSERIAL PRIMARY KEY,
    author_norm TEXT NOT NULL,
    title_norm TEXT NOT NULL,
    book_id INTEGER NOT NULL REFERENCES public.opds_catalog_book(id) ON DELETE CASCADE,
    decided_by_user_id INTEGER REFERENCES public.auth_user(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE (author_norm, title_norm)
);

-- Trigram index on title for matching normalized book titles.
-- An equivalent author index was added by migration 09.
-- Check the index definition rather than its name so that:
--   * fresh installations create the index;
--   * production databases with an equivalent legacy Django index named
--     `idx_book_title_trgm` avoid a duplicate, blocking CREATE INDEX on the
--     largest table.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = 'public'
          AND tablename = 'opds_catalog_book'
          AND indexdef ILIKE '%gin_trgm_ops%'
          AND indexdef ILIKE '%lower(title%'
    ) THEN
        CREATE INDEX opds_catalog_book_title_trgm_idx
            ON public.opds_catalog_book USING GIN (lower(title) gin_trgm_ops);
    END IF;
END$$;
