-- 55: the v2 bot's one command, /start — «Главное меню» (decisions-ui §1,
-- amended 2026-10-20: one command brings the home panel back).
--
--   * telegram_bot_ui.commands_set_at — when the bot's command menu was set
--     to /start and its menu button to the commands. A new column rather
--     than commands_cleared_at reused: every bot cleared before has its menu
--     set once more, and code rolled back still finds its own mark.
--
-- Locks: ACCESS EXCLUSIVE on telegram_bot_ui, up front, NOWAIT, like 28-54.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.telegram_bot_ui IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.telegram_bot_ui
    ADD COLUMN commands_set_at timestamptz;
