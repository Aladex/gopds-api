-- An eval report can be computed again from its run's stored answers when the
-- gate code changes, without a new call to any model. Each computation is a
-- new version of the run's report: the versions stay (the rows are
-- immutable, a registration may cite any of them by id and digest), and the
-- latest version is the one a resolve run registers from. The reports stored
-- so far are version 1.
--
-- Locks: the reports are taken up front, NOWAIT, like 28-36. A transaction
-- holding them (the report stage storing one) makes the file fail at once;
-- the next start retries it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_eval_report IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_llm_eval_report
    ADD COLUMN version INT NOT NULL DEFAULT 1,
    ADD CONSTRAINT author_llm_eval_report_version_check CHECK (version >= 1),
    DROP CONSTRAINT author_llm_eval_report_run_key,
    ADD CONSTRAINT author_llm_eval_report_run_version_key UNIQUE (run_id, version);
