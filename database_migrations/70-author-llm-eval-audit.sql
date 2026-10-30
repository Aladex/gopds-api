-- 70: the census audit (E4c, Andrey's decision of 2026-10-30). A census
-- mode's stratum (merge1, merge2, merge4) is gated on a human audit of its
-- applied set — the census items the production pair and both judges
-- answered same in a run — instead of on the pair's answers against the
-- judges'. The audit reads a deterministic sample of the applied items of
-- each stratum: in order of sha256 of the UTF-8 text '<run id>:<item id>'
-- (bytes ascending, as its hex), the first 300 (llmeval.AuditKey,
-- llmeval.AuditSampleSize). An item of
-- that sample may be labeled in author_llm_eval_adjudication like a suite
-- item or a contested accept; the trigger keeps refusing anything else (an
-- applied item outside the sample, a gold error, an anchor, K's items).
--
-- One function, author_llm_eval_item_audited, says what an audited item
-- is; the adjudication trigger and the application's insert both read it.
--
-- Locks: none up front. The file creates a function and replaces another
-- (the trigger keeps naming it); neither takes a table lock.

SET LOCAL lock_timeout = '5s';

-- The function's body names the LLM tables; validating it at creation would
-- take them ACCESS SHARE and wait for a writer holding them. It is checked
-- at its first call instead (the adjudication insert and its trigger).
SET LOCAL check_function_bodies = off;

-- An item of the audit sample of some census run: a census item (the run
-- mode's holdout set, merge candidate digest) all four of whose first
-- answers in the run are same, within the first sample applied items of
-- its stratum (the kind) in the run's audit order.
CREATE FUNCTION public.author_llm_eval_item_audited(item bigint, sample integer DEFAULT 300) RETURNS boolean
    LANGUAGE sql STABLE AS $$
    WITH target AS (
        SELECT e.set_version, split_part(e.stratum, '.', 1) AS kind
        FROM public.author_llm_eval_item e
        WHERE e.id = item AND e.item_set = 'holdout' AND e.extractor_version = 'e4-cand-v2'
    ), runs AS (
        SELECT DISTINCT r.id
        FROM public.author_llm_job j
        JOIN public.author_llm_run r ON r.id = j.run_id AND r.kind = 'eval'
            AND r.mode IN ('merge1', 'merge2', 'merge4')
        JOIN target t ON t.set_version = 'm1.' || r.mode || '.holdout'
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
        ) ranked
        WHERE ranked.eval_item_id = item AND ranked.rank <= sample)
$$;

-- Only an adversarial suite item, a contested accept or an item of a
-- census's audit sample is adjudicated.
CREATE OR REPLACE FUNCTION public.author_llm_eval_adjudication_adversarial() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.author_llm_eval_item
                   WHERE id = NEW.eval_item_id AND item_set = 'adversarial')
       AND NOT public.author_llm_eval_item_contested(NEW.eval_item_id)
       AND NOT public.author_llm_eval_item_audited(NEW.eval_item_id) THEN
        RAISE EXCEPTION 'eval item % is neither an adversarial suite item, a contested accept nor an audited one',
            NEW.eval_item_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;
