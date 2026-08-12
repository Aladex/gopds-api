/**
 * The one place the card's layout boundary is written down.
 *
 * The card had two of them: the list asked useMediaQuery for 600px while the
 * card branched on Tailwind's `sm:` classes at 40rem. Between the two the pair
 * disagreed — CSS still laid the card out narrow while React had already
 * handed it the wide branch — and the block of download buttons landed inside
 * a container built for the other layout.
 *
 * Layout belongs in CSS, and most of this card's does. What cannot go there is
 * the block of format buttons: narrow, it sits below the rule at the foot of
 * the card; wide, it sits under the cover, in a different parent. No media
 * query moves a node between parents, and rendering it twice would put two
 * copies of every button in the document — two tab stops, two announcements to
 * a screen reader, and two elements answering to the same name.
 *
 * So one decision stays in JS, and this module keeps it honest.
 *
 * The unit matters as much as the number. Tailwind emits `sm:` as
 * `@media (width >= 40rem)`, and a media query's rem is the reader's own font
 * size, not ours. Asking for 640px instead would agree only while that size is
 * 16px: a reader who set 20px would get CSS switching at 800px and React at
 * 640px — the same defect as before, over a band four times wider. So the
 * query is written in the same unit Tailwind uses.
 */
export const CARD_WIDE_MIN_WIDTH_REM = 40;

/** The media query the card's layout decision is made with. */
export const CARD_WIDE_QUERY = `(min-width: ${CARD_WIDE_MIN_WIDTH_REM}rem)`;
