import React from 'react';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

import BookCard from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';
import enTranslation from '@/locales/en/translation.json';
import ruTranslation from '@/locales/ru/translation.json';

/*
 * The publisher and ISBNs come from the book's current metadata snapshot. A
 * book without one carries null and [], and the card then says nothing about
 * either: no label over an empty value, no placeholder.
 */

const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName: vi.fn() }) }));

const baseBook: Book = {
    id: 1,
    title: 'Пикник на обочине',
    authors: [{ id: 1, full_name: 'Аркадий Стругацкий' }],
    series: [],
    genres: [],
    annotation: 'Зона.',
    filename: 'roadside',
    cover: false,
    registerdate: '',
    docdate: '',
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'lib/roadside',
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

/** The row a label heads, or null when the card has no such row. */
const rowLabelled = (label: string) => screen.queryByText(label)?.closest('div') ?? null;

describe('the publisher and ISBN of a book', () => {
    it('are shown, each under its own label, when the book has them', () => {
        renderCard({ publisher: 'Издательство «Мир»', isbn: ['978-5-03-000000-1'] });

        expect(rowLabelled('bookPublisher')).toHaveTextContent('Издательство «Мир»');
        expect(rowLabelled('bookIsbn')).toHaveTextContent('978-5-03-000000-1');
    });

    it('list several ISBNs separated by commas', () => {
        renderCard({ isbn: ['978-5-03-000000-1', '5-03-000000-2', '978-0-00-000000-3'] });

        expect(
            screen.getByText('978-5-03-000000-1, 5-03-000000-2, 978-0-00-000000-3'),
        ).toBeInTheDocument();
    });

    it('leave no trace when the book has neither', () => {
        renderCard({ publisher: null, isbn: [] });

        expect(rowLabelled('bookPublisher')).toBeNull();
        expect(rowLabelled('bookIsbn')).toBeNull();
    });

    it('leave no trace when the payload does not carry them at all', () => {
        renderCard({});

        expect(rowLabelled('bookPublisher')).toBeNull();
        expect(rowLabelled('bookIsbn')).toBeNull();
    });

    it('show one without the other', () => {
        renderCard({ publisher: null, isbn: ['978-5-03-000000-1'] });

        expect(rowLabelled('bookPublisher')).toBeNull();
        expect(rowLabelled('bookIsbn')).toHaveTextContent('978-5-03-000000-1');
    });

    // An ISBN is read and copied by people, not aligned in a column.
    it('are set in the text face, not a monospace one', () => {
        renderCard({ publisher: 'Азбука', isbn: ['978-5-389-00000-1'] });

        for (const label of ['bookPublisher', 'bookIsbn']) {
            const row = rowLabelled(label);
            expect(row).not.toBeNull();
            expect(row?.outerHTML).not.toMatch(/font-mono/);
        }
    });

    it('have labels in both languages', () => {
        expect(enTranslation).toMatchObject({ bookPublisher: 'Publisher', bookIsbn: 'ISBN' });
        expect(ruTranslation).toMatchObject({ bookPublisher: 'Издательство', bookIsbn: 'ISBN' });
    });
});
