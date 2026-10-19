-- The new bot interface's state (plan.md §2.4, plan-ui.md §5.1).
--
--   * telegram_view — an immutable query scope: what a list shows (a search
--     with its language, later an author, a series, a collection, favourites).
--   * telegram_node — one screen of one view, with the screen it came from;
--     «Назад» walks parent_id. Reused on conflict, so moving back and forth
--     does not grow the table.
--   * telegram_panel — a bot message that shows nodes and is edited in place.
--     revision is the compare-and-set token every button carries; mode is
--     fixed when the panel is sent (a message never switches between plain
--     text and rich content by an edit); is_home marks the chat's pinned
--     home panel.
--   * telegram_prompt — a question waiting for a reply (author filter and the
--     like; used from S5).
--   * telegram_cover_file — a cover's Telegram file_id once a bot uploaded
--     it; file ids belong to the bot that uploaded them.
--   * telegram_bot_ui — per-bot interface facts: when its command menu was
--     cleared, and when Telegram last refused it a rich message. A table of
--     its own rather than columns of telegram_bot, which every webhook reads:
--     altering that one would wait on live traffic.
--   * telegram_user_settings.card_style — rich (default) or plain cards.
--
-- Node parent_id and view_id are plain bigints: retention deletes old tails,
-- and a dangling reference is the defined «no longer saved» state.
--
-- Locks: the foreign keys take SHARE ROW EXCLUSIVE on auth_user, telegram_bot
-- and telegram_binding; the ALTER takes ACCESS EXCLUSIVE on
-- telegram_user_settings. All up front, NOWAIT, like 28-45.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.auth_user IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.telegram_bot IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.telegram_binding IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.telegram_user_settings IN ACCESS EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.telegram_view (
    id            bigserial   PRIMARY KEY,
    user_id       integer     NOT NULL REFERENCES public.auth_user (id) ON DELETE CASCADE,
    kind          smallint    NOT NULL,
    query         text        NOT NULL DEFAULT '' CHECK (char_length(query) <= 200),
    lang          text        NOT NULL DEFAULT '',
    author_id     bigint      NOT NULL DEFAULT 0,
    series_id     bigint      NOT NULL DEFAULT 0,
    collection_id bigint      NOT NULL DEFAULT 0,
    favorites     boolean     NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX telegram_view_user ON public.telegram_view (user_id, id);
CREATE INDEX telegram_view_lru ON public.telegram_view (last_used_at);

CREATE TABLE public.telegram_node (
    id           bigserial   PRIMARY KEY,
    user_id      integer     NOT NULL REFERENCES public.auth_user (id) ON DELETE CASCADE,
    view_id      bigint      NOT NULL DEFAULT 0,
    screen       smallint    NOT NULL,
    entity_id    bigint      NOT NULL DEFAULT 0,
    page         integer     NOT NULL DEFAULT 0 CHECK (page BETWEEN 0 AND 16777215),
    parent_id    bigint      NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, view_id, screen, entity_id, page, parent_id)
);
CREATE INDEX telegram_node_lru ON public.telegram_node (last_used_at);

CREATE TABLE public.telegram_panel (
    bot_ref        bigint      NOT NULL REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    chat_id        bigint      NOT NULL,
    message_id     bigint      NOT NULL,
    user_id        integer     NOT NULL REFERENCES public.auth_user (id) ON DELETE CASCADE,
    binding_id     bigint      NOT NULL REFERENCES public.telegram_binding (id) ON DELETE CASCADE,
    node_id        bigint      NOT NULL DEFAULT 0,
    revision       integer     NOT NULL DEFAULT 1 CHECK (revision BETWEEN 1 AND 65534),
    mode           text        NOT NULL DEFAULT 'plain' CHECK (mode IN ('plain', 'rich')),
    is_home        boolean     NOT NULL DEFAULT false,
    pinned_at      timestamptz,
    pin_checked_at timestamptz,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bot_ref, chat_id, message_id)
);
CREATE UNIQUE INDEX telegram_panel_home ON public.telegram_panel (bot_ref, chat_id) WHERE is_home;

CREATE TABLE public.telegram_prompt (
    bot_ref           bigint      NOT NULL REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    chat_id           bigint      NOT NULL,
    prompt_message_id bigint      NOT NULL,
    user_id           integer     NOT NULL REFERENCES public.auth_user (id) ON DELETE CASCADE,
    kind              smallint    NOT NULL,
    source_message_id bigint      NOT NULL,
    source_node_id    bigint      NOT NULL,
    source_revision   integer     NOT NULL,
    state             text        NOT NULL DEFAULT 'open'
                      CHECK (state IN ('open', 'answered', 'cancelled', 'expired')),
    expires_at        timestamptz NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bot_ref, chat_id, prompt_message_id)
);
CREATE INDEX telegram_prompt_open ON public.telegram_prompt (bot_ref, chat_id, created_at DESC) WHERE state = 'open';

CREATE TABLE public.telegram_cover_file (
    bot_ref        bigint      NOT NULL REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    book_id        bigint      NOT NULL,
    file_id        text        NOT NULL,
    file_unique_id text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bot_ref, book_id)
);

CREATE TABLE public.telegram_bot_ui (
    bot_ref             bigint      PRIMARY KEY REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    commands_cleared_at timestamptz,
    rich_rejected_at    timestamptz
);

ALTER TABLE public.telegram_user_settings
    ADD COLUMN card_style text NOT NULL DEFAULT 'rich' CHECK (card_style IN ('rich', 'plain'));
