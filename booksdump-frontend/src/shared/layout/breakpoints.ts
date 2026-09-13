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

/**
 * The Tailwind variant the card styles its wide layout with.
 *
 * Named on purpose. A class may carry the right width and still add a
 * condition of its own inside the same query — a theme, a container, a print
 * rule — and arrive at a layout React never agreed to. Requiring the name
 * means nothing whose meaning has to be re-derived gets in.
 */
export const CARD_WIDE_VARIANT = 'sm';

/** The media query the card's layout decision is made with. */
export const CARD_WIDE_QUERY = `(min-width: ${CARD_WIDE_MIN_WIDTH_REM}rem)`;

/**
 * Where the reader can afford a contents column beside the text.
 *
 * This is a different question from the card's, and answering it with the
 * card's number made the reading worse across a whole band of widths. The
 * contents column takes a fixed 14rem plus its gap out of the work area, so
 * below this width the text is what pays: measured in Chrome at 640px the
 * line ran to 25 characters and at 768px to 38, while the same widths with
 * the panel instead gave 50 and 62. A reader is better served by a full
 * measure and a panel a tap away.
 *
 * The number is where the text still reaches its 62-character measure with
 * the column present — measured, not derived: 61 characters at 1000px, the
 * full 62 at 1024. It is Tailwind's own `lg`, so the stylesheet can express
 * the same boundary by name if it ever needs to.
 */
export const READER_TOC_MIN_WIDTH_REM = 64;
export const READER_TOC_VARIANT = 'lg';
export const READER_TOC_QUERY = `(min-width: ${READER_TOC_MIN_WIDTH_REM}rem)`;

/**
 * The 1200px column the reader-facing list routes draw inside.
 *
 * Named, not repeated: the book cards have always had it and the pager had
 * not, so on a wide viewport the pager measured the whole content area and
 * fitted its window to a row nobody could see the edges of — its arrows ended
 * up a couple of hundred pixels inside the cards above with nothing but air on
 * either side.
 *
 * It is a width, not the contract. The admin tables run to their own 1400px
 * column and are no less correct for it; what every list page has to get right
 * is LIST_COLUMN_ATTR below. Several routes that hold no pager still spell the
 * literal — AuthorSearch's own SearchBar, OpdsInfo, AppSkeleton — and changing
 * them is a tidying job of its own rather than part of this one.
 */
export const CONTENT_COLUMN = 'mx-auto w-full max-w-[1200px]';

/**
 * The attribute that marks a list page's column.
 *
 * BookPagination spreads its row to the whole of its parent and fits its window
 * to what that row measures, which only tells the reader the truth when the
 * parent is the box the list above it occupies. That is a contract between the
 * pager and its caller, and it was already got wrong once: the author search
 * put its rows in a centred 1200px column and its pager in the page's
 * full-width root, so the pager measured the viewport.
 *
 * So the column is marked rather than assumed. Every list page puts this on the
 * one element that holds both the list and its pager, whatever width that
 * element happens to be — the admin tables run to 1400px and the catalogue to
 * 1200. The pager checks for it in development and says so when it is missing.
 */
export const LIST_COLUMN_ATTR = 'data-list-column';
export const LIST_COLUMN_SELECTOR = `[${LIST_COLUMN_ATTR}]`;
/** Spread onto the element that establishes a list page's column. */
export const listColumn = { [LIST_COLUMN_ATTR]: '' };
