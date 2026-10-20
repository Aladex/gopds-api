-- 51: Managed Bots onboarding (plan.md P1-1, concept §C.3).
--
--   * telegram_pending_connection — a reader pressed «Создать личного бота»:
--     the site waits for Telegram to say a bot was made. discovery_label is
--     the non-secret mark in the suggested username (bd{label}bot) by which
--     the bot is found; the proof of ownership is later the one-time link
--     (telegram_link_token), whose /start must come from the bot's creator.
--     state: waiting (no bot yet), found (a bot is the reader's candidate),
--     canceled, linked.
--   * telegram_managed_event — every managed_bot update the manager got,
--     once per update_id: a repeated delivery changes nothing. One process
--     handles an event at a time: it claims it for a while (claimed_by,
--     claim_expires_at); an event whose claim ran out unhandled is taken up
--     again.
--   * telegram_managed_bot — what the manager confirmed about a bot it
--     manages: when it was found ready (token, restricted access, webhook
--     answered), and whether its access is restricted. last_update_id is the
--     newest event applied to the bot: an older one, delivered late, never
--     changes its owner back.
--
-- Locks: the foreign keys take SHARE ROW EXCLUSIVE on auth_user and
-- telegram_bot, up front, NOWAIT, like 28-50.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.auth_user IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.telegram_bot IN SHARE ROW EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.telegram_pending_connection (
    id              bigserial   PRIMARY KEY,
    user_id         integer     NOT NULL REFERENCES public.auth_user (id) ON DELETE CASCADE,
    discovery_label text        NOT NULL UNIQUE CHECK (discovery_label ~ '^[a-z2-7]{8}$'),
    state           text        NOT NULL DEFAULT 'waiting'
                    CHECK (state IN ('waiting', 'found', 'canceled', 'linked')),
    bot_ref         bigint      REFERENCES public.telegram_bot (id) ON DELETE SET NULL,
    expires_at      timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX telegram_pending_connection_open ON public.telegram_pending_connection (user_id, created_at DESC)
    WHERE state IN ('waiting', 'found');

CREATE TABLE public.telegram_managed_event (
    update_id     bigint      PRIMARY KEY,
    bot_tg_id     bigint      NOT NULL,
    creator_tg_id bigint      NOT NULL,
    bot_username  text        NOT NULL DEFAULT '',
    received_at   timestamptz NOT NULL DEFAULT now(),
    claimed_by       uuid,
    claim_expires_at timestamptz,
    processed_at  timestamptz
);
CREATE INDEX telegram_managed_event_open ON public.telegram_managed_event (received_at)
    WHERE processed_at IS NULL;

CREATE TABLE public.telegram_managed_bot (
    bot_ref           bigint      PRIMARY KEY REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    access_restricted boolean     NOT NULL DEFAULT false,
    last_update_id    bigint      NOT NULL DEFAULT 0,
    ready_at          timestamptz,
    checked_at        timestamptz NOT NULL DEFAULT now()
);
