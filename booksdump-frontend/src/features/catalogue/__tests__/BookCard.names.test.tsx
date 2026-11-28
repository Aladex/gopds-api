import { act, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import type { AuthorDisplay } from '@/api/books';
import { makeBook, renderCard, translate } from './bookCardHarness';

/*
 * One name for a book's author wherever the card writes it (design/author-
 * display): a Russian book names its author by the entity's short name in the
 * byline and on the typographic cover; a book in another language by its own
 * spelling. When the card's name is not how the book writes it — more than a
 * dropped patronymic, ё/е, case or punctuation — the opened card says how the
 * book writes it, in one line.
 */

vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: translate, i18n: { language: 'ru' } }),
}));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName: vi.fn() }) }));

// The clamp lets go at once without motion, so the opened card is on screen
// without waiting for a transition.
beforeEach(() => {
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
        matches: query.includes('prefers-reduced-motion'),
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
    })) as unknown as typeof window.matchMedia;
});

const mikhailov: AuthorDisplay = {
    name: 'Владимир Дмитриевич Михайлов',
    short_name: 'Владимир Михайлов',
    entities: [
        {
            id: '01a168d0-0000-7000-8000-000000000001',
            full_name: 'Владимир Дмитриевич Михайлов',
            short_name: 'Владимир Михайлов',
        },
    ],
};
const pseudonym: AuthorDisplay = {
    name: 'Opal Andrews',
    entities: [{ id: '01a168d0-0000-7000-8000-000000000002', full_name: 'Алан Маршалл' }],
};

const byline = () => within(screen.getByTestId('book-byline'));
const tileAuthor = () =>
    screen
        .getByTestId('book-cover-tile')
        .querySelector('[data-tile-author]')
        ?.getAttribute('data-tile-author');
const more = () => screen.queryByRole('button', { name: 'bookMore' });
const inBook = () => screen.queryByTestId('book-in-book');

describe('the names on a card', () => {
    it('are the short names for a Russian book, in the byline and on the cover', () => {
        renderCard({ book: makeBook({ authors_display: [mikhailov] }) });

        expect(byline().getByRole('link')).toHaveTextContent(/^Владимир Михайлов$/);
        expect(tileAuthor()).toBe('Владимир Михайлов');
    });

    it('are the book’s spelling for a book in another language', () => {
        renderCard({ book: makeBook({ lang: 'uk', authors_display: [mikhailov] }) });

        expect(byline().getByRole('link')).toHaveTextContent(/^Владимир Дмитриевич Михайлов$/);
        expect(tileAuthor()).toBe('Владимир Дмитриевич Михайлов');
    });

    it('say nothing more when only the patronymic was dropped', () => {
        renderCard({ book: makeBook({ authors_display: [mikhailov] }) });

        // Nothing else is held back, so there is nothing to open.
        expect(more()).toBeNull();
        expect(inBook()).toBeNull();
    });

    it('show how the book writes a name the card writes otherwise, once opened', async () => {
        renderCard({ book: makeBook({ authors_display: [mikhailov, pseudonym] }) });

        expect(
            byline()
                .getAllByRole('link')
                .map((link) => link.textContent),
        ).toEqual(['Владимир Михайлов', 'Алан Маршалл']);
        // Shut, the line waits behind «Подробнее», which is offered for it.
        expect(inBook()).toBeNull();
        await userEvent.click(more()!);
        await act(async () => {});

        expect(inBook()).toHaveTextContent(/^bookInBookLabel: Opal Andrews$/);
    });

    it('show no such line for a book in another language', () => {
        renderCard({ book: makeBook({ lang: 'en', authors_display: [pseudonym] }) });

        expect(byline().getByRole('link')).toHaveTextContent(/^Opal Andrews$/);
        expect(more()).toBeNull();
    });
});
