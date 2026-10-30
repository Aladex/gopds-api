-- 69: the merge run (E4d-2, plan §4.4, §4.6, §6). A resolve-type run of the
-- merge configuration asks the production pair, and both judges where the
-- pair agreed, about the candidates of the last completed build of every
-- registered, not withdrawn kind (script not blocked) that the merge
-- configuration never asked; a verdict the pair agreed same and both judges
-- confirmed is inserted as a decision through the compare-and-insert of
-- §4.4, like an apply from an eval. Moved here from 68, which is on prod.
--
--   * author_llm_run: kind merge (modes pilot and full, like resolve).
--   * author_llm_job: a merge_check job belongs to a merge run.
--   * author_llm_merge_input — the card a merge run froze for a candidate,
--     by its digest: what the model is asked, exactly (eval items keep
--     theirs in author_llm_eval_item.input). Kind and script ride along for
--     the run's report; a digest names both. Insert only.
--   * author_llm_verdict: a merge verdict is settled without a result. It
--     was applied (a decision inserted, or found live from this verdict's
--     answers) when it has no outcome reason; otherwise one of the merge
--     reasons, or the pair's and the judges' reasons K uses.
--
-- K's rows and checks keep their meaning: every change only widens a check
-- for the merge purpose or kind.
--
-- Locks: the tables are taken up front, NOWAIT, like 28-68. A worker
-- holding one makes the file fail at once; the next start retries it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_run, public.author_llm_job, public.author_llm_verdict
    IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_llm_run
    DROP CONSTRAINT author_llm_run_kind_check,
    ADD CONSTRAINT author_llm_run_kind_check CHECK (kind IN ('eval', 'resolve', 'swap', 'merge'));

CREATE OR REPLACE FUNCTION public.author_llm_job_run_kind_check() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    run_kind text;
    wanted   text := CASE NEW.purpose WHEN 'review' THEN 'resolve' WHEN 'swap_check' THEN 'swap'
                                      WHEN 'merge_check' THEN 'merge' ELSE 'eval' END;
BEGIN
    SELECT kind INTO run_kind FROM public.author_llm_run WHERE id = NEW.run_id;
    IF run_kind IS DISTINCT FROM wanted THEN
        RAISE EXCEPTION 'author_llm_job: purpose does not belong to the run''s kind'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;

ALTER TABLE public.author_llm_verdict
    DROP CONSTRAINT author_llm_verdict_settled_check,
    ADD CONSTRAINT author_llm_verdict_settled_check
        CHECK (CASE WHEN settled_at IS NULL
                    THEN result_id IS NULL AND outcome_reason IS NULL AND policy_version IS NULL
                    WHEN purpose = 'merge_check'
                    THEN confirmed_at IS NOT NULL AND result_id IS NULL AND policy_version IS NULL
                    ELSE confirmed_at IS NOT NULL
                         AND (result_id IS NULL) = (outcome_reason IS NOT NULL)
                         AND (result_id IS NULL) = (policy_version IS NULL)
               END),
    DROP CONSTRAINT author_llm_verdict_outcome_check,
    ADD CONSTRAINT author_llm_verdict_outcome_check
        CHECK (outcome_reason IS NULL
               OR outcome_reason IN ('llm_disagreement', 'llm_refused', 'llm_invalid', 'llm_failed',
                                     'llm_judge_disagreement', 'llm_judge_refused', 'llm_judge_invalid',
                                     'llm_judge_failed', 'llm_not_registered', 'llm_context_unavailable',
                                     'apply_failed', 'input_gone', 'swap_fields_confirmed',
                                     'swap_stats_disagree')
               OR (purpose = 'merge_check'
                   AND outcome_reason IN ('merge_not_same', 'merge_lapsed_before_apply', 'merge_already_decided',
                                          'merge_unregistered', 'merge_withdrawn', 'merge_script_blocked',
                                          'merge_side_unrecorded')));

CREATE TABLE public.author_llm_merge_input (
    digest     bytea       PRIMARY KEY,
    kind       text        NOT NULL,
    script     text        NOT NULL,
    input      jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT author_llm_merge_input_digest_check CHECK (octet_length(digest) = 32),
    CONSTRAINT author_llm_merge_input_kind_check CHECK (kind IN ('bucket', 'swap', 'initials', 'translit')),
    CONSTRAINT author_llm_merge_input_script_check CHECK (script IN ('Cyrl', 'Latn', 'Cyrl-Latn')),
    CONSTRAINT author_llm_merge_input_input_check CHECK (jsonb_typeof(input) = 'object')
);

-- A frozen card never changes: the digest it is frozen under hashes it.
CREATE TRIGGER author_llm_merge_input_immutable
    BEFORE UPDATE OR DELETE ON public.author_llm_merge_input
    FOR EACH ROW EXECUTE FUNCTION public.author_metadata_reject_mutation();
