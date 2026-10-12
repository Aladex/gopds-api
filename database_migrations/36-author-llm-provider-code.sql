-- A provider may reject one item rather than the participant: the z.ai coding
-- plan's content filter answers HTTP 400 (its code 1301) on a "sensitive"
-- input or output. Such an item ends rejected and the participant goes on;
-- the attempt keeps the provider's own error code — an identifier, never the
-- message — so a report can count rejections by their cause.
--
-- Only a run of rejections on distinct items, with no answered call in
-- between, says the participant itself is broken. The participant's state
-- keeps that run in the order the calls settle (under the claim lock), not in
-- the order they were sent: rejected_items holds the distinct items rejected
-- since the last answered call or bad_request pause.
--
-- Locks: the attempts and the participants' state are taken up front,
-- NOWAIT, like 28-35. A transaction holding either (the LLM worker) makes the
-- file fail at once; the next start retries it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_attempt, public.author_llm_provider_state IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_llm_attempt
    ADD COLUMN provider_code TEXT,
    ADD CONSTRAINT author_llm_attempt_provider_code_check
        CHECK (provider_code IS NULL OR provider_code ~ '^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$');

-- The code is written when the attempt is closed, with its outcome.
DROP TRIGGER author_llm_attempt_identity ON public.author_llm_attempt;
CREATE TRIGGER author_llm_attempt_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_attempt
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'finished_at', 'outcome', 'validator_class', 'reply', 'canonical_sha256', 'tier', 'refused',
        'provider_code');

ALTER TABLE public.author_llm_provider_state
    ADD COLUMN rejected_items TEXT[] NOT NULL DEFAULT '{}';
