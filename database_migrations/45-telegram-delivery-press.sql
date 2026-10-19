-- Every press of a bot button that a delivery job answered, beside the press
-- that made the job (telegram_delivery.tap_key).
--
-- A press that repeats a request — the same button again while its job runs,
-- or within a few seconds after — is answered with the existing job. Its
-- update may still be replayed later, after its callback answer failed; the
-- replay must find the same job, not make a second file once the repeat window
-- is over. So the press is recorded here, with the job it was answered with,
-- in the transaction that decided it.
--
-- Locks: the foreign key takes SHARE ROW EXCLUSIVE on telegram_delivery. Taken
-- up front, NOWAIT, like 28-44.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.telegram_delivery IN SHARE ROW EXCLUSIVE MODE NOWAIT;

CREATE TABLE public.telegram_delivery_press (
    tap_key    text        PRIMARY KEY,
    job_id     bigint      NOT NULL REFERENCES public.telegram_delivery (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX telegram_delivery_press_job ON public.telegram_delivery_press (job_id);
