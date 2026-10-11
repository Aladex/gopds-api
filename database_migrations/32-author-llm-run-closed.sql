-- An LLM run an administrator closes (design K, phase 4, review B1). A run of
-- an older configuration cannot go on — the worker and the driver serve only
-- the configuration they were started with — yet it held the one active slot,
-- so after an endpoint or model change nothing could start again. Closing
-- ends such a run, or a paused one, for good:
--
--   * status 'closed': ended by an administrator, neither completed nor
--     failed. Like them it has a finished_at and leaves the active slot
--     (author_llm_run_one_active covers pending, running and paused only).
--   * closed_by_user_id: who closed it. Attribution only, without a foreign
--     key: one would lock auth_user, which every login writes, for the file.
--
-- Locks: author_llm_run is taken up front, NOWAIT, like 28, 29 and 31. A
-- transaction holding it (the LLM worker, the eval driver) makes the file
-- fail at once; the next start retries it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_run IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_llm_run
    ADD COLUMN closed_by_user_id BIGINT,
    DROP CONSTRAINT author_llm_run_status_check,
    ADD CONSTRAINT author_llm_run_status_check
        CHECK (status IN ('pending', 'running', 'paused', 'completed', 'failed_systemic', 'closed')),
    DROP CONSTRAINT author_llm_run_finished_check,
    ADD CONSTRAINT author_llm_run_finished_check
        CHECK ((status IN ('completed', 'failed_systemic', 'closed')) = (finished_at IS NOT NULL)),
    ADD CONSTRAINT author_llm_run_closed_check
        CHECK (closed_by_user_id IS NULL OR status = 'closed');

-- The closing administrator joins the run's mutable columns.
DROP TRIGGER author_llm_run_identity ON public.author_llm_run;
CREATE TRIGGER author_llm_run_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_run
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'status', 'stage', 'estimate_tokens', 'expected_models', 'reference', 'paused_reason',
        'last_error_class', 'updated_at', 'started_at', 'finished_at', 'closed_by_user_id');
