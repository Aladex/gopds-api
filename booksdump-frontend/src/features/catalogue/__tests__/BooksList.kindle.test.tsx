import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { toast } from 'sonner';

import BooksList from '@/features/catalogue/BooksList';
import * as booksApi from '@/api/books';
import type { Book } from '@/api/books';
import type { User } from '@/api/auth';
import type { KindleReply } from '@/context/WebSocketContext';

// "To Kindle" as the catalogue wires it: the card, the page's one sender and
// its one setup dialog, with the socket and the toaster replaced at their
// edges. What is held here is the behaviour a reader sees — a reader with no
// Kindle gets the setup and nothing is sent; one with a Kindle gets the frame
// sent and the outcome said.

const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({
    useTranslation: () => translation,
    Trans: ({ i18nKey }: { i18nKey: string }) => i18nKey,
}));

vi.mock('sonner', () => ({
    toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), info: vi.fn() }),
}));

vi.mock('@/api/books', () => ({ listBooks: vi.fn(), toggleFavourite: vi.fn() }));
vi.mock('@/api/auth', () => ({ getCurrentUser: vi.fn() }));
vi.mock('@/api/admin', () => ({ updateBook: vi.fn() }));

const authState: { user: User } = {
    user: {
        username: 'reader',
        first_name: '',
        last_name: '',
        is_superuser: false,
        books_lang: 'ru',
    },
};
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));
vi.mock('@/context/FavContext', () => {
    const value = { fav: false, favEnabled: true, setFavEnabled: vi.fn() };
    return { useFav: () => value };
});
vi.mock('@/context/AuthorContext', () => {
    const value = { authorId: '', setAuthorId: vi.fn(), setAuthorName: vi.fn() };
    return { useAuthor: () => value };
});
vi.mock('@/context/SearchBarContext', () => {
    const value = {
        searchItem: '',
        setSearchItem: vi.fn(),
        selectedSearch: 'title',
        setSelectedSearch: vi.fn(),
        languages: ['ru'],
        selectedLanguage: 'ru',
        setSelectedLanguage: vi.fn(),
        scopeName: '',
        setScopeName: vi.fn(),
    };
    return { useSearchBar: () => value };
});
vi.mock('@/context/BookConversionContext', () => {
    const value = { state: { convertingBooks: [], conversionErrors: [] }, dispatch: vi.fn() };
    return { useBookConversion: () => value };
});
vi.mock('@/features/catalogue/ConversionBackdrop', () => ({ default: () => null }));

const sendToKindle = vi.fn<(bookID: number) => Promise<KindleReply>>();
vi.mock('@/context/WebSocketContext', () => {
    const value = {
        isConnected: true,
        subscribe: vi.fn(),
        sendToKindle: (id: number) => sendToKindle(id),
    };
    return { useWebSocket: () => value };
});

const openProfile = vi.fn();
vi.mock('@/features/profile/ProfileOpenerContext', () => ({
    useProfileOpener: () => ({ openProfile }),
}));

const book: Book = {
    id: 5,
    title: 'Трудно быть богом',
    authors: [{ id: 7, full_name: 'Аркадий Стругацкий' }],
    series: [],
    genres: [],
    annotation: '',
    filename: 'hard',
    cover: false,
    registerdate: '',
    docdate: '',
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'lib/hard',
    format: 'fb2',
    favorite_count: 0,
};

const renderList = async () => {
    render(
        <MemoryRouter initialEntries={['/books/page/1']}>
            <Routes>
                <Route path="/books/page/:page" element={<BooksList />} />
            </Routes>
        </MemoryRouter>,
    );
    await screen.findByText(book.title);
};

beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(booksApi.listBooks).mockResolvedValue({ books: [book], length: 1 });
    authState.user = {
        username: 'reader',
        first_name: '',
        last_name: '',
        is_superuser: false,
        books_lang: 'ru',
    };
});

describe('a reader with no Kindle address', () => {
    it('gets the setup dialog, and nothing is sent', async () => {
        await renderList();

        await userEvent.click(screen.getByRole('button', { name: 'sendToKindle.setup' }));

        expect(await screen.findByRole('dialog')).toHaveTextContent('kindleSetup.title');
        expect(sendToKindle).not.toHaveBeenCalled();
    });

    it('is taken to the Kindle settings from the dialog', async () => {
        await renderList();
        await userEvent.click(screen.getByRole('button', { name: 'sendToKindle.setup' }));

        await userEvent.click(
            await screen.findByRole('button', { name: 'kindleSetup.openSettings' }),
        );

        expect(openProfile).toHaveBeenCalledWith('kindle');
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    });
});

describe('a reader with a Kindle address', () => {
    beforeEach(() => {
        authState.user.kindle_email = 'reader@kindle.com';
    });

    it('sends the book and says it was sent — not delivered', async () => {
        sendToKindle.mockResolvedValue({ status: 'sent' });
        await renderList();

        await userEvent.click(screen.getByRole('button', { name: 'sendToKindle.send' }));

        expect(sendToKindle).toHaveBeenCalledWith(5);
        await waitFor(() =>
            expect(toast.success).toHaveBeenCalledWith('sendToKindle.sent', {
                description: 'sendToKindle.sentHint',
                duration: 8000,
            }),
        );
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('shows the spinner while the book is on its way, and sends it once', async () => {
        let answer: (reply: KindleReply) => void = () => {};
        sendToKindle.mockReturnValue(new Promise((resolve) => (answer = resolve)));
        await renderList();

        const button = screen.getByRole('button', { name: 'sendToKindle.send' });
        await userEvent.click(button);
        expect(await screen.findByRole('button', { name: 'sendToKindle.sending' })).toBe(button);
        await userEvent.click(button);
        expect(sendToKindle).toHaveBeenCalledTimes(1);

        answer({ status: 'sent' });
        expect(await screen.findByRole('button', { name: 'sendToKindle.send' })).toBe(button);
    });

    it.each([
        ['too_large', 'sendToKindle.error.too_large'],
        ['not_found', 'sendToKindle.error.not_found'],
        ['conversion_failed', 'sendToKindle.error.conversion_failed'],
        ['send_failed', 'sendToKindle.error.send_failed'],
        ['connection_lost', 'sendToKindle.error.connection_lost'],
        ['something_new', 'sendToKindle.error.send_failed'],
    ])('names the %s error', async (code, key) => {
        sendToKindle.mockResolvedValue({ status: 'error', code });
        await renderList();

        await userEvent.click(screen.getByRole('button', { name: 'sendToKindle.send' }));

        await waitFor(() => expect(toast.error).toHaveBeenCalledWith(key));
        expect(toast.success).not.toHaveBeenCalled();
    });

    it('treats a second request for a book already on its way as news, not failure', async () => {
        sendToKindle.mockResolvedValue({ status: 'error', code: 'in_progress' });
        await renderList();

        await userEvent.click(screen.getByRole('button', { name: 'sendToKindle.send' }));

        await waitFor(() =>
            expect(toast.info).toHaveBeenCalledWith('sendToKindle.error.in_progress'),
        );
        expect(toast.error).not.toHaveBeenCalled();
    });

    it('offers the setup again when the server finds no address after all', async () => {
        sendToKindle.mockResolvedValue({ status: 'error', code: 'not_configured' });
        await renderList();

        await userEvent.click(screen.getByRole('button', { name: 'sendToKindle.send' }));

        expect(await screen.findByRole('dialog')).toHaveTextContent('kindleSetup.title');
    });
});

describe('the reader’s switches', () => {
    it('hide each button from the card', async () => {
        authState.user.show_kindle_button = false;
        authState.user.show_telegram_button = false;
        await renderList();

        expect(screen.queryByRole('button', { name: /sendToKindle/ })).not.toBeInTheDocument();
        expect(
            screen.queryByRole('button', { name: 'sendToTelegram.soon' }),
        ).not.toBeInTheDocument();
    });

    it('show both when the profile says nothing about them', async () => {
        await renderList();

        expect(screen.getByRole('button', { name: 'sendToKindle.setup' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'sendToTelegram.soon' })).toBeInTheDocument();
    });
});
