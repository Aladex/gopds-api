-- A provider may reject one item rather than the participant: the z.ai coding
-- plan's content filter answers HTTP 400 (its code 1301) on a "sensitive"
-- input or output. Such an item ends rejected and the participant goes on;
-- the attempt keeps the provider's own error code — an identifier, never the
-- message — so a report can count rejections by their cause.
--
-- Locks: author_llm_attempt is taken up front, NOWAIT, like 28-35. A
-- transaction holding it (the LLM worker) makes the file fail at once; the
-- next start retries it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_attempt IN ACCESS EXCLUSIVE MODE NOWAIT;

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
