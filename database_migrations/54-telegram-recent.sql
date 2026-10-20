-- 54: recent sends (plan.md P1-3, concept S-RECENT and S-RECENT-SETTINGS).
--
--   * telegram_recent — the books Telegram confirmed it delivered to the
--     account's chat, one row per book: its last format and when. A book sent
--     again moves up, it does not repeat. Kept for the last 50 books within
--     90 days (the janitor trims the rest). It is a list of sends, not of
--     reading: a confirmed send proves nothing more.
--   * telegram_user_settings.keep_recent — «Сохранять» (the default) or «Не
--     сохранять». Off stops new rows; it does not clear the list.
--
-- Locks: the foreign key takes SHARE ROW EXCLUSIVE on auth_user; the new
-- column takes ACCESS EXCLUSIVE on telegram_user_settings (a constant
-- default: no rewrite). All up front, NOWAIT, like 28-53.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.auth_user IN SHARE ROW EXCLUSIVE MODE NOWAIT;
LOCK TABLE public.telegram_user_settings IN ACCESS EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.telegram_recent (
    user_id integer     NOT NULL REFERENCES public.auth_user (id) ON DELETE CASCADE,
    book_id bigint      NOT NULL,
    format  text        NOT NULL,
    sent_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, book_id)
);
CREATE INDEX telegram_recent_newest ON public.telegram_recent (user_id, sent_at DESC);

ALTER TABLE public.telegram_user_settings
    ADD COLUMN keep_recent boolean NOT NULL DEFAULT true;
