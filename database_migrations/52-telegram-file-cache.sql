-- 52: a bot's own copy of the files it sent (plan.md P1-4).
--
--   * telegram_file_cache — the file_id Telegram gave a bot for a book in a
--     format. A file_id belongs to the bot that uploaded it, so the row is
--     keyed by the bot. It is good only while the file it stands for is the
--     same: source_rev is the book archive's md5 (no md5, no row) and
--     converter_rev the converter's revision (delivery.ConverterRev). A row
--     whose revisions do not match is not used, and the next upload replaces
--     it. file_id never leaves the bot: it is not shown on the site, nor put
--     into inline results.
--
-- Locks: the foreign key takes SHARE ROW EXCLUSIVE on telegram_bot, up front,
-- NOWAIT, like 28-51. No key to the catalog: a row of a deleted book is never
-- asked for again.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.telegram_bot IN SHARE ROW EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.telegram_file_cache (
    bot_ref        bigint      NOT NULL REFERENCES public.telegram_bot (id) ON DELETE CASCADE,
    book_id        bigint      NOT NULL,
    format         text        NOT NULL,
    source_rev     text        NOT NULL CHECK (source_rev <> ''),
    converter_rev  integer     NOT NULL,
    file_id        text        NOT NULL,
    file_unique_id text        NOT NULL DEFAULT '',
    bytes          bigint      NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bot_ref, book_id, format)
);
