-- Send to Kindle. Four columns on auth_user:
--
--   * kindle_email — the reader's Send-to-Kindle address, '' while they have
--     not given one. Validated and lower-cased by the application; the column
--     holds what it was told.
--   * kindle_sender_id — the local part of the address the reader's books are
--     mailed from, {kindle_sender_id}@{sender domain}. The reader puts that
--     address on Amazon's approved list once, so it must never change: every
--     existing row gets one here (a volatile default is evaluated per row) and
--     every new row gets one from the default. Unique, because two readers
--     sharing a sender would share Amazon's per-sender throttling and each
--     other's approval.
--   * show_kindle_button / show_telegram_button — whether the book card shows
--     each of the two buttons. Shown unless the reader turns them off.
--
-- Locks: auth_user is taken up front, NOWAIT, like 28-41. A login stamping
-- last_login holds it for a moment; the file then fails at once rather than
-- queueing every other login behind the rewrite, and the next start retries it
-- whole.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.auth_user IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.auth_user
    ADD COLUMN kindle_email character varying(254) NOT NULL DEFAULT '',
    ADD COLUMN kindle_sender_id uuid NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN show_kindle_button boolean NOT NULL DEFAULT true,
    ADD COLUMN show_telegram_button boolean NOT NULL DEFAULT true;

CREATE UNIQUE INDEX auth_user_kindle_sender_id_key ON public.auth_user (kindle_sender_id);
