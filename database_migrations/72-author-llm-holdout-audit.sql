-- 72: the holdout audit (E4c option (c)). merge3's bucket is gated like a
-- census's stratum (70) on a human audit of its applied set, without the
-- census: the holdout's applied items — those the production pair and both
-- judges answered same — sampled in the run's audit order (sha256 of the
-- UTF-8 text '<run id>:<item id>'), the first 300, and the applied extra
-- Latin questions outside |A| sampled in the same order, the first 100
-- (llmeval.AuditSampleSize, llmeval.AuditSupplementSize). An item of
-- either sample may be labeled; anything else stays refused.
--
-- The function keeps its signature: author_llm_eval_item_audited(item,
-- sample) is now also true of a merge3 holdout item within the first sample
-- applied items of its set, and of a merge3 extra question within the
-- first least(sample, 100). The census sets are read as before.
--
-- Locks: none up front. The file replaces a function; it takes no table
-- lock.

SET LOCAL lock_timeout = '5s';

-- The function's body names the LLM tables; validating it at creation would
-- take them ACCESS SHARE and wait for a writer holding them. It is checked
-- at its first call instead (the adjudication insert and its trigger).
SET LOCAL check_function_bodies = off;

-- An item of the audit sample of some census or merge3 run: a census item
-- (the run mode's holdout set) or a merge3 holdout item or extra question
-- (merge candidate digest), all four of whose first answers in the run are
-- same, within the first applied items of its set and stratum (the kind) in
-- the run's audit order — sample of them, at most 100 extra questions.
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
        JOIN target t ON (r.mode IN ('merge1', 'merge2', 'merge4') AND t.set_version = 'm1.' || r.mode || '.holdout')
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
