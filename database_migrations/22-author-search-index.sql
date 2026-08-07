-- Author search index over the canonical normalized full name.
--
-- Same rule as the book indexes: the index expression must be the exact
-- predicate expression, or the word-similarity lane cannot reach it.
SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_author_full_name_search_norm_trgm
    ON public.opds_catalog_author
    USING gin (public.search_normalize(full_name) gin_trgm_ops);
