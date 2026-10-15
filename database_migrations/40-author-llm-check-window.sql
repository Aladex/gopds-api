-- A fingerprint check has no job, so a check that kept failing transiently
-- (5xx, timeout, broken transport, an expired lease) was sent again after
-- every throttle, forever: the answers it would cover stayed conditional and
-- the run never finished. The check now gets the items' transient window:
-- check_failures counts its consecutive transient failures and
-- check_failing_since is when the first of them happened. Once the check has
-- failed MaxAttempts times and the window since the first failure has passed,
-- the participant is paused: check_unavailable. Nothing it answered is
-- confirmed meanwhile; the admin's resume (or a new run) starts a fresh
-- window, and an answered check ends the count.
--
-- Locks: the participants' state is taken up front, NOWAIT, like 28-39. A
-- worker holding it makes the file fail at once; the next start retries it
-- whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_llm_provider_state IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_llm_provider_state
    ADD COLUMN check_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN check_failing_since TIMESTAMPTZ,
    ADD CONSTRAINT author_llm_provider_state_check_failures_check
        CHECK (check_failures >= 0 AND (check_failures = 0) = (check_failing_since IS NULL)),
    DROP CONSTRAINT author_llm_provider_state_pause_check,
    ADD CONSTRAINT author_llm_provider_state_pause_check
        CHECK ((paused_reason IS NULL) = (paused_at IS NULL)
               AND (paused_reason IS NULL
                    OR paused_reason IN ('auth', 'model_mismatch', 'bad_request', 'drift', 'over_reserve',
                                         'relearn_unstable', 'check_unavailable')));
