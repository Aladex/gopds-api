/**
 * How an author's name relates to the query that found them.
 *
 * Client-side, so it works on any answer of the search, with or without the
 * author summary.
 */

/** A range of a name, as string offsets. */
export type NameRange = [start: number, end: number];

/** A name cut around the part of it the query is. */
export interface NameParts {
    before: string;
    hit: string;
    after: string;
}

/** Below this length a query has no stem to speak of. */
const STEM_MIN_LENGTH = 6;
const STEM_ENDING = 2;
/** The letters of a query a word has to start with to explain a hit. */
const WORD_START = 3;

const TRANSLIT: Record<string, string> = {
    а: 'a',
    б: 'b',
    в: 'v',
    г: 'g',
    д: 'd',
    е: 'e',
    ж: 'zh',
    з: 'z',
    и: 'i',
    й: 'y',
    к: 'k',
    л: 'l',
    м: 'm',
    н: 'n',
    о: 'o',
    п: 'p',
    р: 'r',
    с: 's',
    т: 't',
    у: 'u',
    ф: 'f',
    х: 'h',
    ц: 'ts',
    ч: 'ch',
    ш: 'sh',
    щ: 'sch',
    ъ: '',
    ы: 'y',
    ь: '',
    э: 'e',
    ю: 'yu',
    я: 'ya',
};

const normalize = (text: string) => text.toLowerCase().replaceAll('ё', 'е');

const toLatin = (text: string) => [...text].map((letter) => TRANSLIT[letter] ?? letter).join('');

/** Where the query sits in the name, or null when it does not. */
export const findMatch = (name: string, query: string): NameRange | null => {
    const haystack = normalize(name);
    const needle = normalize(query.trim());
    // A letter that lower-cases to two would shift every offset after it.
    if (!needle || haystack.length !== name.length) {
        return null;
    }

    const exact = haystack.indexOf(needle);
    if (exact >= 0) {
        return [exact, exact + needle.length];
    }

    const latin = toLatin(needle);
    const transliterated = haystack.indexOf(latin);
    if (transliterated >= 0) {
        return [transliterated, transliterated + latin.length];
    }

    // An inflected name: стругацкий finds Стругацкие by its stem, and the
    // whole word is what the query stands for.
    if (needle.length >= STEM_MIN_LENGTH) {
        const stem = needle.slice(0, -STEM_ENDING);
        const start = haystack.indexOf(stem);
        if (start >= 0) {
            let end = start + stem.length;
            while (end < haystack.length && /\p{L}/u.test(haystack[end])) {
                end += 1;
            }
            return [start, end];
        }
    }
    return null;
};

/**
 * nameParts cuts a name around the query, which every row of the page shares
 * and which therefore tells them apart the least. A name the query covers
 * whole is not cut: there is nothing else in it to read the row by.
 */
export const nameParts = (name: string, query: string): NameParts | null => {
    const match = findMatch(name, query);
    if (!match || match[1] - match[0] >= name.length) {
        return null;
    }
    const [start, end] = match;
    return { before: name.slice(0, start), hit: name.slice(start, end), after: name.slice(end) };
};

/**
 * isUnexplained says a hit shows no trace of the query: it is not in the name
 * or in its Latin spelling, and no word starts the way the query does.
 */
export const isUnexplained = (name: string, query: string): boolean => {
    if (findMatch(name, query)) {
        return false;
    }
    const start = normalize(query.trim()).slice(0, WORD_START);
    return !normalize(name)
        .split(/[\s.-]+/)
        .some((word) => word.startsWith(start));
};

/**
 * authorInitial is the first letter of the surname, the last word of the name
 * that is more than an initial.
 */
export const authorInitial = (name: string): string => {
    const words = name
        .split(/\s+/)
        .map((word) => word.match(/\p{L}/gu) ?? [])
        .filter((letters) => letters.length > 0);
    const surname = words.findLast((letters) => letters.length > 1) ?? words.at(-1);
    return surname ? surname[0].toLocaleUpperCase() : '';
};
