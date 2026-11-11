-- 85: an administrator stops a running triage apply or undo, and it stays
-- stopped (design/review-triage, B1 of REVIEW-4-5).
--
--   * author_review_triage_batch.status takes 'stopped': an apply or an undo
--     an administrator stopped. It is not active, so no worker takes it again
--     and it holds no place in author_review_triage_batch_one_active; its
--     applied part can still be undone.
--   * stop_requested_by_user_id and stop_requested_at: who stopped it, when.
--     Both or neither.
--
-- No row is rewritten: the new columns are NULL, and every existing status is
-- still allowed.
--
-- Locks: author_review_triage_batch is taken up front, NOWAIT, like 28-84. A
-- worker writing its progress makes the file fail at once; startup retries it
-- whole with backoff until its deadline. No other table is touched.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_review_triage_batch IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_review_triage_batch
    ADD COLUMN stop_requested_by_user_id bigint,
    ADD COLUMN stop_requested_at timestamptz,
    DROP CONSTRAINT author_review_triage_batch_status_check,
    ADD CONSTRAINT author_review_triage_batch_status_check
        CHECK (status IN ('planning', 'planned', 'applying', 'applied', 'undoing', 'failed', 'superseded',
                          'stopped')),
    ADD CONSTRAINT author_review_triage_batch_stop_check
        CHECK ((stop_requested_at IS NULL) = (stop_requested_by_user_id IS NULL)
               AND (stop_requested_by_user_id IS NULL OR stop_requested_by_user_id > 0));

DROP TRIGGER author_review_triage_batch_immutable ON public.author_review_triage_batch;
CREATE TRIGGER author_review_triage_batch_immutable
    BEFORE UPDATE OR DELETE ON public.author_review_triage_batch
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'status', 'planned_at', 'apply_requested_by_user_id', 'apply_requested_at', 'applied_steps', 'audit',
        'undo_requested_by_user_id', 'undo_requested_at', 'counts', 'items_total', 'items_done', 'finished_at',
        'failure', 'lease_owner', 'lease_until', 'stop_requested_by_user_id', 'stop_requested_at');
