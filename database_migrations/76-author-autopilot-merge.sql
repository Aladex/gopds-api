-- 76: the autopilot's merge step (E4e, plan §6). Once a scan is quiet, the
-- registered strata resolved and the entities built, the autopilot starts a
-- merge run of the merge configuration over the candidates due — of a
-- registered kind, never asked — and the build after it lands the
-- decisions. The step has a switch of its own on the autopilot's card, off
-- here: an administrator turns it on, so it never takes the LLM slot from
-- an eval they mean to run.
--
--   * author_autopilot.merge_enabled — the merge step's switch, off.
--   * author_autopilot.merge_note, merge_candidates — how the last merge
--     check came out and the candidates it found due, as resolve_note and
--     resolve_inputs say for the resolve check.
--   * author_autopilot.last_action admits merge.
--
-- The existing row keeps every value; only checks widen.
--
-- Locks: the row is taken up front, NOWAIT, like 28-75: a tick holding it
-- makes the file fail at once, and the next start applies it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_autopilot IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_autopilot
    ADD COLUMN merge_enabled    boolean NOT NULL DEFAULT false,
    ADD COLUMN merge_note       text,
    ADD COLUMN merge_candidates integer NOT NULL DEFAULT 0,
    DROP CONSTRAINT author_autopilot_codes_check,
    ADD CONSTRAINT author_autopilot_codes_check
        CHECK ((state IS NULL OR state ~ '^[a-z][a-z_]{0,63}$')
               AND (resolve_note IS NULL OR resolve_note ~ '^[a-z][a-z_]{0,63}$')
               AND (merge_note IS NULL OR merge_note ~ '^[a-z][a-z_]{0,63}$')
               AND (last_error_class IS NULL OR last_error_class ~ '^[a-z0-9][a-z0-9_]{0,63}$')),
    DROP CONSTRAINT author_autopilot_action_check,
    ADD CONSTRAINT author_autopilot_action_check
        CHECK ((last_action IS NULL OR last_action IN ('resolve', 'build', 'merge'))
               AND (last_action IS NULL) = (last_action_run_id IS NULL)),
    DROP CONSTRAINT author_autopilot_counts_check,
    ADD CONSTRAINT author_autopilot_counts_check
        CHECK (failures >= 0 AND resolve_inputs >= 0 AND merge_candidates >= 0);
