import React from 'react';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import BookCard, { type BookCardProps } from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';

// An administrator's three actions on a book — rescan, edit, approve — sit in
// one menu at the end of the title row, at both sizes. As three buttons of
// their own, with the send buttons beside them, the row ran out of a phone-width
// card; and among the buttons a reader presses every day, the menu was one
// stray tap away. It is never in the rail and never on the cover, where the
// favourite star sits.

const translate = (key: string, fallback?: string) => fallback ?? key;
vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: translate, i18n: { language: 'ru' } }),
}));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName: vi.fn() }) }));

const book: Book = {
    id: 3,
    title: 'Улитка на склоне',
    authors: [{ id: 1, full_name: 'Аркадий Стругацкий' }],
    series: [],
    genres: [],
    annotation: '',
    filename: 'snail',
    cover: false,
    registerdate: '',
    docdate: '',
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'lib/snail',
    format: 'fb2',
    favorite_count: 0,
};

const handlers = {
    onRescan: vi.fn(),
    onEdit: vi.fn(),
    onToggleApproved: vi.fn(),
};

const renderCard = (props: Partial<BookCardProps> = {}) =>
    render(
        <MemoryRouter>
            <BookCard
                book={book}
                isWide={false}
                showLanguage={false}
                isSuperuser
                formatDate={(value) => value}
                isBookConverting={() => false}
                onDownload={vi.fn()}
                onPreview={vi.fn()}
                onEpubRequest={vi.fn()}
                onMobiRequest={vi.fn()}
                onToggleFavourite={vi.fn()}
                showKindleButton
                showTelegramButton
                {...handlers}
                {...props}
            />
        </MemoryRouter>,
    );

const actions = () => screen.getByTestId('book-actions');
const menuButton = () => screen.getByRole('button', { name: /^bookAdminActions/ });

beforeEach(() => vi.clearAllMocks());

describe('where the menu is', () => {
    it.each([true, false])('ends the title row, out of the actions (wide %s)', (isWide) => {
        renderCard({ isWide });
        const title = screen.getByRole('heading', { name: book.title });

        expect(within(actions()).queryByRole('button', { name: /bookAdminActions/ })).toBeNull();
        expect(
            within(screen.getByTestId('book-cover')).queryByRole('button', {
                name: /bookAdminActions/,
            }),
        ).toBeNull();
        // The title and the menu share one row: the title takes the room, the
        // menu keeps its own width at the end of it.
        expect(title.parentElement).toContainElement(menuButton());
        expect(title.className).toMatch(/\bflex-1\b/);
    });

    it('comes after the title for a keyboard and a screen reader', () => {
        renderCard();
        const title = screen.getByRole('heading', { name: book.title });

        expect(
            title.compareDocumentPosition(menuButton()) & Node.DOCUMENT_POSITION_FOLLOWING,
        ).toBeTruthy();
    });

    it('is only on an administrator’s card', () => {
        const admin = renderCard();
        expect(menuButton()).toBeInTheDocument();
        admin.unmount();

        renderCard({ isSuperuser: false });
        expect(screen.queryByRole('button', { name: /bookAdminActions/ })).toBeNull();
    });

    it('leaves the action row the same for an administrator and a reader', () => {
        const reader = renderCard({ isSuperuser: false });
        const readerRow = within(actions())
            .getAllByRole('button')
            .map((b) => b.getAttribute('aria-label') ?? b.textContent);
        reader.unmount();

        renderCard();
        const adminRow = within(actions())
            .getAllByRole('button')
            .map((b) => b.getAttribute('aria-label') ?? b.textContent);

        expect(adminRow).toEqual(readerRow);
    });
});

describe('the admin menu', () => {
    it('rescans, edits and withdraws the approval', async () => {
        renderCard();

        await userEvent.click(menuButton());
        await userEvent.click(await screen.findByRole('menuitem', { name: 'bookMenuRescan' }));
        expect(handlers.onRescan).toHaveBeenCalledWith(3);

        await userEvent.click(menuButton());
        await userEvent.click(await screen.findByRole('menuitem', { name: 'bookMenuEdit' }));
        expect(handlers.onEdit).toHaveBeenCalledWith(book);

        await userEvent.click(menuButton());
        const approval = await screen.findByRole('menuitemcheckbox', { name: 'bookMenuUnapprove' });
        expect(approval).toBeChecked();
        await userEvent.click(approval);
        expect(handlers.onToggleApproved).toHaveBeenCalledWith(book);
    });

    it('offers the approval, unticked, for a book not yet approved', async () => {
        renderCard({ book: { ...book, approved: false } });

        await userEvent.click(menuButton());
        const approval = await screen.findByRole('menuitemcheckbox', { name: 'bookMenuApprove' });
        expect(approval).not.toBeChecked();
    });
});

// The green tick on its own button used to say a book was approved. Folded
// into the menu, a book still waiting for approval must be told apart without
// opening anything.
describe('a book not yet approved', () => {
    it('marks the menu button, and says so in its name', () => {
        renderCard({ book: { ...book, approved: false } });

        expect(menuButton()).toHaveAccessibleName('bookAdminActions: bookNotApproved');
        expect(within(menuButton()).getByTestId('not-approved-dot')).toBeInTheDocument();
    });

    it('is the only one marked', () => {
        renderCard();

        expect(menuButton()).toHaveAccessibleName('bookAdminActions');
        expect(within(menuButton()).queryByTestId('not-approved-dot')).toBeNull();
    });
});
