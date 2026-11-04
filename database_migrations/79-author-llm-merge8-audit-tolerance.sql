-- 79: merge8's wider audit (E4q plan B). The name kinds' audit samples 473
-- applied items of each kind and passes with at most one labeled different
-- (llmeval.AuditSampleTolerant, AuditToleratedDifferent): the one-sided 95%
-- Clopper-Pearson bound of the error share stays within 1%, as 300 with
-- none does. The rule is fixed before any label of merge8's first report;
-- the apply skips an item labeled different.
--
--   * author_llm_eval_item_audited (70, 72-75, 78): a merge8 holdout item is
--     audited within the first sample + 173 applied items of its kind in the
--     run's audit order, 473 at the default 300, so ranks 301 to 473 can be
--     labeled. The default stays 300 and every other set is unchanged; the
--     extra ranks follow the same order, so a label of the first 300 stays
--     valid.
--
-- Only the function is replaced: no table is locked and no row rewritten, so
-- a running eval or build is not disturbed.

-- An item of the audit sample of some census or merge3 run: a census item
-- (the run mode's holdout set: merge1, merge2, merge4 to merge8) or a
-- merge3 holdout item or extra question, all four of whose first answers in
-- the run are same (for an orphan, every production answer, a repeat
-- included), within the first applied items of its set and stratum
-- (the kind) in the run's audit order — sample of them, at most 100 extra
-- questions (72), sample + 173 of a merge8 kind (79). A kind of at most that
-- many applied items is audited whole.
CREATE OR REPLACE FUNCTION public.author_llm_eval_item_audited(item bigint, sample integer DEFAULT 300) RETURNS boolean
    LANGUAGE sql STABLE AS $$
    WITH target AS (
        SELECT e.set_version, split_part(e.stratum, '.', 1) AS kind,
            CASE e.set_version
                WHEN 'm1.merge3.supplement' THEN least(sample, 100)
                WHEN 'm1.merge8.holdout' THEN sample + 173
                ELSE sample END AS size
        FROM public.author_llm_eval_item e
        WHERE e.id = item AND e.item_set = 'holdout' AND e.extractor_version = 'e4-cand-v2'
    ), runs AS (
        SELECT DISTINCT r.id
        FROM public.author_llm_job j
        JOIN public.author_llm_run r ON r.id = j.run_id AND r.kind = 'eval'
        JOIN target t ON (r.mode IN ('merge1', 'merge2', 'merge4', 'merge5', 'merge6', 'merge7', 'merge8')
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
        GROUP BY j.run_id, j.eval_item_id, t.kind
        HAVING count(DISTINCT j.slot) = 4
            AND (t.kind <> 'orphan' OR NOT EXISTS (
                SELECT 1
                FROM public.author_llm_job p
                LEFT JOIN public.author_llm_attempt pa ON pa.id = p.answer_attempt_id
                WHERE p.run_id = j.run_id AND p.eval_item_id = j.eval_item_id
                    AND p.slot IN ('production_a', 'production_b') AND p.purpose IN ('eval_candidate', 'eval_repeat')
                    AND NOT (p.status = 'answered' AND NOT coalesce(pa.refused, false)
                        AND coalesce(pa.canonical_sha256 = sha256(convert_to('{"answer":"same"}', 'UTF8')), false))))
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
