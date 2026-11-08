-- 83: book variants — which files of the catalog are one book, one card on
-- the site, and how the files inside a card rank (design/book-variants). The
-- rule is internal/bookvariants; these tables keep what it decided.
--
--   * book_variant_member: one row per visible book, with the card it is in
--     (group_key), the base key the rule gave it ('' for a book that is a card
--     of its own), its FB2 document (lineage), whether it is that document's
--     newest revision in the card, and its rank (1 = the default variant).
--     part_entity and part_lang name the partition the book was built in: the
--     first entity of its base and its language. A partition holds every book
--     of each of its bases, so it is the unit a rebuild takes. walk is the walk
--     that last rebuilt the row.
--   * book_variant_group: one row per card: its language, files, kind, what it
--     says it holds (shown_count), its newest file and the feed's mark.
--   * book_variant_dirty: the books whose card may be stale. The author display
--     drain marks every book it drains; a rescan, a series edit, a duplicate
--     hide and a deletion mark theirs. The worker takes them.
--   * book_variant_walk: the one row of the walk over every book. The first walk
--     fills the tables, in the background and not here; later walks run daily,
--     the net under whatever changes a book without marking it.
--
-- Derived data: no foreign key reaches the catalog, the worker removes what
-- outlived its book.
--
-- Locks: new tables only. No existing table is touched, so this waits on no
-- writer; lock_timeout bounds it should that ever change.

SET LOCAL lock_timeout = '5s';

CREATE TABLE public.book_variant_member (
    book_id      bigint  NOT NULL,
    group_key    uuid    NOT NULL,
    base_key     text    NOT NULL,
    part_entity  uuid,
    part_lang    text,
    lineage      text    NOT NULL,
    latest       boolean NOT NULL,
    variant_rank integer NOT NULL,
    tag          text    NOT NULL DEFAULT '',
    walk         bigint  NOT NULL DEFAULT 0,
    CONSTRAINT book_variant_member_pkey PRIMARY KEY (book_id),
    CONSTRAINT book_variant_member_rank_check CHECK (variant_rank >= 1),
    CONSTRAINT book_variant_member_part_check CHECK ((base_key = '') = (part_entity IS NULL AND part_lang IS NULL))
);

CREATE INDEX book_variant_member_group_idx ON public.book_variant_member (group_key, variant_rank);
CREATE INDEX book_variant_member_part_idx ON public.book_variant_member (part_entity, part_lang)
    WHERE part_entity IS NOT NULL;

CREATE TABLE public.book_variant_group (
    group_key      uuid    NOT NULL,
    lang           text    NOT NULL,
    files          integer NOT NULL,
    kind           text    NOT NULL,
    shown_count    integer NOT NULL,
    newest_book_id bigint  NOT NULL,
    mark           text    NOT NULL DEFAULT '',
    CONSTRAINT book_variant_group_pkey PRIMARY KEY (group_key),
    CONSTRAINT book_variant_group_kind_check CHECK (kind IN ('single', 'variants', 'revisions')),
    CONSTRAINT book_variant_group_mark_check CHECK (mark IN ('', 'new_revision', 'new_variant')),
    CONSTRAINT book_variant_group_files_check CHECK (files >= 1 AND shown_count >= 1)
);

CREATE INDEX book_variant_group_feed_idx ON public.book_variant_group (newest_book_id DESC);
CREATE INDEX book_variant_group_lang_feed_idx ON public.book_variant_group (lang, newest_book_id DESC);

CREATE TABLE public.book_variant_dirty (
    book_id   bigint      NOT NULL,
    marked_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT book_variant_dirty_pkey PRIMARY KEY (book_id)
);

CREATE INDEX book_variant_dirty_marked_at_idx ON public.book_variant_dirty (marked_at);

CREATE TABLE public.book_variant_walk (
    only_row     boolean     NOT NULL DEFAULT true,
    -- The walk the next rebuild stamps; it grows by one when a walk finishes.
    walk_number  bigint      NOT NULL DEFAULT 1,
    -- The last book a running walk finished; NULL when no walk runs.
    walk_cursor  bigint,
    walk_read    bigint      NOT NULL DEFAULT 0,
    walk_skipped bigint      NOT NULL DEFAULT 0,
    finished_at  timestamptz,
    CONSTRAINT book_variant_walk_pkey PRIMARY KEY (only_row),
    CONSTRAINT book_variant_walk_only_row_check CHECK (only_row)
);

INSERT INTO public.book_variant_walk DEFAULT VALUES;
