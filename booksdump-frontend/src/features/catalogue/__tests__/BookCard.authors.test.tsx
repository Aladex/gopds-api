import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import BookCard from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';
import entityEmptyLine from './fixtures/entity-empty-line.json';

/*
 * The authors row reads the book's author line when the list sends one —
 * authors_display, the names in the file's order, each linking to the
 * catalogue author it is when there is one — and the catalogue authors when
 * it does not. A name without a catalogue author is shown as plain text: no
 * link to guess at.
 */

const translate = (key: string, fallback?: unknown) =>
    typeof fallback === 'string' ? fallback : key;
const translation = { t: translate, i18n: { language: 'ru' } };
const setAuthorName = vi.fn();
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName }) }));

const baseBook: Book = {
    id: 1,
    title: 'Сборник',
    authors: [
        { id: 7, full_name: 'Петров Иван' },
        { id: 8, full_name: 'Сидорова Анна' },
    ],
    series: [],
    genres: [],
    annotation: '',
    filename: 'collection',
    cover: false,
    registerdate: '',
    docdate: '',
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'lib/collection',
    format: 'fb2',
    favorite_count: 0,
};

const renderCard = (overrides: Partial<Book>) =>
    render(
        <MemoryRouter>
            <BookCard
                book={{ ...baseBook, ...overrides }}
                isWide
                showLanguage={false}
                isSuperuser={false}
                formatDate={(value) => value}
                isBookConverting={() => false}
                onDownload={vi.fn()}
                onPreview={vi.fn()}
                onEpubRequest={vi.fn()}
                onMobiRequest={vi.fn()}
                onToggleFavourite={vi.fn()}
                onToggleApproved={vi.fn()}
                onRescan={vi.fn()}
                onEdit={vi.fn()}
            />
        </MemoryRouter>,
    );

/** The authors row, by its label. */
const authorsRow = () => {
    const row = screen.getByText('authors').closest('div');
    expect(row).not.toBeNull();
    return within(row as HTMLElement);
};

const linkTargets = () =>
    authorsRow()
        .queryAllByRole('link')
        .map((link) => [link.textContent, link.getAttribute('href')]);

describe('the authors of a book', () => {
    beforeEach(() => setAuthorName.mockClear());

    it('are the catalogue authors, each a link, when the list sends no author line', () => {
        renderCard({});

        expect(linkTargets()).toEqual([
            ['Петров Иван', '/books/find/author/7/1'],
            ['Сидорова Анна', '/books/find/author/8/1'],
        ]);
    });

    it('are the author line in its own order when the list sends one', () => {
        renderCard({
            authors_display: [
                { name: 'Анна Сидорова', legacy_author_id: 8 },
                { name: 'Иван Петрович Петров', legacy_author_id: 7 },
            ],
        });

        expect(linkTargets()).toEqual([
            ['Анна Сидорова', '/books/find/author/8/1'],
            ['Иван Петрович Петров', '/books/find/author/7/1'],
        ]);
        expect(screen.queryByText('Петров Иван')).toBeNull();
    });

    it('show a name with no catalogue author as text, not as a link', () => {
        renderCard({
            authors_display: [{ name: 'Иван Петров', legacy_author_id: 7 }, { name: 'Мастер' }],
        });

        expect(linkTargets()).toEqual([['Иван Петров', '/books/find/author/7/1']]);
        const plain = authorsRow().getByText('Мастер');
        expect(plain.closest('a')).toBeNull();
        // The separator stands between every two names, linked or not.
        expect(authorsRow().getAllByText('·')).toHaveLength(1);
    });

    it('show a line of unlinked names only, with no link at all', () => {
        renderCard({ authors: [], authors_display: [{ name: 'Козьма Прутков' }] });

        expect(authorsRow().getByText('Козьма Прутков')).toBeInTheDocument();
        expect(linkTargets()).toEqual([]);
    });

    it('name the scope with the name the reader followed', () => {
        renderCard({ authors_display: [{ name: 'Иван Петрович Петров', legacy_author_id: 7 }] });

        fireEvent.click(authorsRow().getByRole('link'));
        expect(setAuthorName).toHaveBeenCalledWith('Иван Петрович Петров');
    });
});

/*
 * Under the entity identity a name links to the author entity its credit
 * names, by the entity's UUID, and the scope is named as the entity's page
 * names it — the name with its namesake label — so a reload says the same.
 * A name of several people opens a short menu of each of them.
 */
describe('the authors of a book under the entity identity', () => {
    const king = '01a168d0-a113-741a-b69d-53b91f3c7eb9';
    const arkady = '01a168d0-a10a-7c1f-9cd0-dc223f9a3e97';
    const boris = '01a168d0-a10a-7989-931f-2d034dcc8175';

    beforeEach(() => setAuthorName.mockClear());

    it('link a name to its entity and name the scope as the entity page does', () => {
        renderCard({
            authors: [],
            authors_display: [
                {
                    name: 'С. Кинг',
                    entities: [{ id: king, full_name: 'Стивен Кинг', label: 'Ужасы' }],
                },
                { name: 'Мастер' },
            ],
        });

        expect(linkTargets()).toEqual([['С. Кинг', `/books/find/author/${king}/1`]]);
        fireEvent.click(authorsRow().getByRole('link'));
        expect(setAuthorName).toHaveBeenCalledWith('Стивен Кинг · Ужасы');
        expect(authorsRow().getByText('Мастер').closest('a')).toBeNull();
    });

    it('name a namesake of the same genre by its books', () => {
        renderCard({
            authors: [],
            authors_display: [
                {
                    name: 'Стивен Кинг',
                    entities: [{ id: king, full_name: 'Стивен Кинг', label_books: 83 }],
                },
            ],
        });

        fireEvent.click(authorsRow().getByRole('link'));
        // The test translator answers with the key: the count goes through
        // the book-count phrase.
        expect(setAuthorName).toHaveBeenCalledWith('Стивен Кинг · bookCount');
    });

    it('open a menu of every person a name of several people names', async () => {
        renderCard({
            authors: [],
            authors_display: [
                {
                    name: 'Аркадий и Борис Стругацкие',
                    entities: [
                        { id: arkady, full_name: 'Аркадий Стругацкий' },
                        { id: boris, full_name: 'Борис Стругацкий' },
                    ],
                },
            ],
        });

        expect(linkTargets()).toEqual([]);
        await userEvent.click(
            authorsRow().getByRole('button', { name: 'Аркадий и Борис Стругацкие' }),
        );
        const items = await screen.findAllByRole('menuitem');
        expect(items.map((item) => [item.textContent, item.getAttribute('href')])).toEqual([
            ['Аркадий Стругацкий', `/books/find/author/${arkady}/1`],
            ['Борис Стругацкий', `/books/find/author/${boris}/1`],
        ]);
        await userEvent.click(items[1]);
        expect(setAuthorName).toHaveBeenCalledWith('Борис Стругацкий');
    });
});

/*
 * The API's own answer for a book whose line names no one under the entity
 * identity (the Go handler test pins the same file): the card shows no author
 * link at all — not the catalogue authors with their legacy links, which name
 * no author there.
 */
describe('a book whose entity line names no one', () => {
    it('links to no author', () => {
        renderCard(entityEmptyLine as unknown as Book);

        expect(screen.queryByText('authors')).toBeNull();
        expect(
            screen.queryAllByRole('link').map((link) => link.getAttribute('href')),
        ).not.toContain('/books/find/author/177615/1');
    });
});
