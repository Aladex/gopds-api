-- 75: the mixed-script merge kinds (E4h). A level-0 side whose keys span
-- two scripts (a Wikidata cross-script merge) was kept out of every kind as
-- mixed_script. It now pairs through the keys that match, recorded in their
-- script, as a kind of its own per shape — bucket_mixed, swap_mixed,
-- initials_mixed, translit_mixed, variant_mixed — so what a registered
-- plain kind covers never changes: asked in a census eval of its own
-- (merge7), gated on a human audit of each kind's applied set, registered
-- and applied through the existing flow.
--
--   * The kind checks of the candidates, their sample, the decisions, the
--     registrations and a merge run's frozen cards admit the five kinds.
--   * A candidate and a decision are of a book exactly when of a bucket
--     kind: bucket or bucket_mixed.
--   * author_llm_run: eval mode merge7.
--   * author_llm_eval_item_audited (70, 72-74): a merge7 holdout item is a
--     census item like merge6's; the rest as in 74.
--
-- Every change only widens a check; no row is rewritten.
--
-- Locks: the tables are taken up front, NOWAIT, like 28-74. A build, a
-- worker or a run holding one makes the file fail at once; the next start
-- retries it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_entity_merge_candidate, public.author_entity_merge_candidate_sample,
    public.author_entity_merge_decision, public.author_entity_merge_registration, public.author_llm_merge_input,
    public.author_llm_run IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_entity_merge_candidate
    DROP CONSTRAINT author_entity_merge_candidate_kind_check,
    ADD CONSTRAINT author_entity_merge_candidate_kind_check
        CHECK (kind IN ('bucket', 'swap', 'initials', 'translit', 'variant', 'initials_hub', 'bucket_mixed', 'swap_mixed',
                     'initials_mixed', 'translit_mixed', 'variant_mixed')),
    DROP CONSTRAINT author_entity_merge_candidate_item_check,
    ADD CONSTRAINT author_entity_merge_candidate_item_check
        CHECK ((item_kind = 'book') = (kind IN ('bucket', 'bucket_mixed')) AND item_kind IN ('entity', 'book')
               AND (item_kind = 'book') = (credit_id IS NOT NULL)
               AND (credit_id IS NULL) = (credit_ordinal IS NULL) AND (credit_ordinal IS NULL OR credit_ordinal >= 0));

ALTER TABLE public.author_entity_merge_candidate_sample
    DROP CONSTRAINT author_entity_merge_candidate_sample_codes_check,
    ADD CONSTRAINT author_entity_merge_candidate_sample_codes_check
        CHECK (kind IN ('bucket', 'swap', 'initials', 'translit', 'variant', 'initials_hub', 'bucket_mixed', 'swap_mixed',
                     'initials_mixed', 'translit_mixed', 'variant_mixed')
               AND script ~ '^[A-Za-z][A-Za-z-]{0,15}$' AND reason ~ '^[a-z][a-z0-9_]{0,63}$');

ALTER TABLE public.author_entity_merge_decision
    DROP CONSTRAINT author_entity_merge_decision_kind_check,
    ADD CONSTRAINT author_entity_merge_decision_kind_check
        CHECK (kind IN ('bucket', 'swap', 'initials', 'translit', 'variant', 'initials_hub', 'bucket_mixed', 'swap_mixed',
                     'initials_mixed', 'translit_mixed', 'variant_mixed')),
    DROP CONSTRAINT author_entity_merge_decision_item_check,
    ADD CONSTRAINT author_entity_merge_decision_item_check
        CHECK ((item_kind = 'book') = (kind IN ('bucket', 'bucket_mixed')) AND item_kind IN ('entity', 'book')
               AND (item_kind = 'book') = (credit_id IS NOT NULL)
               AND (credit_id IS NULL) = (credit_ordinal IS NULL) AND (credit_ordinal IS NULL OR credit_ordinal >= 0));

ALTER TABLE public.author_entity_merge_registration
    DROP CONSTRAINT author_entity_merge_registration_kind_check,
    ADD CONSTRAINT author_entity_merge_registration_kind_check
        CHECK (kind IN ('bucket', 'swap', 'initials', 'translit', 'variant', 'initials_hub', 'bucket_mixed', 'swap_mixed',
                     'initials_mixed', 'translit_mixed', 'variant_mixed'));

ALTER TABLE public.author_llm_merge_input
    DROP CONSTRAINT author_llm_merge_input_kind_check,
    ADD CONSTRAINT author_llm_merge_input_kind_check
        CHECK (kind IN ('bucket', 'swap', 'initials', 'translit', 'variant', 'initials_hub', 'bucket_mixed', 'swap_mixed',
                     'initials_mixed', 'translit_mixed', 'variant_mixed'));

ALTER TABLE public.author_llm_run
    DROP CONSTRAINT author_llm_run_mode_check,
    ADD CONSTRAINT author_llm_run_mode_check
        CHECK (CASE kind
                   WHEN 'eval' THEN mode IN ('dev', 'pilot', 'full', 'swap', 'stage0', 'stage1', 'stage2',
                                             'stage3', 'merge', 'merge0', 'merge1', 'merge2', 'merge3', 'merge4',
                                             'merge5', 'merge6', 'merge7')
                   ELSE mode IN ('pilot', 'full')
               END);

-- The function's body names the LLM tables; validating it at creation would
-- take them ACCESS SHARE and wait for a writer holding them. It is checked
-- at its first call instead (the adjudication insert and its trigger).
SET LOCAL check_function_bodies = off;

-- An item of the audit sample of some census or merge3 run: a census item
-- (the run mode's holdout set: merge1, merge2, merge4 to merge7) or a
-- merge3 holdout item or extra question, all four of whose first answers in
-- the run are same, within the first applied items of its set and stratum
-- (the kind) in the run's audit order — sample of them, at most 100 extra
-- questions (72). A kind of at most sample applied items (merge6's, and
-- merge7's swap, initials and variant mixed kinds) is audited whole.
CREATE OR REPLACE FUNCTION public.author_llm_eval_item_audited(item bigint, sample integer DEFAULT 300) RETURNS boolean
    LANGUAGE sql STABLE AS $$
    WITH target AS (
        SELECT e.set_version, split_part(e.stratum, '.', 1) AS kind,
            CASE WHEN e.set_version = 'm1.merge3.supplement' THEN least(sample, 100) ELSE sample END AS size
        FROM public.author_llm_eval_item e
        WHERE e.id = item AND e.item_set = 'holdout' AND e.extractor_version = 'e4-cand-v2'
    ), runs AS (
        SELECT DISTINCT r.id
        FROM public.author_llm_job j
        JOIN public.author_llm_run r ON r.id = j.run_id AND r.kind = 'eval'
        JOIN target t ON (r.mode IN ('merge1', 'merge2', 'merge4', 'merge5', 'merge6', 'merge7')
                AND t.set_version = 'm1.' || r.mode || '.holdout')
            OR (r.mode = 'merge3' AND t.set_version IN ('m1.merge3.holdout', 'm1.merge3.supplement'))
        WHERE j.eval_item_id = item
    ), applied AS (
        SELECT j.run_id, j.eval_item_id
        FROM runs
        JOIN public.author_llm_job j ON j.run_id = runs.id
        JOIN public.author_llm_eval_item e ON e.id = j.eval_item_id
        JOIN target t ON e.set_version = t.set_version AND split_part(e.stratum, '.', 1) = t.kind
        JOIN public.author_llm_attempt a ON a.id = j.answer_attempt_id
        WHERE e.item_set = 'holdout' AND e.extractor_version = 'e4-cand-v2'
            AND j.purpose IN ('eval_candidate', 'eval_judge') AND j.repeat_no = 0 AND j.status = 'answered'
            AND NOT coalesce(a.refused, false)
            AND a.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8'))
        GROUP BY j.run_id, j.eval_item_id
        HAVING count(DISTINCT j.slot) = 4
    )
    SELECT EXISTS (
        SELECT 1 FROM (
            SELECT eval_item_id, row_number() OVER (
                PARTITION BY run_id
                ORDER BY sha256(convert_to(run_id::text || ':' || eval_item_id::text, 'UTF8'))) AS rank
            FROM applied
        ) ranked, target t
        WHERE ranked.eval_item_id = item AND ranked.rank <= t.size)
$$;
