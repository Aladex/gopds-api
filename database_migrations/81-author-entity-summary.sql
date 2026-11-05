-- 81: what the author search shows of each author beside its name: the genres,
-- titles and covers of its books, precomputed. A live aggregate per row of a
-- page is what entitySearchSQL was tuned to avoid (its comment records a plan of
-- ninety seconds), so the page reads this by primary key instead.
--
--   * author_entity_summary: one row per entity and language it has listed books
--     in, and one with lang = '' for all of them. '' is what the search's own
--     filter means by no language, so a book with an empty lang is in that row
--     alone: no filter can ask for it. names is the entity's name variants, in
--     the '' row only: the search finds an entity by any name whatever the
--     filter. Derived data: no foreign key reaches the entities or the catalog,
--     the worker takes out what outlived them.
--   * author_entity_summary_dirty: the entities whose summary may be stale, one
--     row each. The read model writes them in the transaction that changes an
--     author line, and the worker takes them.
--   * author_entity_summary_walk: the one row of the walk over every entity. The
--     first walk fills the table, in the background and not here: the server
--     must start, and the site works from an empty summary. Later walks run
--     daily, the net under whatever changes a book without marking it.
--
-- Locks: new tables only. No existing table is touched, so this waits on no
-- writer; lock_timeout bounds it should that ever change.

SET LOCAL lock_timeout = '5s';

CREATE TABLE public.author_entity_summary (
    entity_id uuid   NOT NULL,
    lang      text   NOT NULL,
    genres    text[] NOT NULL DEFAULT '{}',
    works     text[] NOT NULL DEFAULT '{}',
    covers    jsonb  NOT NULL DEFAULT '[]',
    names     text[] NOT NULL DEFAULT '{}',
    CONSTRAINT author_entity_summary_pkey PRIMARY KEY (entity_id, lang)
);

CREATE TABLE public.author_entity_summary_dirty (
    entity_id uuid        NOT NULL,
    marked_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT author_entity_summary_dirty_pkey PRIMARY KEY (entity_id)
);

CREATE INDEX author_entity_summary_dirty_marked_at_idx ON public.author_entity_summary_dirty (marked_at);

CREATE TABLE public.author_entity_summary_walk (
    only_row     boolean     NOT NULL DEFAULT true,
    -- The last entity a running walk finished; NULL when no walk runs.
    walk_cursor  uuid,
    walk_read    bigint      NOT NULL DEFAULT 0,
    walk_skipped bigint      NOT NULL DEFAULT 0,
    finished_at  timestamptz,
    CONSTRAINT author_entity_summary_walk_pkey PRIMARY KEY (only_row),
    CONSTRAINT author_entity_summary_walk_only_row_check CHECK (only_row)
);

INSERT INTO public.author_entity_summary_walk DEFAULT VALUES;
