/**
 * The one place the card's layout boundary is written down.
 *
 * The card had two of them: the list asked useMediaQuery for 600px while the
 * card itself branched on Tailwind's `sm:` classes at 640px. Between 601 and
 * 639 the two disagreed — CSS still laid the card out narrow while React had
 * already handed it the wide branch — and the block of download buttons landed
 * inside a container built for the other layout.
 *
 * Layout belongs in CSS, and most of this card's does. What cannot go there is
 * the block of format buttons: narrow, it sits below the rule at the foot of
 * the card; wide, it sits under the cover, in a different parent. No media
 * query moves a node between parents, and rendering it twice would put two
 * copies of every button in the document — two tab stops, two announcements to
 * a screen reader, and two elements answering to the same name in a test.
 *
 * So one decision stays in JS, and this constant is what keeps it honest: the
 * query below and Tailwind's `sm` are the same number, asserted by a test, and
 * changing one without the other fails.
 */
export const CARD_WIDE_MIN_WIDTH_PX = 640;

/** The media query the card's layout decision is made with. */
export const CARD_WIDE_QUERY = `(min-width: ${CARD_WIDE_MIN_WIDTH_PX}px)`;
