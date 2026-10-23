-- 57: author entities in the read model (design E, phase E2), still in the
-- shadow: nothing a reader sees reads them yet.
--
--   * book_author_display.entity_ids — the entities a layer row's credit
--     names, in segment order; NULL is no link (a legacy or empty row, a
--     credit without an entity, or a credit of several people one of whom
--     has none, §6 item 5). GIN-indexed for "all books of an entity".
--   * book_author_display.nickname — the nickname of the credit's selected
--     result, a name variant the entity's names count beside the row's own.
--   * author_entity_name — from now on the read-model worker's (emptied
--     here, recounted by it), like
--     book_author_display_name: every row with entities counts its search
--     key (and its nickname's) into each of them, the daily walk recounts.
--     A trigram index for the entity search.
--   * author_entity_build_run.attempts — how many times a pass was planned:
--     a pass is written in one transaction, so an attempt that did not land
--     (a crash, a lost lease) wrote nothing and the next one plans again.
--   * Triggers on the two assignment tables mark the books of every credit
--     whose assignment changed, in the writer's transaction: the entity
--     build, the manual decisions to come (E5) and a retirement all mark
--     the read model the same way. Statement-level, with transition tables:
--     a batch of a thousand rows pays one INSERT of marks.
--
-- No backfill here: the rows get their entities as the worker rebuilds them
-- (the marks of the next build, or the daily walk).
--
-- Locks: the read model and the assignment tables, up front, NOWAIT, like
-- 28-56. The new GIN index reads a column that is NULL in every row.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.book_author_display, public.author_entity_assignment, public.author_entity_credit_assignment,
    public.author_entity_name, public.author_entity_build_run IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_entity_build_run
    ADD COLUMN attempts integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT author_entity_build_run_attempts_check CHECK (attempts >= 0);

ALTER TABLE public.book_author_display
    ADD COLUMN entity_ids uuid[],
    ADD COLUMN nickname text,
    ADD CONSTRAINT book_author_display_entity_ids_check CHECK (
        entity_ids IS NULL
        OR (source = 'layer' AND cardinality(entity_ids) > 0 AND array_position(entity_ids, NULL) IS NULL)),
    ADD CONSTRAINT book_author_display_nickname_check CHECK (nickname IS NULL OR (source = 'layer' AND nickname <> ''));

CREATE INDEX book_author_display_entity_ids_idx ON public.book_author_display USING gin (entity_ids);

-- The names E1's build wrote (keyed by its own normalization, not the one
-- search uses) go: the worker writes them from now on, and its marks and
-- walk count every row in. Nothing reads them yet.
TRUNCATE public.author_entity_name;

CREATE INDEX author_entity_name_search_key_trgm_idx ON public.author_entity_name USING gin (search_key gin_trgm_ops);

-- The credits a fingerprint assignment names: every author credit with that
-- fingerprint, whatever its snapshot (the worker resolves a credit of an old
-- snapshot to its book and finds it unchanged).
CREATE FUNCTION public.author_entity_mark_fingerprints() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        INSERT INTO public.book_author_display_dirty (credit_id)
        SELECT c.id FROM public.book_contributor_credit c
        WHERE c.role = 'author' AND c.source_fingerprint IN (SELECT DISTINCT source_fingerprint FROM changed_new);
    END IF;
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        INSERT INTO public.book_author_display_dirty (credit_id)
        SELECT c.id FROM public.book_contributor_credit c
        WHERE c.role = 'author' AND c.source_fingerprint IN (SELECT DISTINCT source_fingerprint FROM changed_old);
    END IF;
    RETURN NULL;
END
$$;

CREATE FUNCTION public.author_entity_mark_credits() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        INSERT INTO public.book_author_display_dirty (credit_id) SELECT DISTINCT credit_id FROM changed_new;
    END IF;
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        INSERT INTO public.book_author_display_dirty (credit_id) SELECT DISTINCT credit_id FROM changed_old;
    END IF;
    RETURN NULL;
END
$$;

-- One trigger per event: a trigger with transition tables fires for one.
CREATE TRIGGER author_entity_assignment_marks_insert
    AFTER INSERT ON public.author_entity_assignment
    REFERENCING NEW TABLE AS changed_new
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_mark_fingerprints();
CREATE TRIGGER author_entity_assignment_marks_update
    AFTER UPDATE ON public.author_entity_assignment
    REFERENCING OLD TABLE AS changed_old NEW TABLE AS changed_new
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_mark_fingerprints();
CREATE TRIGGER author_entity_assignment_marks_delete
    AFTER DELETE ON public.author_entity_assignment
    REFERENCING OLD TABLE AS changed_old
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_mark_fingerprints();

CREATE TRIGGER author_entity_credit_assignment_marks_insert
    AFTER INSERT ON public.author_entity_credit_assignment
    REFERENCING NEW TABLE AS changed_new
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_mark_credits();
CREATE TRIGGER author_entity_credit_assignment_marks_update
    AFTER UPDATE ON public.author_entity_credit_assignment
    REFERENCING OLD TABLE AS changed_old NEW TABLE AS changed_new
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_mark_credits();
CREATE TRIGGER author_entity_credit_assignment_marks_delete
    AFTER DELETE ON public.author_entity_credit_assignment
    REFERENCING OLD TABLE AS changed_old
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_entity_mark_credits();
