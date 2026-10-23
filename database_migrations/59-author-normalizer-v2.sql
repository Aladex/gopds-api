-- 59: the local normalizer authornorm-local-v2: its queue, safe next to the
-- v1 binary in a rolling update, and its acceptance policy.
--
-- v2 reads three shapes v1 left to review (rules D2-D4 of the review backlog
-- analysis R0): a nickname that is exactly a placeholder is dropped before
-- the rest is normalized; a value named by a collective word, with no
-- conjunction and no person label, is the selectable class collective; a
-- capital "И" between name words is an initial, not a conjunction. Every
-- other input normalizes exactly as under v1 (measured on a production copy:
-- all 231,572 inputs, differences only in placeholder_fragment and
-- collective_candidate).
--
-- Registrations are keyed by normalizer configuration, so v1's pairs do not
-- carry over: structured_person is registered again for v2, placeholder
-- (rule D1) and collective (rule D3) for the first time. collective is
-- registered for Cyrillic only: the production copy has one Latin input of
-- it, too little to judge. The evidence is one frozen report drawn from the
-- production copy under v2, checked in at evidence_ref; a test pins the hash
-- to the file's bytes.
--
-- The version is the next after the newest registration, whatever registered
-- it: the LLM layer registers its own pairs at run time, so a fixed number
-- could already be taken. The lock keeps the read and the insert one step,
-- as the LLM layer's registration does.

SET LOCAL lock_timeout = '5s';

-- The local queue in a rolling update. The v1 binary keeps running next to
-- this one for a while (and again after a rollback); its claim reads every
-- row with status 'pending', whatever the row's normalizer version, and its
-- worker refuses a job of another version, spending the job's attempts. So a
-- job written from v2 on waits as 'queued', a value the v1 claim, its
-- exhaustion sweep and its lease fences never match, and a worker claims only
-- the queued jobs of its own normalizer version. 'pending' stays the v1
-- binary's own waiting state: the column default, which its inserts rely on,
-- is left as it is, and the v2 worker re-queues those inputs under v2 instead
-- of claiming them.
ALTER TABLE public.contributor_normalization_job
    DROP CONSTRAINT contributor_normalization_job_status_check,
    ADD CONSTRAINT contributor_normalization_job_status_check
        CHECK (status IN ('pending', 'queued', 'completed', 'failed')),
    DROP CONSTRAINT contributor_normalization_job_finished_check,
    ADD CONSTRAINT contributor_normalization_job_finished_check
        CHECK ((status IN ('pending', 'queued')) = (finished_at IS NULL)),
    DROP CONSTRAINT contributor_normalization_job_lease_terminal_check,
    ADD CONSTRAINT contributor_normalization_job_lease_terminal_check
        CHECK (status IN ('pending', 'queued') OR lease_owner IS NULL);

-- 'pending' is the v1 binary's alone: a job of any other version that is
-- written as pending becomes queued as it is written. The v1 binary writes
-- that only one way — its run-admin retry, which reopens the failed jobs of a
-- run's own normalizer version and would do so for a v2 run after a rollback —
-- and its claim would then burn the job's attempts on a key it refuses. The
-- rewrite keeps every v1 statement valid and makes the reopened job one this
-- worker claims.
CREATE FUNCTION public.contributor_normalization_job_waiting() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'pending' AND NEW.normalizer_version <> 'authornorm-local-v1' THEN
        NEW.status := 'queued';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER contributor_normalization_job_waiting
    BEFORE INSERT OR UPDATE OF status ON public.contributor_normalization_job
    FOR EACH ROW EXECUTE FUNCTION public.contributor_normalization_job_waiting();

-- The claim of a version's queued jobs.
CREATE INDEX contributor_normalization_job_queued_idx
    ON public.contributor_normalization_job (normalizer_version, next_attempt_at, id)
    WHERE status = 'queued';

-- A job by its input: whether an input already has a job of a version.
CREATE INDEX contributor_normalization_job_input_idx
    ON public.contributor_normalization_job (source_fingerprint, extractor_version);

-- The v1 jobs the v2 worker re-queues: failed ones, and any written after
-- the release by a v1 binary still running.
CREATE INDEX contributor_normalization_job_failed_idx
    ON public.contributor_normalization_job (id)
    WHERE status = 'failed';
CREATE INDEX contributor_normalization_job_created_idx
    ON public.contributor_normalization_job (created_at);

LOCK TABLE public.author_acceptance_class IN SHARE ROW EXCLUSIVE MODE;

INSERT INTO public.author_acceptance_class
    (policy_version, decision_class, script, config_version, evidence_report_sha256, evidence_ref, source,
     registered_by_user_id)
SELECT next.version::text, pair.decision_class, pair.script, 'authornorm-local-v2',
       decode('ed8de5597de44f9a26b0754cb8bdd54f9ca270640a77ecc95b11e0faa9bb2e62', 'hex'),
       'internal/authornorm/policy_evidence/local-v2.json', 'shipped', NULL
FROM (SELECT greatest(coalesce(max(policy_version::int), 0), 1) + 1 AS version
      FROM public.author_acceptance_class) AS next
CROSS JOIN (VALUES
    ('structured_person', 'Cyrl'),
    ('structured_person', 'Latn'),
    ('placeholder', 'Cyrl'),
    ('placeholder', 'Latn'),
    ('collective', 'Cyrl')
) AS pair (decision_class, script);
