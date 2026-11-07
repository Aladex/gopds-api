import type { AutocompleteSuggestion } from '@/api/autocomplete';

/** What the picker shows of each lane, out of the fifteen rows the server sends. */
const AUTHOR_ROWS = 3;
const TITLE_ROWS = 7;
/** Titles that stay above the authors when no author leads. */
const TITLES_AHEAD = 2;

/** An author leads only with this much behind them, and this much ahead of the next. */
const LEAD_MIN_BOOKS = 100;
const LEAD_MIN_RATIO = 3;

const bookCount = (row: AutocompleteSuggestion) => row.books_count ?? 0;
const byBooks = (a: AutocompleteSuggestion, b: AutocompleteSuggestion) =>
    bookCount(b) - bookCount(a);

/**
 * leadsWithAuthor says whether the words are plainly one author: `кинг` is
 * Стивен Кинг at 778 books against 90, and the reader wants him.
 *
 * All three have to hold. The count and the lead keep a namesake out, and the
 * last clause keeps a prefix out: `гарри` first led to Гарри Гаррисон, with 444
 * books against 112, when the reader means Поттер.
 */
function leadsWithAuthor(authors: AutocompleteSuggestion[], query: string): boolean {
    const [first, second] = authors;
    if (!first || bookCount(first) < LEAD_MIN_BOOKS) {
        return false;
    }
    if (bookCount(first) < LEAD_MIN_RATIO * (second ? bookCount(second) : 0)) {
        return false;
    }
    const words = query.trim().toLowerCase();
    const name = first.value.toLowerCase();
    return name === words || name.split(/[\s-]+/).pop() === words;
}

/**
 * orderSuggestions puts one field's two lanes in one list.
 *
 * The server sends the book lane and then the author lane. A list in that order
 * would bury every author under eight titles, and the reader who typed a name
 * has the answer they came for at the bottom. So an author goes first when the
 * words are plainly that author, and otherwise the two likeliest titles do, with
 * the authors right behind them where they can still be seen.
 */
export function orderSuggestions(
    rows: AutocompleteSuggestion[],
    query: string,
): AutocompleteSuggestion[] {
    const authors = rows.filter((row) => row.type === 'author').sort(byBooks);
    const titles = rows
        .filter((row) => row.type !== 'author')
        .sort(byBooks)
        .slice(0, TITLE_ROWS);
    const shown = authors.slice(0, AUTHOR_ROWS);

    return leadsWithAuthor(authors, query)
        ? [...shown, ...titles]
        : [...titles.slice(0, TITLES_AHEAD), ...shown, ...titles.slice(TITLES_AHEAD)];
}
