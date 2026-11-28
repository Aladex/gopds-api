-- 87: who pinned an author entity's name, and when (design/author-display,
-- name pins).
--
--   * author_entity_name_pin_log: one row per pin or unpin an administrator
--     made: the entity, the name it was pinned to (or carried when unpinned),
--     the name it had before, who did it and when. The pin itself is
--     author_entity.name_pinned with the row's names, which the build keeps
--     as it finds them (internal/authorentity/plan.go); this table is its
--     history. No foreign key to author_entity: a log outlives the rows it
--     names, and an unpinned entity the build retires must not be held back
--     by its history.
--
-- Locks: a new table only. No existing table is touched.

SET LOCAL lock_timeout = '5s';

CREATE TABLE public.author_entity_name_pin_log (
    id                    bigserial PRIMARY KEY,
    entity_id             uuid        NOT NULL,
    action                text        NOT NULL,
    display_name          text        NOT NULL,
    previous_display_name text        NOT NULL,
    actor_user_id         bigint REFERENCES public.auth_user (id) ON DELETE SET NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT author_entity_name_pin_log_action_check CHECK (action IN ('pin', 'unpin'))
);

CREATE INDEX author_entity_name_pin_log_entity_idx ON public.author_entity_name_pin_log (entity_id, created_at DESC);
