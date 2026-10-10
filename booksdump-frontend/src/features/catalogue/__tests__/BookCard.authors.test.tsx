import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

import BookCard from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';

/*
 * The authors row reads the book's author line when the list sends one —
 * authors_display, the names in the file's order, each linking to the
 * catalogue author it is when there is one — and the catalogue authors when
 * it does not. A name without a catalogue author is shown as plain text: no
 * link to guess at.
 */

const translate = (key: string, fallback?: string) => fallback ?? key;
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
