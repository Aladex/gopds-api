-- Run 15 (the resolve pilot) lost a quarter of its inputs to a burst of 503
-- from the gateway: a job failed after five quick attempts, and the input was
-- settled llm_judge_failed for good. A transient failure — a 5xx, a timeout, a
-- broken connection — now keeps the job retrying for a time window, and an
-- input whose verdict ended on one decides nothing: the next resolve run asks
-- it again.
--
-- author_llm_job.transient_since is when the job's current run of transient
-- failures began; the job fails only once the window since then has passed.
-- A job asked again by a later run moves to that run, so run_id becomes
-- mutable (the calls and attempts keep the run they were made under).
--
-- A verdict settled on a transient failure (llm_failed, llm_judge_failed) is
-- history, like a held_drift one: the input's current verdict is the one
-- asked again. And a verdict whose input a rescan removed before it was
-- applied settles input_gone.
--
-- Locks: the jobs and the verdicts are taken up front, NOWAIT, like 28-37. A
-- transaction holding either (the LLM worker settling a call) makes the file
-- fail at once; the next start retries it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_job, public.author_llm_verdict IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_llm_job ADD COLUMN transient_since TIMESTAMPTZ;

DROP TRIGGER author_llm_job_identity ON public.author_llm_job;
CREATE TRIGGER author_llm_job_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_job
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'context_state', 'context_book_id', 'context_sha256', 'status', 'call_id', 'attempt_count',
        'failure_count', 'invalid_count', 'retry_single', 'next_attempt_at', 'last_error_class',
        'answer_attempt_id', 'updated_at', 'finished_at', 'output_mode', 'run_id', 'transient_since');

DROP INDEX public.author_llm_verdict_current;
CREATE UNIQUE INDEX author_llm_verdict_current
    ON public.author_llm_verdict (source_fingerprint, extractor_version, purpose, config_version)
    WHERE verdict <> 'held_drift'
        AND (outcome_reason IS NULL OR outcome_reason NOT IN ('llm_failed', 'llm_judge_failed'));

ALTER TABLE public.author_llm_verdict
    DROP CONSTRAINT author_llm_verdict_outcome_check,
    ADD CONSTRAINT author_llm_verdict_outcome_check
        CHECK (outcome_reason IS NULL
               OR outcome_reason IN ('llm_disagreement', 'llm_refused', 'llm_invalid', 'llm_failed',
                                     'llm_judge_disagreement', 'llm_judge_refused', 'llm_judge_invalid',
                                     'llm_judge_failed', 'llm_not_registered', 'llm_context_unavailable',
                                     'apply_failed', 'input_gone'));
