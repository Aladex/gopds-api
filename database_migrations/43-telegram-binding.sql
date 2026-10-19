-- The personal Telegram bots, who they belong to, and the updates they receive.
--
--   * telegram_bot — one row per bot the library talks to. The token, the
--     webhook secret and the key that signs the bot's buttons are stored
--     encrypted by the application (AES-GCM, key_id names the key); the token's
--     SHA-256 lets "this token is already connected" be answered without
--     decrypting anything. webhook_path is the bot's URL path; existing bots
--     keep the auth_user.webhook_uuid Telegram already knows.
--   * telegram_binding — which library account a bot serves and which Telegram
--     user owns it. One active binding per account, per bot and per Telegram
--     user; relinking revokes the old row and creates a new one with the bot's
--     next generation, so buttons signed for the old owner stop working.
--   * telegram_link_token — the one-time link the site gives out. Only the
--     SHA-256 of the secret is stored; a consumed token remembers the binding
--     it created, so replaying the same /start answers instead of relinking.
--   * telegram_update_inbox — every accepted update, written before the
--     webhook answers 200, deduplicated by (bot, update_id). The process that
--     inserts a row holds a short lease on it; another process takes over a
--     row only after the lease ran out, so two pods alive during a rollout
--     never handle one update twice. Processed rows drop their payload.
--
-- The old auth_user columns (bot_token, webhook_uuid, telegram_id) stay; the
-- application copies them in at start and stops reading them.
--
-- Locks: the foreign keys to auth_user take SHARE ROW EXCLUSIVE on it. Taken up
-- front, NOWAIT, like 28-42: a login stamping last_login makes the file fail at
-- once instead of queueing every login behind it, and the next start retries.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.auth_user IN SHARE ROW EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.telegram_bot (
    id                 bigserial   PRIMARY KEY,
    tg_bot_id          bigint      NOT NULL UNIQUE,
    username           text        NOT NULL DEFAULT '',
    kind               text        NOT NULL CHECK (kind IN ('manual', 'managed')),
    added_by_user_id   integer     REFERENCES public.auth_user (id) ON DELETE SET NULL,
    token_enc          bytea,
    token_sha256       bytea       UNIQUE,
    key_id             smallint    NOT NULL DEFAULT 1,
    webhook_path       uuid        NOT NULL UNIQUE,
    webhook_secret_enc bytea       NOT NULL,
    nav_key_enc        bytea       NOT NULL,
    next_generation    integer     NOT NULL DEFAULT 1 CHECK (next_generation BETWEEN 1 AND 65536),
    creator_tg_id      bigint,
    status             text        NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'token_rejected', 'owner_changed', 'disabled')),
    router             text        NOT NULL DEFAULT 'legacy' CHECK (router IN ('legacy', 'v2', 'maintenance')),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CHECK ((token_enc IS NULL) = (token_sha256 IS NULL))
);

CREATE TABLE public.telegram_binding (
    id                  bigserial   PRIMARY KEY,
    user_id             integer     NOT NULL REFERENCES public.auth_user (id) ON DELETE CASCADE,
    bot_ref             bigint      NOT NULL REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    tg_user_id          bigint      NOT NULL,
    tg_display          text        NOT NULL DEFAULT '',
    generation          integer     NOT NULL CHECK (generation BETWEEN 1 AND 65535),
    status              text        NOT NULL CHECK (status IN ('active', 'revoked')),
    blocked_at          timestamptz,
    reply_kb_removed_at timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    revoked_at          timestamptz,
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL)),
    UNIQUE (bot_ref, generation)
);
CREATE UNIQUE INDEX telegram_binding_one_per_user ON public.telegram_binding (user_id) WHERE status = 'active';
CREATE UNIQUE INDEX telegram_binding_one_per_bot ON public.telegram_binding (bot_ref) WHERE status = 'active';
CREATE UNIQUE INDEX telegram_binding_one_per_tg ON public.telegram_binding (tg_user_id) WHERE status = 'active';

CREATE TABLE public.telegram_link_token (
    id             bigserial   PRIMARY KEY,
    user_id        integer     NOT NULL REFERENCES public.auth_user (id) ON DELETE CASCADE,
    bot_ref        bigint      NOT NULL REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    secret_sha256  bytea       NOT NULL UNIQUE,
    expires_at     timestamptz NOT NULL,
    consumed_at    timestamptz,
    consumed_by_tg bigint,
    binding_id     bigint      REFERENCES public.telegram_binding (id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK ((consumed_at IS NULL) = (consumed_by_tg IS NULL))
);
CREATE INDEX telegram_link_token_expiry ON public.telegram_link_token (expires_at) WHERE consumed_at IS NULL;

CREATE TABLE public.telegram_update_inbox (
    bot_ref          bigint      NOT NULL REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    update_id        bigint      NOT NULL,
    kind             text        NOT NULL,
    payload          jsonb,
    state            text        NOT NULL DEFAULT 'pending'
                     CHECK (state IN ('pending', 'done', 'skipped', 'failed')),
    lease_owner      uuid,
    lease_expires_at timestamptz,
    attempts         smallint    NOT NULL DEFAULT 0,
    received_at      timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz,
    PRIMARY KEY (bot_ref, update_id),
    CHECK ((state = 'pending') = (finished_at IS NULL)),
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CHECK (state = 'pending' OR lease_owner IS NULL)
);
CREATE INDEX telegram_update_inbox_pending ON public.telegram_update_inbox (lease_expires_at) WHERE state = 'pending';
CREATE INDEX telegram_update_inbox_finished ON public.telegram_update_inbox (finished_at) WHERE state <> 'pending';
