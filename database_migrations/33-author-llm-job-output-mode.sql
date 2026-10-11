-- The output mode is negotiated at the eval run's fingerprint stage. A
-- provider may refuse a request format (the official DeepSeek API answers
-- response_format json_schema with HTTP 400); the fingerprint job is then
-- asked again in the next mode — json_object, then tool — so its output_mode
-- joins the job's mutable columns. Everything else about a job stays fixed.
--
-- Locks: author_llm_job is taken up front, NOWAIT, like 28, 29, 31 and 32. A
-- transaction holding it (the LLM worker, the eval driver) makes the file
-- fail at once; the next start retries it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_job IN ACCESS EXCLUSIVE MODE NOWAIT;

DROP TRIGGER author_llm_job_identity ON public.author_llm_job;
CREATE TRIGGER author_llm_job_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_job
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'context_state', 'context_book_id', 'context_sha256', 'status', 'call_id', 'attempt_count',
        'failure_count', 'invalid_count', 'retry_single', 'next_attempt_at', 'last_error_class',
        'answer_attempt_id', 'updated_at', 'finished_at', 'output_mode');
