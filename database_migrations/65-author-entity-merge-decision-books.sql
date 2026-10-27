-- 65: the books a merge decision was about (E4b-3a round 3, plan §4.11).
-- A revoke records its veto from them: the books each side was credited on
-- when the decision was made — exactly those, not a book that carried a
-- member fingerprint only in an older snapshot, nor one rewritten later
-- without a trace. A snapshot that becomes current again leaves no history,
-- so this cannot be read back from the snapshots; it is written with the
-- decision.
--
--   * author_entity_merge_decision_book — per decision and side, every
--     (book, raw fingerprint) of the current author credits of the side's
--     member keys at insert time; a book item's side a is its book and
--     credit alone. Written in the decision's transaction; history, never
--     changed. A revoke copies its decision's rows.
--
-- The rows mark no build: they are written in the transaction of their
-- decision, which marks already.
--
-- Locks: the new table references the decision table, which is taken SHARE
-- ROW EXCLUSIVE up front, NOWAIT, like 28-63: a writer holding it makes this
-- file fail at once, and the next start applies it whole. No other existing
-- table is touched. The table is empty (no decision exists before E4d).

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_entity_merge_decision IN SHARE ROW EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.author_entity_merge_decision_book (
    decision_id        bigint NOT NULL REFERENCES public.author_entity_merge_decision (id) ON DELETE RESTRICT,
    side               text   NOT NULL,
    -- No foreign key to the book: a deleted book stays recorded, as gone.
    book_id            bigint NOT NULL,
    source_fingerprint bytea  NOT NULL,
    CONSTRAINT author_entity_merge_decision_book_pkey PRIMARY KEY (decision_id, side, book_id, source_fingerprint),
    CONSTRAINT author_entity_merge_decision_book_side_check CHECK (side IN ('a', 'b'))
);

CREATE TRIGGER author_entity_merge_decision_book_history
    BEFORE UPDATE OR DELETE ON public.author_entity_merge_decision_book
    FOR EACH ROW EXECUTE FUNCTION public.author_entity_merge_history_guard();
