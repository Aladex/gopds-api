import { describe, expect, it } from 'vitest';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import BookCard from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';

const source = (relative: string) =>
    readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8');

const translate = (key: string, fallback?: string) => fallback ?? key;
vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: translate, i18n: { language: 'ru' } }),
}));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName: vi.fn() }) }));

// Dates are counted from now: a literal date drifts from the future into the
// past as the calendar moves, and a test that branches on that starts failing
// on a day nobody touched it.
const day = 24 * 60 * 60 * 1000;
const registeredOn = new Date(Date.now() - 30 * day).toISOString().slice(0, 10);
const publishedOn = new Date(Date.now() - 365 * day).toISOString().slice(0, 10);

const book: Book = {
    id: 1,
    title: 'Test Book',
    authors: [{ id: 1, full_name: 'Test Author' }],
    series: [],
    genres: [],
    annotation: 'Test annotation.',
    filename: 'test',
    cover: false,
    registerdate: registeredOn,
    docdate: publishedOn,
    lang: 'en',
    fav: false,
    approved: true,
    path: 'test',
    format: 'fb2',
    favorite_count: 0,
};

const renderCard = (isWide: boolean) =>
    render(
        <MemoryRouter>
            <BookCard
                book={book}
                isWide={isWide}
                showLanguage={false}
                isSuperuser={false}
                formatDate={(value) => value}
                isBookConverting={() => false}
                onDownload={vi.fn()}
                onEpubRequest={vi.fn()}
                onMobiRequest={vi.fn()}
                onToggleFavourite={vi.fn()}
                onToggleApproved={vi.fn()}
                onRescan={vi.fn()}
                onEdit={vi.fn()}
            />
        </MemoryRouter>,
    );

/**
 * The card used to carry two layout boundaries: the list asked useMediaQuery
 * for 600px while the card styled itself with `sm:` at 40rem. Between them the
 * two disagreed and the download block landed in a container built for the
 * other layout — a bug visible in one band of widths and nowhere else.
 *
 * What has to hold now is not "at width X the layout is Y": jsdom applies no
 * stylesheet, so no rendered test can observe a Tailwind breakpoint at all. A
 * test claiming to compare CSS against JS here would be comparing nothing.
 * What is observable, and what actually prevents the bug, is that exactly one
 * width question is asked anywhere in this pair of files.
 */
describe('the card has a single layout boundary', () => {
    const listSource = source('../BooksList.tsx');
    const cardSource = source('../BookCard.tsx');

    it('asks its one width question through the shared query', () => {
        expect(listSource).toContain('useMediaQuery(CARD_WIDE_QUERY)');
    });

    it('asks no other width question in either file', () => {
        // Any media query written by hand is a second boundary, whatever
        // number it carries — guarding the old 600px literal alone would let
        // 620px, or a second query at the same width, straight through.
        const inlineQueries = /\((?:min|max)-(?:width|height)\s*:/g;
        expect(listSource.replace('CARD_WIDE_QUERY', '')).not.toMatch(inlineQueries);
        expect(cardSource).not.toMatch(inlineQueries);

        // And the card must not take the decision itself: a query inside it
        // is a second boundary under another name.
        expect(cardSource).not.toContain('useMediaQuery');
        expect([...listSource.matchAll(/useMediaQuery\(/g)]).toHaveLength(1);
    });

    it('keeps the narrow card its longer annotation', () => {
        // Expandable turns the count into an inline height, so this is
        // observable without a stylesheet. A phone fits about a third of the
        // words per line that a desktop does; the same count leaves it with a
        // sentence fragment. An earlier refactor quietly dropped the wide
        // count onto both.
        // The card holds more than one collapsible; the annotation is the one
        // clamped to a count of line heights.
        const peekHeight = (container: HTMLElement) =>
            [...container.querySelectorAll<HTMLElement>('[data-state="collapsed"]')]
                .map((box) => box.style.maxHeight)
                .find((height) => height.endsWith('lh'));

        const narrow = renderCard(false);
        expect(peekHeight(narrow.container)).toBe('5lh');
        narrow.unmount();

        const wide = renderCard(true);
        expect(peekHeight(wide.container)).toBe('2lh');
    });
});
