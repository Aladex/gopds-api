/** What the server sends of an author that may have a namesake label. */
export interface NamedAuthor {
    full_name: string;
    /** The dominant genre of its books, when no namesake shares it. */
    label?: string;
    /** Otherwise the number of its books. */
    label_books?: number;
}

/** The translator's book count, as useTranslation's t provides it. */
type CountBooks = (key: 'bookCount', options: { count: number }) => string;

/**
 * namesakeLabel tells an author entity from another of the same name, under
 * the entity identity: its dominant genre, or — when a namesake's books are
 * mostly of that genre too — how many books it has. Empty for anyone else.
 */
export const namesakeLabel = (author: NamedAuthor, t: CountBooks): string => {
    if (author.label) return author.label;
    return author.label_books ? t('bookCount', { count: author.label_books }) : '';
};

/**
 * authorTitle is an author's name as its page names it: the name and, for an
 * entity with a namesake, its label — written the way the server writes it
 * for the bot and OPDS.
 */
export const authorTitle = (author: NamedAuthor, t: CountBooks): string => {
    const label = namesakeLabel(author, t);
    return label ? `${author.full_name} · ${label}` : author.full_name;
};
