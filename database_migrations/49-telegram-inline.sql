-- 49: whether a v2 bot answers inline queries (plan-ui.md §3.1, S5b).
--
--   * telegram_bot_ui.supports_inline — getMe's supports_inline_queries, as
--     last seen: at bot start, in the daily health check, on «Проверить»,
--     and set by any inline query that arrives. It decides whether the home
--     panel offers «Найти с подсказками».
--   * telegram_bot_ui.inline_checked_at — when that was last asked.
--
-- On telegram_bot_ui rather than telegram_bot (plan-ui named the latter):
-- every webhook reads telegram_bot, and its ALTER would wait on that
-- traffic. Adding a column with a constant default rewrites nothing.
--
-- Locks: ACCESS EXCLUSIVE on telegram_bot_ui, up front, NOWAIT, like 28-46.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.telegram_bot_ui IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.telegram_bot_ui
    ADD COLUMN supports_inline   boolean     NOT NULL DEFAULT false,
    ADD COLUMN inline_checked_at timestamptz;
