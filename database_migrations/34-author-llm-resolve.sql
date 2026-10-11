-- The LLM resolve run (design K, phase 6): the production pair answers a
-- review input, both judges confirm every answer the pair agrees on, and the
-- agreed answer is applied only when the judges confirm it and the eval
-- report registered its stratum. Everything else stays unresolved with a
-- closed reason, and the review item is closed by the service.
--
--   contributor_normalization_result — at most one llm result per key.
--   book_contributor_credit_selection — the LLM's unresolved reasons.
--   contributor_review_item — the resolution 'automatic', closed without an
--       admin.
--   author_llm_verdict — the judges' judgement of an agreed answer, and how
--       the verdict was settled (applied, or the reason it was not).
--   author_llm_resolve_report — the immutable report of a resolve run.
--   author_llm_job — a review job carries the arm its stratum was registered
--       on, so arm A and arm B inputs never share a call.
--
-- Locks: every existing table this file alters is taken up front, NOWAIT, as
-- in 28-33, and the runs in the mode the new foreign key needs. A transaction
-- holding any of them makes the file fail at once; the next start retries it
-- whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.contributor_normalization_result, public.book_contributor_credit_selection,
    public.contributor_review_item, public.author_llm_verdict, public.author_llm_job
    IN ACCESS EXCLUSIVE MODE NOWAIT;
-- The report's foreign key onto the runs.
LOCK TABLE public.author_llm_run IN SHARE ROW EXCLUSIVE MODE NOWAIT;

-- A review job names the arm its stratum's eval chose: the claim batches
-- only jobs of one arm, as the eval asked them. Everything else about the
-- shape is migration 29's.
ALTER TABLE public.author_llm_job
    DROP CONSTRAINT author_llm_job_shape_check,
    ADD CONSTRAINT author_llm_job_shape_check
        CHECK (CASE WHEN purpose IN ('review', 'swap_check')
                    THEN octet_length(source_fingerprint) = 32
                         AND extractor_version ~ '^[^[:space:]](.*[^[:space:]])?$'
                         AND eval_item_id IS NULL AND repeat_no = 0
                    ELSE source_fingerprint IS NULL AND extractor_version IS NULL
                         AND eval_item_id IS NOT NULL
               END);

-- One llm result per normalization key (fingerprint, extractor, LLM
-- configuration), as the local methods have one per key.
CREATE UNIQUE INDEX contributor_normalization_result_one_llm_per_key
    ON public.contributor_normalization_result (normalization_key)
    WHERE method = 'llm';

-- Why the LLM layer left an input unresolved: the pair disagreed, refused,
-- stayed invalid or failed; a judge did not confirm the agreed answer; the
-- answer's class, script or tier is not registered; the excerpt the stratum
-- needs could not be read.
ALTER TABLE public.book_contributor_credit_selection
    DROP CONSTRAINT book_contributor_credit_selection_reason_check,
    ADD CONSTRAINT book_contributor_credit_selection_reason_check
        CHECK (unresolved_reason IS NULL
               OR unresolved_reason IN ('policy_not_registered', 'normalizer_failed', 'review_left_unresolved',
                                        'llm_disagreement', 'llm_refused', 'llm_invalid', 'llm_failed',
                                        'llm_judge_disagreement', 'llm_judge_refused', 'llm_judge_invalid',
                                        'llm_judge_failed', 'llm_not_registered', 'llm_context_unavailable'));

-- The service closes a review item it resolved: resolution 'automatic',
-- without an admin.
ALTER TABLE public.contributor_review_item
    DROP CONSTRAINT contributor_review_item_resolution_check,
    ADD CONSTRAINT contributor_review_item_resolution_check
        CHECK (resolution IS NULL
               OR resolution IN ('accepted', 'edited', 'classified', 'left_unresolved', 'retried', 'automatic')),
    DROP CONSTRAINT contributor_review_item_closed_check,
    ADD CONSTRAINT contributor_review_item_closed_check
        CHECK (coalesce(CASE status
                   WHEN 'open' THEN num_nonnulls(resolution, resolution_result_id, resolved_by_user_id, finished_at) = 0
                   ELSE resolution IS NOT NULL AND finished_at IS NOT NULL AND finished_at >= created_at
                        AND CASE resolution
                                WHEN 'retried' THEN true
                                WHEN 'automatic' THEN resolved_by_user_id IS NULL
                                ELSE resolved_by_user_id > 0
                            END
               END, false));

-- The judges' judgement of an agreed answer: confirmed when both answered it
-- byte-equal to the pair; otherwise why not. judged_*_at are the judges'
-- answer times, which their fingerprint checks must cover before the verdict
-- is confirmed. settled_at is when the verdict was applied to the credits:
-- result_id and policy_version when the answer was selected, outcome_reason
-- when it was not; stratum is the input's stratum, for the run's report.
ALTER TABLE public.author_llm_verdict
    ADD COLUMN judgement TEXT,
    ADD COLUMN judged_a_at TIMESTAMPTZ,
    ADD COLUMN judged_b_at TIMESTAMPTZ,
    ADD COLUMN settled_at TIMESTAMPTZ,
    ADD COLUMN outcome_reason TEXT,
    ADD COLUMN policy_version TEXT,
    ADD COLUMN stratum TEXT,
    ADD CONSTRAINT author_llm_verdict_judgement_check
        CHECK ((judgement IS NULL
                OR (verdict IN ('agree', 'held_drift')
                    AND judgement IN ('confirmed', 'disagree', 'refused', 'invalid', 'failed')))
               AND (judgement IS NULL) = (judged_a_at IS NULL)
               AND (judgement IS NULL) = (judged_b_at IS NULL)),
    ADD CONSTRAINT author_llm_verdict_settled_check
        CHECK (CASE WHEN settled_at IS NULL
                    THEN result_id IS NULL AND outcome_reason IS NULL AND policy_version IS NULL
                    ELSE confirmed_at IS NOT NULL
                         AND (result_id IS NULL) = (outcome_reason IS NOT NULL)
                         AND (result_id IS NULL) = (policy_version IS NULL)
               END),
    ADD CONSTRAINT author_llm_verdict_outcome_check
        CHECK (outcome_reason IS NULL
               OR outcome_reason IN ('llm_disagreement', 'llm_refused', 'llm_invalid', 'llm_failed',
                                     'llm_judge_disagreement', 'llm_judge_refused', 'llm_judge_invalid',
                                     'llm_judge_failed', 'llm_not_registered', 'llm_context_unavailable',
                                     'apply_failed')),
    ADD CONSTRAINT author_llm_verdict_policy_check
        CHECK (policy_version IS NULL OR policy_version ~ '^[1-9][0-9]{0,8}$'),
    ADD CONSTRAINT author_llm_verdict_stratum_check
        CHECK (stratum IS NULL OR (settled_at IS NOT NULL AND stratum ~ '^[a-z0-9][a-z0-9_.:-]{0,95}$'));

CREATE INDEX author_llm_verdict_unsettled_idx ON public.author_llm_verdict (run_id)
    WHERE settled_at IS NULL AND verdict <> 'held_drift';

DROP TRIGGER author_llm_verdict_identity ON public.author_llm_verdict;
CREATE TRIGGER author_llm_verdict_identity
    BEFORE UPDATE OR DELETE ON public.author_llm_verdict
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation(
        'verdict', 'confirmed_at', 'result_id', 'judgement', 'judged_a_at', 'judged_b_at', 'settled_at',
        'outcome_reason', 'policy_version', 'stratum');

-- The report of a resolve run: what was applied and what not, per stratum
-- and reason, and the tokens. Immutable.
CREATE TABLE public.author_llm_resolve_report (
    id            BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    run_id        BIGINT NOT NULL REFERENCES public.author_llm_run (id) ON DELETE RESTRICT,
    report        JSONB NOT NULL,
    report_sha256 BYTEA NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT author_llm_resolve_report_run_key UNIQUE (run_id),
    CONSTRAINT author_llm_resolve_report_report_check CHECK (jsonb_typeof(report) = 'object'),
    CONSTRAINT author_llm_resolve_report_sha_check CHECK (octet_length(report_sha256) = 32)
);

CREATE TRIGGER author_llm_resolve_report_immutable
    BEFORE UPDATE OR DELETE ON public.author_llm_resolve_report
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation();
