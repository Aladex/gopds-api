import type { AuthorDisplay, Book } from '@/api/books';

/** What the server sends of an author that may have a namesake label. */
export interface NamedAuthor {
    full_name: string;
    /**
     * The name as book lists write it, «Имя Фамилия» without a patronymic.
     * The server decides it; a legacy author or an older answer has none.
     */
    short_name?: string;
    /** The dominant genre of its books, when no namesake shares it. */
    label?: string;
    /** Otherwise the number of its books. */
    label_books?: number;
}

/** The translator's book count, as useTranslation's t provides it. */
type CountBooks = (key: 'bookCount', options: { count: number }) => string;

/**
 * namesakeLabel tells an author entity from another of the same short name,
 * under the entity identity: its dominant genre, or — when a namesake's books
 * are mostly of that genre too — how many books it has. Empty for anyone else.
 */
export const namesakeLabel = (author: NamedAuthor, t: CountBooks): string => {
    if (author.label) return author.label;
    return author.label_books ? t('bookCount', { count: author.label_books }) : '';
};

/** shortName is the name an entity goes by on the site: its short name, else its full one. */
export const shortName = (author: NamedAuthor): string => author.short_name || author.full_name;

/**
 * namesakeTitle is an entity's name and label as one line of text, for where
 * only text goes (a tooltip, a chip's accessible label): the short name and,
 * for a namesake, «· label» — what NamesakeName draws.
 */
export const namesakeTitle = (author: NamedAuthor, t: CountBooks): string => {
    const label = namesakeLabel(author, t);
    return label ? `${shortName(author)} · ${label}` : shortName(author);
};

/**
 * showsShortNames says whether a book's language writes its authors by the
 * entities' short names: Russian, after lower-casing and dropping a region,
 * or no language at all. Any other language keeps the book's own spelling.
 */
export const showsShortNames = (lang: string | undefined): boolean => {
    const base = (lang ?? '').trim().toLowerCase().split(/[-_]/)[0];
    return base === '' || base === 'ru';
};

/**
 * shownAuthorName is the name a book's author is shown by wherever the book
 * lists its authors — the byline and its menu, the cover, the send sheet: the
 * entity's short name for a Russian book (or one of no language) whose name
 * links to one entity; the book's own spelling otherwise, for a name of
 * several people and for a name that links to no one.
 */
export const shownAuthorName = (author: AuthorDisplay, lang: string | undefined): string => {
    const entity = author.entities?.length === 1 ? author.entities[0] : undefined;
    return entity && showsShortNames(lang) ? shortName(entity) : author.name;
};

/**
 * bookAuthors is a book's author line from whichever list the catalogue sent:
 * the author line, else the catalogue's own authors.
 */
export const bookAuthors = (book: Book): AuthorDisplay[] =>
    book.authors_display ??
    (book.authors ?? []).map((author) => ({ name: author.full_name, legacy_author_id: author.id }));

/** shownAuthorNames is a book's authors as it lists them anywhere (shownAuthorName). */
export const shownAuthorNames = (book: Book): string[] =>
    bookAuthors(book).map((author) => shownAuthorName(author, book.lang));

/** The words of a name, compared without case, with ё read as е and punctuation ignored. */
const nameWords = (name: string): Set<string> =>
    new Set(
        name
            .toLowerCase()
            .replaceAll('ё', 'е')
            .split(/[^\p{L}\p{N}\p{M}]+/u)
            .filter(Boolean),
    );

const sameWords = (a: Set<string>, b: Set<string>): boolean =>
    a.size === b.size && [...a].every((word) => b.has(word));

/**
 * bookSpelling is how the book itself writes an author, when the card shows
 * the author by another name: a pseudonym, a transliteration, another
 * spelling. Empty when the two differ only by a patronymic, by ё and е, by
 * case, punctuation or word order — the same name.
 */
export const bookSpelling = (author: AuthorDisplay, lang: string | undefined): string => {
    const shown = shownAuthorName(author, lang);
    if (shown === author.name) return '';
    const shownWords = nameWords(shown);
    // The patronymic is told by the server, never guessed here: the words the
    // entity's short name drops from its full name, and the short form of the
    // book's own spelling.
    const entity = author.entities?.[0];
    const dropped = entity ? nameWords(entity.full_name) : new Set<string>();
    for (const word of entity ? nameWords(shortName(entity)) : []) dropped.delete(word);
    const same = [author.name, author.short_name ?? ''].some((own) => {
        if (!own) return false;
        const words = nameWords(own);
        for (const word of dropped) words.delete(word);
        return sameWords(words, shownWords);
    });
    return same ? '' : author.name;
};
