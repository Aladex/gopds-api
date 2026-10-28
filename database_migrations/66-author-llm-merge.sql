-- 66: the merge configuration's questions in the LLM layer (E4c, plan §4.1,
-- §4.6). Nothing here asks a model: the merge worker (a second worker on the
-- merge configuration, its own version) answers what a merge eval or, from
-- E4d, a merge run enqueues. K's rows, policy numbers and configuration are
-- untouched; every change only widens a check.
--
--   * author_llm_run: eval modes merge and merge0..merge4 (the pilot and
--     the pooled kinds); their stages are stored with the scoped_ prefix
--     like migration 61's, which the stage check already admits.
--   * author_llm_job: purpose merge_check — a merge question, keyed like a
--     review by its candidate's digest (32 bytes) and the card version.
--   * author_llm_verdict: purpose merge_check (its verdicts come with E4d).
--   * author_llm_eval_item: item set adversarial — the namesake suite, each
--     item with its expected answer, like an anchor.
--   * author_llm_attempt: tier merge — an answered merge question that is
--     not a refusal (cannot_tell is the refusal, as for a name).
--
-- Locks: the tables are taken up front, NOWAIT, like 28-61. A worker holding
-- one makes the file fail at once; the next start retries it whole. Only
-- check constraints change; no row is rewritten.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_run, public.author_llm_job, public.author_llm_verdict, public.author_llm_eval_item,
    public.author_llm_attempt IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_llm_run
    DROP CONSTRAINT author_llm_run_mode_check,
    ADD CONSTRAINT author_llm_run_mode_check
        CHECK (CASE kind
                   WHEN 'eval' THEN mode IN ('dev', 'pilot', 'full', 'swap', 'stage0', 'stage1', 'stage2',
                                             'stage3', 'merge', 'merge0', 'merge1', 'merge2', 'merge3', 'merge4')
                   ELSE mode IN ('pilot', 'full')
               END);

ALTER TABLE public.author_llm_job
    DROP CONSTRAINT author_llm_job_purpose_check,
    ADD CONSTRAINT author_llm_job_purpose_check
        CHECK (purpose IN ('review', 'swap_check', 'eval_judge', 'eval_candidate', 'eval_repeat',
                           'eval_anchor', 'eval_fingerprint', 'canary', 'merge_check')),
    DROP CONSTRAINT author_llm_job_shape_check,
    ADD CONSTRAINT author_llm_job_shape_check
        CHECK (CASE WHEN purpose IN ('review', 'swap_check', 'merge_check')
                    THEN octet_length(source_fingerprint) = 32
                         AND extractor_version ~ '^[^[:space:]](.*[^[:space:]])?$'
                         AND eval_item_id IS NULL AND repeat_no = 0
                    ELSE source_fingerprint IS NULL AND extractor_version IS NULL
                         AND eval_item_id IS NOT NULL
               END);

ALTER TABLE public.author_llm_verdict
    DROP CONSTRAINT author_llm_verdict_purpose_check,
    ADD CONSTRAINT author_llm_verdict_purpose_check CHECK (purpose IN ('review', 'swap_check', 'merge_check'));

ALTER TABLE public.author_llm_eval_item
    DROP CONSTRAINT author_llm_eval_item_set_check,
    ADD CONSTRAINT author_llm_eval_item_set_check
        CHECK (item_set IN ('holdout', 'dev', 'anchor', 'canary', 'fingerprint', 'adversarial')),
    DROP CONSTRAINT author_llm_eval_item_expected_check,
    ADD CONSTRAINT author_llm_eval_item_expected_check
        CHECK ((item_set IN ('anchor', 'adversarial')) = (expected IS NOT NULL)
               AND (expected IS NULL OR jsonb_typeof(expected) = 'object'));

ALTER TABLE public.author_llm_attempt
    DROP CONSTRAINT author_llm_attempt_tier_check,
    ADD CONSTRAINT author_llm_attempt_tier_check
        CHECK (tier IS NULL OR tier IN ('confirm', 'restructure', 'reclassify', 'case', 'merge'));
