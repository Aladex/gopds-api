-- 62: the author autopilot (services.AuthorAutopilot), a level-triggered
-- reconciler. Every tick it looks at the state, not at events: once the scan
-- is quiet and the local normalization queue drained, and while no LLM run
-- and no entity pass is active, it starts a full resolve run when the
-- registered strata have open review inputs never asked, or else an entity
-- pass when the selections changed since the last pass started. It converges:
-- whatever a tick misses, a later one sees.
--
--   * author_autopilot — its one row: the card's switch (on by default), the
--     lease of the pod that ticks, what the last tick found and did, and a
--     failed tick's backoff.
--   * author_autopilot_mark — "the selections changed": one row per
--     transaction that changed book_contributor_credit_selection (statement
--     triggers, so every writer marks, the binary before this one included).
--     A mark is visible exactly when its transaction committed, whatever the
--     order of commits. An entity pass, as it starts, takes every mark
--     visible then (build_run_id); a mark of a pass that completed is
--     dropped, one of a pass that failed is given back. An untaken mark is a
--     pass to do: a transaction that commits while a pass runs leaves one for
--     the next. One mark is seeded here, under the same lock: the selections
--     made before the triggers existed are built by the first pass after.
--
-- A pass that completed but skipped units (a row another writer held) owes
-- them; that debt is read from the passes themselves
-- (database.AuthorAutopilotBuildDebt), not from marks.
--
-- Locks: the trigger takes the selections SHARE ROW EXCLUSIVE, up front,
-- NOWAIT, like 28-61: a writer holding them makes this file fail at once,
-- and the next start applies it whole. The new tables are empty.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.book_contributor_credit_selection IN SHARE ROW EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.author_autopilot (
    id                 boolean PRIMARY KEY DEFAULT true,
    enabled            boolean NOT NULL DEFAULT true,
    lease_owner        uuid,
    lease_expires_at   timestamptz,
    -- What the last tick found: the gate that held it, the run it waited
    -- for or started, and how the resolve check came out.
    state              text,
    state_run_id       bigint,
    resolve_note       text,
    resolve_inputs     integer NOT NULL DEFAULT 0,
    -- The last run it started.
    last_action        text,
    last_action_run_id bigint,
    last_action_at     timestamptz,
    failures           integer NOT NULL DEFAULT 0,
    last_error_class   text,
    next_attempt_at    timestamptz,
    updated_by_user_id bigint,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT author_autopilot_one_row_check CHECK (id),
    CONSTRAINT author_autopilot_lease_check CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CONSTRAINT author_autopilot_codes_check
        CHECK ((state IS NULL OR state ~ '^[a-z][a-z_]{0,63}$')
               AND (resolve_note IS NULL OR resolve_note ~ '^[a-z][a-z_]{0,63}$')
               AND (last_error_class IS NULL OR last_error_class ~ '^[a-z0-9][a-z0-9_]{0,63}$')),
    CONSTRAINT author_autopilot_action_check
        CHECK ((last_action IS NULL OR last_action IN ('resolve', 'build'))
               AND (last_action IS NULL) = (last_action_run_id IS NULL)),
    CONSTRAINT author_autopilot_counts_check CHECK (failures >= 0 AND resolve_inputs >= 0)
);

INSERT INTO public.author_autopilot DEFAULT VALUES;

CREATE TABLE public.author_autopilot_mark (
    xact         xid8 PRIMARY KEY,
    created_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    -- The entity pass that took it; NULL while no pass has.
    build_run_id bigint
);

CREATE INDEX author_autopilot_mark_untaken ON public.author_autopilot_mark (xact) WHERE build_run_id IS NULL;
CREATE INDEX author_autopilot_mark_build ON public.author_autopilot_mark (build_run_id) WHERE build_run_id IS NOT NULL;

-- A statement marks only when it changed a row: one that inserted or deleted
-- none, or updated rows to what they were, leaves no pass to do.
CREATE FUNCTION public.author_autopilot_mark() RETURNS trigger
    LANGUAGE plpgsql AS $$
DECLARE
    changed_rows boolean;
BEGIN
    -- Each branch reads only the transition tables its trigger has.
    IF TG_OP = 'UPDATE' THEN
        changed_rows := EXISTS (SELECT * FROM changed EXCEPT SELECT * FROM previous);
    ELSE
        changed_rows := EXISTS (SELECT 1 FROM changed);
    END IF;
    IF changed_rows THEN
        INSERT INTO public.author_autopilot_mark (xact) VALUES (pg_current_xact_id()) ON CONFLICT (xact) DO NOTHING;
    END IF;
    RETURN NULL;
END
$$;

CREATE TRIGGER book_contributor_credit_selection_autopilot_insert
    AFTER INSERT ON public.book_contributor_credit_selection
    REFERENCING NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER book_contributor_credit_selection_autopilot_update
    AFTER UPDATE ON public.book_contributor_credit_selection
    REFERENCING OLD TABLE AS previous NEW TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();
CREATE TRIGGER book_contributor_credit_selection_autopilot_delete
    AFTER DELETE ON public.book_contributor_credit_selection
    REFERENCING OLD TABLE AS changed
    FOR EACH STATEMENT EXECUTE FUNCTION public.author_autopilot_mark();

INSERT INTO public.author_autopilot_mark (xact) VALUES (pg_current_xact_id());
