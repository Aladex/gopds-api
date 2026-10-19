-- 50: the delivery job a card repaint is owed for (notes item 8).
--
--   * telegram_panel_repaint.job_id — the job whose end owes the repaint. A
--     repaint recorded for a panel that was replaced meanwhile (a rich card
--     sent again as plain) follows the job to the panel it names now: the
--     replacement takes the job's origin over, and a repaint recorded from
--     a snapshot older than that would otherwise land on the retired panel.
--     NULL for repaints recorded before this column.
--
-- Locks: ACCESS EXCLUSIVE on telegram_panel_repaint, up front, NOWAIT. A new
-- nullable column rewrites nothing.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.telegram_panel_repaint IN ACCESS EXCLUSIVE MODE NOWAIT;

ALTER TABLE public.telegram_panel_repaint ADD COLUMN job_id bigint;
