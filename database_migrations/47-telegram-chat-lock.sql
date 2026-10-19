-- 47: one panel transition at a time per chat (review of S4, B1).
--
--   * telegram_chat_lock — held while a v2 transition reads a panel, moves
--     it and edits the message, so that two edits of one chat never reach
--     Telegram out of order, whichever pod sends them. A row, not an
--     advisory lock: an advisory lock pins a pooled connection for the
--     length of a Telegram call. expires_at bounds a holder that died.
--
-- No foreign keys: rows live for one transition, and a bot's leftovers
-- expire on their own. Nothing existing is locked.

CREATE TABLE public.telegram_chat_lock (
    bot_ref    bigint      NOT NULL,
    chat_id    bigint      NOT NULL,
    holder     text        NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (bot_ref, chat_id)
);
