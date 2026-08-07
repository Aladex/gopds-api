-- Book search indexes over the canonical normalized title.
--
-- Both indexes must match the predicate expression exactly: a query filtering
-- on public.search_normalize(title) can only use an index built over that
-- same expression. The GIN serves trigram/word-similarity and %substring%
-- candidates, the pattern-ops B-tree serves prefix lanes only.
SET LOCAL lock_timeout = '5s';

CREATE INDEX IF NOT EXISTS idx_book_title_search_norm_trgm
    ON public.opds_catalog_book
    USING gin (public.search_normalize(title) gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_book_title_search_norm_pattern
    ON public.opds_catalog_book
    USING btree (public.search_normalize(title) text_pattern_ops);
