-- 61: stage evals. An eval run of mode stage0, stage1, stage2 or stage3 measures one
-- fixed set of strata (services.AuthorLLMEvalStageStrata), with the gates,
-- sets and report of a full eval, so the review backlog is evaluated and
-- resolved in stages without paying again for the strata already registered.
-- A resolve run rests on the latest completed full or stage eval.
--
-- A stage eval stores its stage under the prefix scoped_ (scoped_freeze,
-- scoped_judge, ...): the binary before this one steps an eval run only
-- through the stage names it knows and passes over any other, so during a
-- rolling update or after a rollback it leaves a stage eval alone instead of
-- measuring every stratum under it. Its workers still answer the run's jobs,
-- with the same configuration. The stage check of migration 29 admits the
-- prefixed names as they are.
--
-- Locks: the runs are taken up front, NOWAIT, like 28-41. A worker holding
-- them makes the file fail at once; the next start retries it whole. Only the
-- check constraint changes; no row is rewritten.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_run IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_llm_run
    DROP CONSTRAINT author_llm_run_mode_check,
    ADD CONSTRAINT author_llm_run_mode_check
        CHECK (CASE kind
                   WHEN 'eval' THEN mode IN ('dev', 'pilot', 'full', 'swap', 'stage0', 'stage1', 'stage2',
                                             'stage3')
                   ELSE mode IN ('pilot', 'full')
               END);
