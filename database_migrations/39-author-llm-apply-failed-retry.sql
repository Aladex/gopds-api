-- Run 15 failed to apply 20 agreed and confirmed verdicts (23503): the LLM
-- result took the fingerprint of the request's fields, which differs from the
-- credit's when the FB2 has an empty element or another element order. The
-- result now carries the verdict's fingerprint; those inputs are asked again
-- by the next resolve run, so a verdict settled apply_failed is history like a
-- transiently failed one: the input's current verdict is the one asked again.
-- Which apply_failed inputs are asked again is decided by the service (only
-- those whose request fields do not rebuild their fingerprint).
--
-- Locks: the verdicts are taken up front, NOWAIT, like 28-38. A transaction
-- holding them (the LLM worker settling a call) makes the file fail at once;
-- the next start retries it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_verdict IN ACCESS EXCLUSIVE MODE NOWAIT;

DROP INDEX public.author_llm_verdict_current;
CREATE UNIQUE INDEX author_llm_verdict_current
    ON public.author_llm_verdict (source_fingerprint, extractor_version, purpose, config_version)
    WHERE verdict <> 'held_drift'
        AND (outcome_reason IS NULL OR outcome_reason NOT IN ('llm_failed', 'llm_judge_failed', 'apply_failed'));
