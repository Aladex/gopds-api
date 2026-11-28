import type { AuthorDisplay, AuthorEntityRef, Book } from '@/api/books';
import {
    bookAuthors,
    bookSpelling,
    namesakeTitle,
    shortName,
    showsShortNames,
    shownAuthorName,
    shownAuthorNames,
} from '@/shared/lib/authorTitle';

// One rule for the name a book's author is shown by (design/author-display):
// the entity decides who, the book's language decides the spelling. The
// frontend never parses a name: the short one comes from the server.

const mikhailov: AuthorEntityRef = {
    id: '01a168d0-0000-7000-8000-000000000001',
    full_name: 'Владимир Дмитриевич Михайлов',
    short_name: 'Владимир Михайлов',
};
const christie: AuthorEntityRef = {
    id: '01a168d0-0000-7000-8000-000000000002',
    full_name: 'Агата Кристи',
    short_name: 'Агата Кристи',
};

const linked = (
    name: string,
    entity: AuthorEntityRef,
    extra: Partial<AuthorDisplay> = {},
): AuthorDisplay => ({
    name,
    entities: [entity],
    ...extra,
});

describe('showsShortNames', () => {
    it.each([
        ['ru', true],
        ['RU', true],
        ['ru-RU', true],
        ['ru_ru', true],
        [' ru ', true],
        ['', true],
        [undefined, true],
        ['uk', false],
        ['en', false],
        ['be', false],
        ['rus', false],
    ])('%j → %s', (lang, want) => {
        expect(showsShortNames(lang)).toBe(want);
    });
});

describe('shownAuthorName', () => {
    const name = linked('Владимир Дмитриевич Михайлов', mikhailov, {
        short_name: 'Владимир Михайлов',
    });

    it('writes a Russian book’s author by the entity’s short name', () => {
        expect(shownAuthorName(name, 'ru')).toBe('Владимир Михайлов');
        expect(shownAuthorName(name, 'ru-RU')).toBe('Владимир Михайлов');
    });

    it('treats a book of no language as Russian', () => {
        expect(shownAuthorName(name, '')).toBe('Владимир Михайлов');
        expect(shownAuthorName(name, undefined)).toBe('Владимир Михайлов');
    });

    it('keeps the book’s own spelling in any other language', () => {
        expect(shownAuthorName(linked('Agatha Christie', christie), 'en')).toBe('Agatha Christie');
        expect(
            shownAuthorName(
                linked('Павло Загребельний', {
                    id: 'x',
                    full_name: 'Павло Архипович Загребельний',
                    short_name: 'Павло Загребельний',
                }),
                'uk',
            ),
        ).toBe('Павло Загребельний');
        expect(shownAuthorName(name, 'uk')).toBe('Владимир Дмитриевич Михайлов');
    });

    it('keeps the book’s spelling for a name of several people', () => {
        const brothers: AuthorDisplay = {
            name: 'Аркадий и Борис Стругацкие',
            entities: [
                {
                    id: 'a',
                    full_name: 'Аркадий Натанович Стругацкий',
                    short_name: 'Аркадий Стругацкий',
                },
                {
                    id: 'b',
                    full_name: 'Борис Натанович Стругацкий',
                    short_name: 'Борис Стругацкий',
                },
            ],
        };
        expect(shownAuthorName(brothers, 'ru')).toBe('Аркадий и Борис Стругацкие');
    });

    it('keeps the book’s spelling for a name of no entity', () => {
        expect(shownAuthorName({ name: 'Мастер' }, 'ru')).toBe('Мастер');
        expect(shownAuthorName({ name: 'Иван Петров', legacy_author_id: 7 }, 'ru')).toBe(
            'Иван Петров',
        );
    });

    it('falls back to the full name when the server sends no short one', () => {
        expect(
            shownAuthorName(linked('С. Кинг', { id: 'k', full_name: 'Стивен Кинг' }), 'ru'),
        ).toBe('Стивен Кинг');
    });
});

describe('bookSpelling, the «В книге» line', () => {
    it('is empty when the book writes the shown name', () => {
        expect(bookSpelling(linked('Агата Кристи', christie), 'ru')).toBe('');
        expect(bookSpelling({ name: 'Мастер' }, 'ru')).toBe('');
    });

    it('is empty when only the patronymic differs, either way round', () => {
        // The book writes it, the entity's short name drops it.
        expect(
            bookSpelling(
                linked('Владимир Дмитриевич Михайлов', mikhailov, {
                    short_name: 'Владимир Михайлов',
                }),
                'ru',
            ),
        ).toBe('');
        // The book writes it, the entity has none at all.
        expect(
            bookSpelling(
                linked(
                    'Мария Васильевна Семенова',
                    { id: 's', full_name: 'Мария Семенова', short_name: 'Мария Семенова' },
                    { short_name: 'Мария Семенова' },
                ),
                'ru',
            ),
        ).toBe('');
        // The book leaves it out, the entity has it.
        expect(bookSpelling(linked('Владимир Михайлов', mikhailov), 'ru')).toBe('');
        // The book writes it family name first, which the server does not shorten.
        expect(bookSpelling(linked('Михайлов Владимир Дмитриевич', mikhailov), 'ru')).toBe('');
    });

    it('is empty when only ё/е, case or punctuation differ', () => {
        const fyodor = { id: 'f', full_name: 'Фёдор Достоевский', short_name: 'Фёдор Достоевский' };
        expect(bookSpelling(linked('Федор Достоевский', fyodor), 'ru')).toBe('');
        expect(bookSpelling(linked('ФЁДОР ДОСТОЕВСКИЙ', fyodor), 'ru')).toBe('');
        expect(bookSpelling(linked('Фёдор  Достоевский.', fyodor), 'ru')).toBe('');
    });

    it('is the book’s spelling for a pseudonym, a transliteration or Latin letters', () => {
        const marshall = { id: 'm', full_name: 'Алан Маршалл', short_name: 'Алан Маршалл' };
        expect(bookSpelling(linked('Opal Andrews', marshall), 'ru')).toBe('Opal Andrews');
        const clancy = { id: 'c', full_name: 'Том Клэнси', short_name: 'Том Клэнси' };
        expect(bookSpelling(linked('Том Кланси', clancy), 'ru')).toBe('Том Кланси');
        expect(bookSpelling(linked('В. Михайлов', mikhailov), 'ru')).toBe('В. Михайлов');
    });

    it('is empty where the book’s own spelling is shown', () => {
        expect(bookSpelling(linked('Agatha Christie', christie), 'en')).toBe('');
        expect(
            bookSpelling(
                {
                    name: 'Аркадий и Борис Стругацкие',
                    entities: [
                        { id: 'a', full_name: 'Аркадий Стругацкий' },
                        { id: 'b', full_name: 'Борис Стругацкий' },
                    ],
                },
                'ru',
            ),
        ).toBe('');
    });
});

describe('a book’s authors', () => {
    const book = {
        lang: 'ru',
        authors: [{ id: 7, full_name: 'Петров Иван' }],
    } as Book;

    it('are the author line when the list sends one, the catalogue authors otherwise', () => {
        expect(bookAuthors(book)).toEqual([{ name: 'Петров Иван', legacy_author_id: 7 }]);
        expect(bookAuthors({ ...book, authors_display: [{ name: 'Мастер' }] })).toEqual([
            { name: 'Мастер' },
        ]);
    });

    it('are named by the book’s language', () => {
        const line = [linked('Владимир Дмитриевич Михайлов', mikhailov), { name: 'Мастер' }];
        expect(shownAuthorNames({ ...book, authors_display: line })).toEqual([
            'Владимир Михайлов',
            'Мастер',
        ]);
        expect(shownAuthorNames({ ...book, lang: 'en', authors_display: line })).toEqual([
            'Владимир Дмитриевич Михайлов',
            'Мастер',
        ]);
    });
});

describe('an entity’s name and label', () => {
    const t = (_key: 'bookCount', options: { count: number }) => `${options.count} книг`;

    it('is the short name, then the label', () => {
        expect(shortName(mikhailov)).toBe('Владимир Михайлов');
        expect(namesakeTitle({ ...mikhailov, label: 'Фантастика' }, t)).toBe(
            'Владимир Михайлов · Фантастика',
        );
        expect(namesakeTitle({ ...mikhailov, label_books: 12 }, t)).toBe(
            'Владимир Михайлов · 12 книг',
        );
        expect(namesakeTitle(mikhailov, t)).toBe('Владимир Михайлов');
        expect(namesakeTitle({ full_name: 'Стивен Кинг' }, t)).toBe('Стивен Кинг');
    });
});
