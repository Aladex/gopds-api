-- The author line of every book as a read model, for search and sorting by the
-- names the author layer gives (read-path design §3.2–§3.3, phase 3).
--
--  1. book_author_display: one row per name of a book's author line, in line
--     order, exactly as database.BookAuthorDisplay decides it — the current
--     snapshot's author credits, or the legacy authors when the book stays on
--     them — with the key it sorts by, the key it is searched by
--     (search_normalize of the name, the normalization every search uses) and
--     the legacy author the name links to. A book whose line names no one has
--     one row of source 'none', so every book of the catalog has a first row
--     and a list sorted by author is one walk of an index. It is derived
--     data: the application rebuilds a book's rows from the layer and the
--     legacy catalog, and nothing reads it for display.
--  2. book_author_display_dirty: an append-only queue of books whose line may
--     have changed. Every writer of the layer or of a book's legacy authors
--     adds its books in its own transaction, in the statement that makes the
--     change where it can; the read-model worker drains it a batch at a time.
--     No row is ever updated and there is no unique key, so a mark never waits
--     on another writer's mark, and thousands of marks are one INSERT.
--  3. book_author_display_reconcile: the one row of the full walk that
--     rebuilds every book a batch at a time. The first walk fills the model
--     (the backfill, in the background, never here); later walks run once a
--     day and count the books whose rows differed — the net under a write
--     path that forgot its mark.
--
-- Locks. The file creates new tables only and touches no existing one: no
-- foreign key reaches the catalog (a book's rows are deleted with the book by
-- the application, and a walk removes whatever outlived its book), so the
-- migration takes no lock an application writer holds and cannot wait on one.
-- lock_timeout still bounds it, failing fast should that ever change.

SET LOCAL lock_timeout = '5s';

CREATE TABLE public.book_author_display (
    book_id          bigint   NOT NULL,
    position         smallint NOT NULL,
    display          text     NOT NULL,
    -- NULL for a line that names no one: it sorts after every name.
    sort_key         text,
    search_key       text     NOT NULL,
    source           text     NOT NULL,
    legacy_author_id bigint,
    credit_id        bigint,
    -- A name of the layer, linked to a legacy author, with a word that
    -- author's catalog name lacks (a patronymic, a given name in full).
    extends_legacy   boolean  NOT NULL DEFAULT false,
    CONSTRAINT book_author_display_pkey PRIMARY KEY (book_id, position),
    CONSTRAINT book_author_display_position_check CHECK (position >= 0),
    CONSTRAINT book_author_display_source_check CHECK (source IN ('layer', 'legacy', 'none')),
    -- A line from the layer names credits; a legacy line names legacy
    -- authors; the row of a line that names no one names nothing.
    CONSTRAINT book_author_display_shape_check CHECK (
        (source = 'layer' AND credit_id IS NOT NULL AND sort_key IS NOT NULL)
        OR (source = 'legacy' AND credit_id IS NULL AND legacy_author_id IS NOT NULL AND sort_key IS NOT NULL)
        OR (source = 'none' AND position = 0 AND display = '' AND sort_key IS NULL
            AND credit_id IS NULL AND legacy_author_id IS NULL)),
    CONSTRAINT book_author_display_extends_check CHECK (
        NOT extends_legacy OR (source = 'layer' AND legacy_author_id IS NOT NULL))
);

COMMENT ON TABLE public.book_author_display IS
    'Read model of each book''s author line (derived; rebuilt by the application from the author layer and the legacy catalog).';

-- Search over the names that can find what the legacy names do not: a
-- layer name linked to no one, or one that says more than its legacy
-- author's catalog name. Every other name's words are its legacy author's,
-- which the legacy author index (22-author-search-index.sql) already finds;
-- leaving them out keeps the index to the names that matter — on the
-- production catalog about a third of the lines, which carry a patronymic
-- the catalog dropped. Book search by author (whose EXISTS the planner turns
-- into a hashed subplan over every matching name) and the author search and
-- picker (which find a legacy author through the names that extend it) both
-- read it, with the operator class the legacy author index uses.
CREATE INDEX book_author_display_search_key_trgm
    ON public.book_author_display USING gin (search_key gin_trgm_ops)
    WHERE source = 'layer' AND (legacy_author_id IS NULL OR extends_legacy);

-- Sorting a list by author sorts by the first name of each line, the newest
-- book first among one name, and the lines that name no one last: the order
-- of this index, so a page is read off it rather than sorted.
CREATE INDEX book_author_display_first_sort_idx
    ON public.book_author_display (sort_key ASC NULLS LAST, book_id DESC)
    WHERE position = 0;

-- The names of the model that extend their legacy author, once each, with
-- the number of books whose line carries them: what the author search and
-- the picker read to find a legacy author by a patronymic — a row per name
-- rather than per book keeps that lookup to the size of the authors' own
-- index. Rebuilds keep it by counts in their own transaction; the walk
-- recounts it from the model.
CREATE TABLE public.book_author_display_name (
    legacy_author_id bigint  NOT NULL,
    search_key       text    NOT NULL,
    books            integer NOT NULL,
    CONSTRAINT book_author_display_name_pkey PRIMARY KEY (legacy_author_id, search_key),
    CONSTRAINT book_author_display_name_books_check CHECK (books >= 0)
);

CREATE INDEX book_author_display_name_search_trgm
    ON public.book_author_display_name USING gin (search_key gin_trgm_ops);

CREATE TABLE public.book_author_display_dirty (
    id        bigserial NOT NULL,
    -- A writer names the book, or — a selection, written for thousands of
    -- credits at a time — just the credit, which the worker resolves to its
    -- book when it drains the mark, so the writer's statement joins nothing.
    book_id   bigint,
    credit_id bigint,
    CONSTRAINT book_author_display_dirty_pkey PRIMARY KEY (id),
    CONSTRAINT book_author_display_dirty_one_target CHECK (num_nonnulls(book_id, credit_id) = 1)
);

COMMENT ON TABLE public.book_author_display_dirty IS
    'Books whose author line may have changed, for the read-model worker; append-only, drained in batches.';

CREATE TABLE public.book_author_display_reconcile (
    singleton      boolean     NOT NULL DEFAULT true,
    -- The walk now running: the last book ID it rebuilt (NULL when no walk
    -- runs), when it started, and its counts so far.
    walk_cursor    bigint,
    walk_started   timestamptz,
    walk_differed  bigint      NOT NULL DEFAULT 0,
    walk_read      bigint      NOT NULL DEFAULT 0,
    -- The last finished walk: when it ended, how many books it found
    -- different from the rebuilt line, how many it read, and how many rows
    -- of the name index it had to correct.
    finished_at    timestamptz,
    books_differed bigint,
    books_read     bigint,
    names_differed bigint,
    CONSTRAINT book_author_display_reconcile_pkey PRIMARY KEY (singleton),
    CONSTRAINT book_author_display_reconcile_singleton CHECK (singleton)
);

INSERT INTO public.book_author_display_reconcile (singleton) VALUES (true);
