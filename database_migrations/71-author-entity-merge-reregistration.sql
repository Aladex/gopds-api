-- 71: a withdrawn merge kind registers again (E4c). 68 made a withdrawal
-- final for a configuration: UNIQUE (config_version, kind) refused a second
-- registration of the kind, withdrawn or not. Now at most one LIVE
-- registration per configuration and kind; a withdrawn one stays as it is
-- (history: never deleted, withdrawn once) and a new one may follow it.
--
--   * The key becomes a unique index on (config_version, kind) of the rows
--     not withdrawn.
--   * A new registration takes the script blocks of every earlier
--     registration of its configuration and kind, with their time and
--     actor: a block is never lifted by registering again (a block is
--     insert only, so a script once blocked stays blocked for the
--     configuration). database.BlockMergeScript holds the registration row
--     FOR SHARE until it commits, so a withdrawal waits for an open block
--     and the registration that follows the withdrawal carries it; a block
--     arriving during an open withdrawal is refused once that commits.
--
-- Decision rows do not change. The build applies a model decision under
-- the live registration of the decision's configuration and kind when the
-- decision was inserted under some registration of that configuration and
-- kind (database.LoadMergeDecisions), so the decisions of a withdrawn
-- registration apply again under the new one, and lapse again when it is
-- withdrawn.
--
-- Locks: the registrations ACCESS EXCLUSIVE, taken up front, NOWAIT, like
-- 28-70: a build reading them makes the file fail at once and the next
-- start retries it whole. The table holds a row per kind registered.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_entity_merge_registration IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.author_entity_merge_registration DROP CONSTRAINT author_entity_merge_registration_key;
CREATE UNIQUE INDEX author_entity_merge_registration_live
    ON public.author_entity_merge_registration (config_version, kind) WHERE withdrawn_at IS NULL;

-- A new registration carries the blocks of its configuration and kind.
CREATE FUNCTION public.author_entity_merge_registration_carry_blocks() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO public.author_entity_merge_script_block (registration_id, script, blocked_at, blocked_by_user_id)
    SELECT DISTINCT ON (b.script) NEW.id, b.script, b.blocked_at, b.blocked_by_user_id
    FROM public.author_entity_merge_script_block b
    JOIN public.author_entity_merge_registration r ON r.id = b.registration_id
    WHERE r.config_version = NEW.config_version AND r.kind = NEW.kind AND r.id <> NEW.id
    ORDER BY b.script, b.blocked_at, b.registration_id;
    RETURN NULL;
END
$$;

CREATE TRIGGER author_entity_merge_registration_carry_blocks
    AFTER INSERT ON public.author_entity_merge_registration
    FOR EACH ROW EXECUTE FUNCTION public.author_entity_merge_registration_carry_blocks();
