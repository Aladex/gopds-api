-- 53: the library's look given to a reader's bot (plan.md P1-2).
--
--   * telegram_bot_profile — when the library's avatar and descriptions were
--     applied to a bot: to a managed bot when the manager set it up (its
--     readiness waits for it), to a bot connected by its token only when its
--     owner asked on the site. A bot with a row is never given them again by
--     itself: an owner who changed them keeps the change.
--
-- Locks: the foreign key takes SHARE ROW EXCLUSIVE on telegram_bot, up front,
-- NOWAIT, like 28-52.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.telegram_bot IN SHARE ROW EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.telegram_bot_profile (
    bot_ref    bigint      PRIMARY KEY REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    applied_at timestamptz NOT NULL DEFAULT now()
);
