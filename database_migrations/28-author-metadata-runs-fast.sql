-- Author metadata runs answer fast on a catalogue-sized run, and one broken
-- archive no longer stops the pass.
--
--  1. A full run starts without its items. The start records the run as
--     pending with a seed cursor and the catalogue size it expects; the run's
--     own extraction loop seeds the items in bounded batches, each batch
--     advancing the cursor in the transaction that inserts its items, and
--     starts the run once the catalogue is exhausted. The pending run holds
--     the single active slot (author_metadata_run_one_active) from the start.
--  2. Item counts by status are maintained by statement triggers on the run
--     items, into an append-only tally of signed deltas: every path that adds,
--     changes or deletes an item — seeding, claims, completion, exhaustion, a
--     retry, deleting books with their layer — is counted in its own
--     transaction (a TRUNCATE of the items empties it), and nothing takes a
--     lock another writer holds. The extraction loop folds a run's deltas into one row per status now and
--     then; the sum per status is the same before and after.
--  3. Partial indexes for the live parts of a run's status: the leases held
--     right now, the oldest pending item, and the books that failed.
--  4. Two per-book terminal statuses: archive_missing (the archive file is
--     not there) and archive_unreadable (it is there and does not open),
--     given only while other archives of the volume open.
--  5. The credit-scale figures of a run (local, review, credits, coverage,
--     storage) are stored with the time they were computed, so a status read
--     of a catalogue-sized run does not compute them inline. They are shown,
--     never trusted for a decision: an approval or a completion accounts the
--     credits exactly at the moment it is taken.
--  6. Deleting the book records of a broken archive is a durable request
--     (author_metadata_archive_deletion) the extraction loop works off in
--     bounded batches; and the two foreign keys that deletion checks without
--     an index get one (see the end of the file).
--
-- Locks. Every table this file locks is locked up front, NOWAIT: an
-- application writer holding one of them (a review action on the review
-- items, a worker on the run items) makes the migration fail at once with
-- 55P03 before it holds anything, so it never waits while holding a lock and
-- cannot close a lock cycle with a writer, which would otherwise end in a
-- deadlock with either side as the victim. The transaction rolls back whole,
-- the server exits before serving, and the next start retries the file. Once
-- the locks are held the file runs to the end without waiting (0.36 s on a
-- copy with 558,609 run items, measured in the task report); writers of
-- these tables wait that long. The tally starts from the rows as they stand
-- under that lock, so no change is counted twice or missed.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_metadata_run, public.author_metadata_run_item, public.author_metadata_run_item_attempt
    IN ACCESS EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.auth_user IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.contributor_review_item, public.book_match_decisions IN SHARE MODE NOWAIT;

ALTER TABLE public.author_metadata_run
    ADD COLUMN seed_cursor BIGINT,
    ADD COLUMN seed_target INTEGER,
    ADD CONSTRAINT author_metadata_run_seed_check CHECK (
        (seed_cursor IS NULL OR (seed_cursor >= 0 AND mode = 'full' AND status IN ('pending', 'failed_systemic')))
        AND (seed_target IS NULL OR seed_target >= 0));

ALTER TABLE public.author_metadata_run_item
    DROP CONSTRAINT author_metadata_run_item_status_check,
    ADD CONSTRAINT author_metadata_run_item_status_check CHECK (status IN (
        'pending', 'extracted', 'extracted_no_author', 'already_current', 'entry_missing', 'invalid_fb2',
        'unsupported_encoding', 'metadata_parse_failed', 'archive_missing', 'archive_unreadable'));

ALTER TABLE public.author_metadata_run_item_attempt
    DROP CONSTRAINT author_metadata_run_item_attempt_outcome_check,
    ADD CONSTRAINT author_metadata_run_item_attempt_outcome_check CHECK (outcome IS NULL OR outcome IN (
        'extracted', 'extracted_no_author', 'already_current', 'entry_missing', 'invalid_fb2',
        'unsupported_encoding', 'metadata_parse_failed', 'archive_missing', 'archive_unreadable'));

CREATE TABLE public.author_metadata_run_item_tally (
    run_id BIGINT NOT NULL,
    status TEXT NOT NULL,
    items BIGINT NOT NULL
);
CREATE INDEX author_metadata_run_item_tally_run_idx ON public.author_metadata_run_item_tally (run_id);

CREATE FUNCTION public.author_metadata_run_item_tally_count() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO public.author_metadata_run_item_tally (run_id, status, items)
        SELECT run_id, status, count(*) FROM new_items GROUP BY run_id, status;
    ELSIF TG_OP = 'DELETE' THEN
        INSERT INTO public.author_metadata_run_item_tally (run_id, status, items)
        SELECT run_id, status, -count(*) FROM old_items GROUP BY run_id, status;
    ELSE
        INSERT INTO public.author_metadata_run_item_tally (run_id, status, items)
        SELECT run_id, status, sum(delta) FROM (
            SELECT run_id, status, 1 AS delta FROM new_items
            UNION ALL
            SELECT run_id, status, -1 FROM old_items) changed
        GROUP BY run_id, status
        HAVING sum(delta) <> 0;
    END IF;
    RETURN NULL;
END
$$;

CREATE TRIGGER author_metadata_run_item_tally_insert AFTER INSERT ON public.author_metadata_run_item
    REFERENCING NEW TABLE AS new_items
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_metadata_run_item_tally_count();
CREATE TRIGGER author_metadata_run_item_tally_update AFTER UPDATE ON public.author_metadata_run_item
    REFERENCING OLD TABLE AS old_items NEW TABLE AS new_items
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_metadata_run_item_tally_count();
CREATE TRIGGER author_metadata_run_item_tally_delete AFTER DELETE ON public.author_metadata_run_item
    REFERENCING OLD TABLE AS old_items
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_metadata_run_item_tally_count();

-- A TRUNCATE fires no DELETE trigger: it empties the tally with the items.
CREATE FUNCTION public.author_metadata_run_item_tally_reset() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM public.author_metadata_run_item_tally;
    RETURN NULL;
END
$$;
CREATE TRIGGER author_metadata_run_item_tally_truncate AFTER TRUNCATE ON public.author_metadata_run_item
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_metadata_run_item_tally_reset();

INSERT INTO public.author_metadata_run_item_tally (run_id, status, items)
SELECT run_id, status, count(*) FROM public.author_metadata_run_item GROUP BY run_id, status;

CREATE INDEX author_metadata_run_item_pending_age_idx ON public.author_metadata_run_item (run_id, created_at)
    WHERE status = 'pending';
CREATE INDEX author_metadata_run_item_lease_idx ON public.author_metadata_run_item (run_id, lease_expires_at)
    WHERE status = 'pending' AND lease_expires_at IS NOT NULL;
CREATE INDEX author_metadata_run_item_failure_idx ON public.author_metadata_run_item (run_id, book_id)
    WHERE status IN ('entry_missing', 'invalid_fb2', 'unsupported_encoding', 'metadata_parse_failed',
                     'archive_missing', 'archive_unreadable');

CREATE TABLE public.author_metadata_run_aggregate (
    run_id BIGINT PRIMARY KEY REFERENCES public.author_metadata_run (id) ON DELETE RESTRICT,
    computed_at TIMESTAMPTZ NOT NULL,
    figures JSONB NOT NULL CHECK (jsonb_typeof(figures) = 'object')
);

-- One request to delete the catalogue records of an archive's books, worked
-- off in batches by the extraction loop. One pending request per archive: a
-- second click finds the first. A finished request stays as the record.
CREATE TABLE public.author_metadata_archive_deletion (
    id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    run_id BIGINT NOT NULL REFERENCES public.author_metadata_run (id) ON DELETE RESTRICT,
    archive TEXT NOT NULL CHECK (btrim(archive) <> ''),
    books_total INTEGER NOT NULL CHECK (books_total >= 0),
    books_deleted INTEGER NOT NULL DEFAULT 0 CHECK (books_deleted >= 0),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done')),
    requested_by_user_id BIGINT REFERENCES public.auth_user (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    CHECK ((status = 'done') = (finished_at IS NOT NULL))
);
CREATE UNIQUE INDEX author_metadata_archive_deletion_one_pending
    ON public.author_metadata_archive_deletion (archive) WHERE status = 'pending';

-- Deleting a book's credits checks the review items that may cite each one
-- (contributor_review_item_credit_fkey); only open items had an index on the
-- credit, so every deleted credit scanned the whole review table — 45 s of
-- the 24,396-book archive's deletion on a catalogue-sized copy, 2 s with this
-- index. Deleting a book checks book_match_decisions (ON DELETE CASCADE) the
-- same way: 4.1 s → 1.2 s for that archive.
CREATE INDEX contributor_review_item_credit_idx ON public.contributor_review_item (scope_credit_id)
    WHERE scope_credit_id IS NOT NULL;
CREATE INDEX book_match_decisions_book_id_idx ON public.book_match_decisions (book_id);
