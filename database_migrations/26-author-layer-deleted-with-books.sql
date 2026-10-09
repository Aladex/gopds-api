-- Deleting a book deletes its author metadata layer.
--
-- The layer's rows reference their book (snapshots, run items) and each
-- other with ON DELETE RESTRICT, and are immutable: their trigger refuses
-- every UPDATE outside the listed columns and every DELETE. Immutability
-- means "never edited", not "never removed with its book": when a book goes,
-- its layer goes with it, in the same transaction, through
-- author_layer_delete_books — and nothing else may delete a row of the layer.
--
-- The mechanism is a transaction-local setting the immutability trigger
-- honours for DELETE on the tables of a book's layer only:
--   * only author_layer_delete_books sets it, and it clears it again before
--     it returns, so later statements of the same transaction are refused as
--     before; a rollback discards it with everything else (set_config local);
--   * it never lets an UPDATE through, and never a DELETE on the policy, run,
--     pilot approval or job tables, which do not belong to one book;
--   * anyone able to set a setting can also drop a trigger: this guards the
--     application's own code paths, as the trigger always did, not against a
--     database owner.

SET LOCAL lock_timeout = '5s';

CREATE OR REPLACE FUNCTION public.author_metadata_reject_mutation() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    -- TG_ARGV is NULL, not empty, when the trigger has no arguments; a NULL
    -- here would turn both sides of the comparison into NULL and let any
    -- UPDATE through.
    mutable text[] := coalesce(TG_ARGV, '{}'::text[]);
BEGIN
    IF TG_OP = 'DELETE' THEN
        -- The one exception: author_layer_delete_books removing the layer of
        -- books being deleted.
        IF current_setting('gopds.author_layer_delete', true) = 'on'
           AND TG_TABLE_NAME IN ('book_metadata_snapshot', 'book_contributor_credit',
                                 'book_contributor_credit_selection', 'book_contributor_credit_selection_audit',
                                 'contributor_manual_override', 'contributor_review_item',
                                 'contributor_normalization_result',
                                 'author_metadata_run_item', 'author_metadata_run_item_attempt') THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'rows of % are immutable and cannot be deleted', TG_TABLE_NAME
            USING ERRCODE = 'restrict_violation';
    END IF;
    IF (to_jsonb(OLD) - mutable) IS DISTINCT FROM (to_jsonb(NEW) - mutable) THEN
        RAISE EXCEPTION 'rows of % are immutable; only % may change', TG_TABLE_NAME, mutable
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END
$$;

-- Deletes the author metadata layer of the given books, inside the caller's
-- transaction; the caller deletes the books themselves afterwards.
--
-- Removed: every snapshot of the books and its credits; the credits'
-- resolutions and their audit; overrides and review items scoped to those
-- credits; review items scoped to a source fingerprint no other author credit
-- carries any more; the manual results only the removed overrides and review
-- items pointed at; the books' run items and their attempts.
--
-- Kept (amendment 1): every fingerprint-scoped override and the manual result
-- it cites. A fingerprint decision is about a spelling of a name, not about a
-- book: when the last book carrying the spelling goes, the decision stays,
-- dormant, and a book that brings the spelling back — the archive rescanned —
-- is resolved by it again (the resolver reads the fingerprint's override for
-- every credit it resolves). Also kept: local results and jobs, keyed by
-- normalization input and shared across books, so a returning book reuses
-- them; and everything about a fingerprint another author credit still
-- carries. Runs keep their rows: an active run recounts its items from the
-- rows that remain (ReconcileRunExtraction), a finished one keeps its
-- counters as its record. Pilot approvals and the acceptance policy do not
-- belong to a book.
--
-- Every statement is set-based over the books' credits, so the work is
-- bounded by the books' own rows, not by the size of the catalogue.
--
-- Lock protocol, shared with the source writer (PersistExtraction) and the
-- review and acceptance paths:
--   1. the books, in ID order. A source writer locks its book (FOR KEY SHARE)
--      before it touches the book's snapshots, so a writer refreshing one of
--      these books either finishes first or waits for the deletion — it never
--      holds a snapshot the deletion needs while waiting for the book;
--   2. the author layer, exclusively (advisory lock 'layr', 0). Source writers
--      hold it shared while they add credits, so no credit of a deleted
--      fingerprint can appear, committed or in flight, after step 3 has
--      decided that the fingerprint is orphaned. Taken after the books, as
--      writers take it after their book;
--   3. the books' credits, in ascending ID order — the order every review and
--      resolution path locks credits in, before any review item;
--   4. only then the orphan decision (which fingerprint review items go) and
--      the deletes.
CREATE FUNCTION public.author_layer_delete_books(book_ids bigint[]) RETURNS void
    LANGUAGE plpgsql AS $$
DECLARE
    credits bigint[];
    orphans bytea[];
    manual bigint[];
BEGIN
    PERFORM 1 FROM public.opds_catalog_book WHERE id = ANY (book_ids) ORDER BY id FOR UPDATE;
    PERFORM pg_advisory_xact_lock(1818327410, 0); -- 'layr', see author_layer_lock.go

    credits := ARRAY(
        SELECT c.id FROM public.book_contributor_credit c
        JOIN public.book_metadata_snapshot s ON s.id = c.snapshot_id
        WHERE s.book_id = ANY (book_ids)
        ORDER BY c.id);
    PERFORM 1 FROM public.book_contributor_credit WHERE id = ANY (credits) ORDER BY id FOR UPDATE;

    -- Source fingerprints of the deleted author credits that no remaining
    -- author credit carries.
    orphans := ARRAY(
        SELECT DISTINCT c.source_fingerprint FROM public.book_contributor_credit c
        WHERE c.id = ANY (credits) AND c.role = 'author'
          AND NOT EXISTS (
              SELECT 1 FROM public.book_contributor_credit o
              WHERE o.source_fingerprint = c.source_fingerprint AND o.role = 'author'
                AND NOT (o.id = ANY (credits))));

    -- Manual results the removed overrides and review items point at; the
    -- ones nothing else points at go too. A fingerprint override is never
    -- removed, so the result it cites stays referenced.
    manual := ARRAY(
        SELECT o.result_id FROM public.contributor_manual_override o
        WHERE o.scope_credit_id = ANY (credits)
        UNION
        SELECT i.resolution_result_id FROM public.contributor_review_item i
        WHERE (i.scope_credit_id = ANY (credits) OR i.scope_fingerprint = ANY (orphans))
          AND i.resolution_result_id IS NOT NULL);

    PERFORM set_config('gopds.author_layer_delete', 'on', true);

    DELETE FROM public.contributor_review_item
    WHERE scope_credit_id = ANY (credits) OR scope_fingerprint = ANY (orphans);
    DELETE FROM public.book_contributor_credit_selection_audit WHERE credit_id = ANY (credits);
    DELETE FROM public.book_contributor_credit_selection WHERE credit_id = ANY (credits);
    DELETE FROM public.contributor_manual_override WHERE scope_credit_id = ANY (credits);
    DELETE FROM public.author_metadata_run_item_attempt
    WHERE run_item_id IN (SELECT id FROM public.author_metadata_run_item WHERE book_id = ANY (book_ids));
    DELETE FROM public.author_metadata_run_item WHERE book_id = ANY (book_ids);
    DELETE FROM public.book_contributor_credit WHERE id = ANY (credits);
    DELETE FROM public.book_metadata_snapshot WHERE book_id = ANY (book_ids);
    DELETE FROM public.contributor_normalization_result r
    WHERE r.id = ANY (manual) AND r.method = 'manual'
      AND NOT EXISTS (SELECT 1 FROM public.contributor_manual_override o WHERE o.result_id = r.id)
      AND NOT EXISTS (SELECT 1 FROM public.contributor_review_item i
                      WHERE r.id IN (i.proposal_result_id, i.resolution_result_id))
      AND NOT EXISTS (SELECT 1 FROM public.book_contributor_credit_selection s WHERE s.result_id = r.id)
      AND NOT EXISTS (SELECT 1 FROM public.book_contributor_credit_selection_audit a
                      WHERE r.id IN (a.result_id, a.previous_result_id))
      AND NOT EXISTS (SELECT 1 FROM public.contributor_normalization_job j WHERE j.result_id = r.id);

    PERFORM set_config('gopds.author_layer_delete', 'off', true);
END
$$;
