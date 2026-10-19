-- 48: a card's repaint that is owed after its delivery job ended (review of
-- the S4 fixes, R2).
--
--   * telegram_panel_repaint — a job ended while its chat was held by
--     another transition for longer than the job could wait. The repaint is
--     kept here: the next holder of the chat runs it before letting go, and
--     the inbox sweeper runs what is left, so a card never keeps its «⏳»
--     after its file was sent.
--
-- No foreign keys, like telegram_chat_lock: rows live until their repaint
-- runs. Nothing existing is locked.

CREATE TABLE public.telegram_panel_repaint (
    id         bigserial   PRIMARY KEY,
    bot_ref    bigint      NOT NULL,
    chat_id    bigint      NOT NULL,
    message_id bigint      NOT NULL,
    binding_id bigint      NOT NULL,
    book_id    bigint      NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX telegram_panel_repaint_chat ON public.telegram_panel_repaint (bot_ref, chat_id);
