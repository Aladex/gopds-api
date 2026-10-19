-- Book files delivered into Telegram chats, as durable jobs.
--
--   * telegram_delivery — one row per request to send a book in a format to a
--     bound chat. A job moves queued → preparing → sending → sent, or ends
--     failed (a known refusal, with its outcome) or uncertain (the request may
--     have reached Telegram and its answer is unknown). Preparing is safe to
--     repeat; sending is never repeated: a job whose holder vanished while
--     sending is closed uncertain, not sent again.
--     A holder works under a lease (lease_owner, lease_expires_at) and a fence
--     that moves on with every claim, so a process that lost its lease — or
--     two processes alive during a rollout — cannot both act on one job.
--     Dedupe: at most one unfinished job per binding, book and format; and a
--     bot tap (tap_key) is recorded once, so a replayed press finds its job.
--   * telegram_user_settings — the account's bot preferences. A missing row
--     means the defaults (EPUB; books language of the site profile).
--
-- Locks: the foreign keys to auth_user take SHARE ROW EXCLUSIVE on it. Taken up
-- front, NOWAIT, like 28-43.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.auth_user IN SHARE ROW EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.telegram_delivery (
    id                bigserial   PRIMARY KEY,
    user_id           integer     NOT NULL REFERENCES public.auth_user (id) ON DELETE CASCADE,
    binding_id        bigint      NOT NULL REFERENCES public.telegram_binding (id) ON DELETE CASCADE,
    bot_ref           bigint      NOT NULL REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    chat_id           bigint      NOT NULL,
    book_id           bigint      NOT NULL,
    format            text        NOT NULL CHECK (format IN ('epub', 'fb2', 'mobi', 'zip')),
    origin            text        NOT NULL CHECK (origin IN ('bot', 'site', 'recent')),
    tap_key           text,
    state             text        NOT NULL DEFAULT 'queued'
                      CHECK (state IN ('queued', 'preparing', 'sending', 'sent', 'failed', 'uncertain', 'cancelled')),
    outcome           text,
    attempt           smallint    NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    lease_owner       uuid,
    lease_expires_at  timestamptz,
    fence             bigint      NOT NULL DEFAULT 0,
    origin_message_id bigint,
    origin_revision   integer,
    status_message_id bigint,
    file_bytes        bigint,
    tg_message_id     bigint,
    created_at        timestamptz NOT NULL DEFAULT now(),
    send_started_at   timestamptz,
    finished_at       timestamptz,
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CHECK ((state IN ('sent', 'failed', 'uncertain', 'cancelled')) = (finished_at IS NOT NULL)),
    CHECK ((state IN ('sent', 'failed', 'uncertain', 'cancelled')) = (outcome IS NOT NULL)),
    CHECK (state NOT IN ('sent', 'failed', 'uncertain', 'cancelled', 'queued') OR lease_owner IS NULL),
    CHECK (state <> 'sending' OR send_started_at IS NOT NULL),
    CHECK ((origin = 'bot') = (tap_key IS NOT NULL))
);
CREATE UNIQUE INDEX telegram_delivery_tap ON public.telegram_delivery (tap_key) WHERE tap_key IS NOT NULL;
CREATE UNIQUE INDEX telegram_delivery_active ON public.telegram_delivery (binding_id, book_id, format)
    WHERE finished_at IS NULL;
CREATE INDEX telegram_delivery_claim ON public.telegram_delivery (next_attempt_at, id)
    WHERE state IN ('queued', 'preparing');
CREATE INDEX telegram_delivery_sending ON public.telegram_delivery (lease_expires_at) WHERE state = 'sending';
CREATE INDEX telegram_delivery_user ON public.telegram_delivery (user_id, created_at DESC);
CREATE INDEX telegram_delivery_finished ON public.telegram_delivery (finished_at) WHERE finished_at IS NOT NULL;

CREATE TABLE public.telegram_user_settings (
    user_id        integer     PRIMARY KEY REFERENCES public.auth_user (id) ON DELETE CASCADE,
    default_format text        NOT NULL DEFAULT 'epub' CHECK (default_format IN ('epub', 'fb2', 'mobi', 'zip')),
    search_lang    text,
    updated_at     timestamptz NOT NULL DEFAULT now()
);
