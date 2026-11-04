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

/** The colophon line, or null when the card has none. */
const colophon = () => screen.queryByTestId('book-colophon');

/*
 * One muted metadata line, «Издательство: АСТ · ISBN 978-5-17-177750-0», always
 * visible on a wide card, labelled as the series line is labelled — a label
 * and its value, so it reads as what the book is, not as its annotation. A
 * phone hides it behind «Подробнее»; BookCard.redesign holds that.
 */
describe('the publisher and ISBN of a book', () => {
    it('are shown on one line, the label before the publisher', () => {
        renderCard({ publisher: 'Издательство «Мир»', isbn: ['978-5-03-000000-1'] });

        expect(colophon()).toHaveTextContent(
            /^bookPublisherLabel: Издательство «Мир» · bookIsbn 978-5-03-000000-1$/,
        );
    });

    it('label the line the way the series line is labelled', () => {
        renderCard({ publisher: 'Азбука', isbn: ['978-5-389-00000-1'] });

        // The series line's own pattern: label and value as plain text in
        // one muted line, no markup invented for this line.
        const line = colophon()!;
        expect(line).toHaveTextContent('bookPublisherLabel: Азбука');
        expect(line.querySelectorAll('span, em, strong')).toHaveLength(0);
    });

    it('typeset the line with the series metadata classes, not its own', () => {
        const { container } = renderCard({
            publisher: 'Азбука',
            series: [{ id: 1, ser: 'Мир Полудня', ser_no: 1 }],
            isbn: ['978-5-389-00000-1'],
        });

        // Both metadata lines read as one voice: same size, same leading, same
        // colour — the tokens the series line carries, not a colophon-specific
        // size. What the colophon adds on top is only what its position needs:
        // the wide card's margin and the shut cut.
        const series = [...container.querySelectorAll<HTMLElement>('div')].find((el) =>
            el.className.includes('text-[13px]'),
        );
        expect(series).not.toBeNull();
        const tokens = series!.className.split(/\s+/);
        const colophonTokens = colophon()!.className.split(/\s+/);
        for (const token of tokens) {
            if (token.startsWith('text-') || token.startsWith('leading-')) {
                expect(colophonTokens).toContain(token);
            }
        }
        expect(colophonTokens).toContain('text-muted-foreground');
    });

    it('leave the label out when the book has no publisher', () => {
        renderCard({ publisher: null, isbn: ['978-5-03-000000-1'] });

        expect(colophon()).toHaveTextContent(/^bookIsbn 978-5-03-000000-1$/);
        expect(colophon()?.textContent).not.toContain('bookPublisherLabel');
    });

    it('list several ISBNs separated by commas', () => {
        renderCard({
            publisher: null,
            isbn: ['978-5-03-000000-1', '5-03-000000-2', '978-0-00-000000-3'],
        });

        expect(colophon()).toHaveTextContent(
            'bookIsbn 978-5-03-000000-1, 5-03-000000-2, 978-0-00-000000-3',
        );
    });

    it('leave no trace when the book has neither', () => {
        renderCard({ publisher: null, isbn: [] });

        expect(colophon()).toBeNull();
    });

    it('leave no trace when the payload does not carry them at all', () => {
        renderCard({});

        expect(colophon()).toBeNull();
    });

    it('show one without the other', () => {
        renderCard({ publisher: null, isbn: ['978-5-03-000000-1'] });

        expect(colophon()).toHaveTextContent(/^bookIsbn 978-5-03-000000-1$/);
    });

    // An ISBN is read and copied by people, not aligned in a column.
    it('are set in the text face, not a monospace one', () => {
        renderCard({ publisher: 'Азбука', isbn: ['978-5-389-00000-1'] });

        expect(colophon()).not.toBeNull();
        expect(colophon()?.outerHTML).not.toMatch(/font-mono/);
    });

    // The ISBN keeps its label, and the publisher line gets one of its own,
    // in both languages.
    it('label the ISBN in both languages', () => {
        expect(enTranslation).toMatchObject({ bookIsbn: 'ISBN' });
        expect(ruTranslation).toMatchObject({ bookIsbn: 'ISBN' });
    });

    it('label the publisher in both languages, as the series line is', () => {
        expect(enTranslation).toMatchObject({ bookPublisherLabel: 'Publisher' });
        expect(ruTranslation).toMatchObject({ bookPublisherLabel: 'Издательство' });
    });
});
