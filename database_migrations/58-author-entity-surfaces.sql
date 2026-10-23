-- 58: what the surfaces need to name authors by their entities (design E,
-- phase E3, behind authors.identity).
--
--   * author_entity_display_name_idx — a namesake is another live entity of
--     the same display name (§6 item 3a): the label that tells them apart is
--     shown only then, and the lookup asks it of every entity a page shows.
--   * telegram_author_entity — the bot's handle of an entity. A bot button
--     carries one signed 64-bit ID (telegram/cbv2), which a UUID does not
--     fit; the bot names an entity by its handle instead, made the first time
--     a button names it. Handles start at 2^40, far above any legacy author
--     ID, so a button or a list made under one identity is told apart under
--     the other: the bot answers "author not found", never another author.
--     Not a foreign key: the entity build never waits on the bot, and a
--     handle of an entity that is gone or merged resolves to nothing.
--
-- Locks: author_entity up front, NOWAIT, like 56-57: a running entity build
-- makes this fail at once, and the next start applies it whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_entity IN SHARE MODE NOWAIT;

CREATE INDEX author_entity_display_name_idx ON public.author_entity (display_name) WHERE merged_into IS NULL;

CREATE SEQUENCE public.telegram_author_entity_id_seq START WITH 1099511627776 MINVALUE 1099511627776;

CREATE TABLE public.telegram_author_entity (
    id         bigint      PRIMARY KEY DEFAULT nextval('public.telegram_author_entity_id_seq'),
    entity_id  uuid        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT telegram_author_entity_entity_uniq UNIQUE (entity_id),
    CONSTRAINT telegram_author_entity_id_check CHECK (id >= 1099511627776)
);

ALTER SEQUENCE public.telegram_author_entity_id_seq OWNED BY public.telegram_author_entity.id;
