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
 * the action area: wide, it is a rail beside the text holding every format
 * and send button; narrow, it is a row of three verbs whose formats and send
 * targets open in sheets. Those are different controls, not the same ones
 * restyled, and rendering both would put two copies of every button in the
 * document — two tab stops, two announcements to a screen reader, and two
 * elements answering to the same name. The byline's counter makes the same
 * choice (a dropdown beside it, or a sheet).
 *
 * So one decision stays in JS, and this module keeps it honest.
 *
 * The number is where the rail has room. The wide card is cover (108px) | text
 * | rail (240px) with 24px gaps and 20px padding, and below a 760px card the
 * text column drops under ~320px — the 156-character title runs to seven
 * lines. The list draws cards inside the page's 1rem padding on either side,
 * and a classic scrollbar takes up to 15px more, so a 760px card needs an
 * 807px viewport: 51rem. Tailwind's own `md` (48rem) would hand the rail a
 * 721–736px card. The stylesheet names it `card` (`--breakpoint-card` in
 * index.css).
 *
 * The unit matters as much as the number. Tailwind emits the variant as
 * `@media (width >= 51rem)`, and a media query's rem is the reader's own font
 * size, not ours. Asking in pixels instead would agree only while that size is
 * 16px: a reader who set 20px would get CSS switching at 1020px and React at
 * 816px. So the query is written in the same unit Tailwind uses.
 */
export const CARD_WIDE_MIN_WIDTH_REM = 51;

/**
 * The Tailwind variant the card styles its wide layout with.
 *
 * Named on purpose. A class may carry the right width and still add a
 * condition of its own inside the same query — a theme, a container, a print
 * rule — and arrive at a layout React never agreed to. Requiring the name
 * means nothing whose meaning has to be re-derived gets in.
 */
export const CARD_WIDE_VARIANT = 'card';

/** The media query the card's layout decision is made with. */
export const CARD_WIDE_QUERY = `(min-width: ${CARD_WIDE_MIN_WIDTH_REM}rem)`;

/**
 * Where a phone's layout ends: the two-sheet split, the phone's pager and the
 * rest of what a thumb decides. It is the one boundary here written in pixels,
 * and four components have spelled it out since before this file existed.
 */
export const PHONE_QUERY = '(max-width: 600px)';

/**
 * Where the header has room to keep the donate pill clear of the field.
 *
 * Green beside a field reads as the button that submits it. From here up the
 * field stops at its 560px and the pill stands far to the right of it; below,
 * the field takes the row and the pill would stand 24px from it, so it goes
 * left of the field instead. Rem, like Tailwind's own `lg`, so a reader's font
 * size moves this with the stylesheet.
 */
export const HEADER_WIDE_MIN_WIDTH_REM = 64;
export const HEADER_WIDE_VARIANT = 'lg';
export const HEADER_WIDE_QUERY = `(min-width: ${HEADER_WIDE_MIN_WIDTH_REM}rem)`;

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
 * Where the admin tables give way to cards.
 *
 * The admin screens run their own 1400px column and their tables carry five
 * or six columns; below this width those rows stop fitting and each row
 * becomes a card. The scan screens already switched at 900px, so the review
 * queue joins the same boundary rather than inventing a second one — and it
 * is written in the rem unit Tailwind's own variants use, so a reader's font
 * size moves CSS and React together.
 */
export const ADMIN_TABLE_WIDE_MIN_WIDTH_REM = 56.25;
export const ADMIN_TABLE_WIDE_QUERY = `(min-width: ${ADMIN_TABLE_WIDE_MIN_WIDTH_REM}rem)`;

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
 * is LIST_COLUMN_ATTR below. The chrome over a list keeps to it too, from the
 * header's row down, so a wide monitor does not set the logo a long way from the
 * books. That is this width and not the attribute: the attribute marks the one
 * box a pager and a landing look for. OpdsInfo and CollectionView still spell
 * the literal, and changing them is a tidying job of its own.
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
