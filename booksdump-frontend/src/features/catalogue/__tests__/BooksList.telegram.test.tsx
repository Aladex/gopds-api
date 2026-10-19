import React from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { toast } from 'sonner';

import BooksList from '@/features/catalogue/BooksList';
import * as booksApi from '@/api/books';
import type { Book } from '@/api/books';
import type { User } from '@/api/auth';
import type { TelegramReply } from '@/context/WebSocketContext';
import { getBotStatus, type BotStatus } from '@/api/telegram';

// "To Telegram" as the catalogue wires it, beside Kindle and in its image: the
// card, the page's one sender and its question, with the socket, the
// connection and the toaster replaced at their edges. A reader without a
// working connection is taken to the profile's Telegram tab and nothing is
// sent; a connected one is asked, the frame sent, and the outcome said.

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
vi.mock('@/api/telegram', () => ({ getBotStatus: vi.fn() }));

const logout = vi.fn();
const authState: { user: User; logout: typeof logout } = {
    user: {
        username: 'reader',
        first_name: '',
        last_name: '',
        is_superuser: false,
        books_lang: 'ru',
    },
    logout,
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

const sendToTelegram = vi.fn<(bookID: number) => Promise<TelegramReply>>();
vi.mock('@/context/WebSocketContext', () => {
    const value = {
        isConnected: true,
        subscribe: vi.fn(),
        sendToKindle: vi.fn(),
        sendToTelegram: (id: number) => sendToTelegram(id),
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

const connected: BotStatus = {
    state: 'connected',
    bot_username: 'reader_library_bot',
    telegram_name: 'Reader',
    default_format: 'epub',
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

/** The card's button once the connection has been read. */
const telegramButton = async (name: string) => screen.findByRole('button', { name });

/** Presses the card's button and says yes. */
const sendBook = async () => {
    await userEvent.click(await telegramButton('sendToTelegram.send'));
    const question = await screen.findByRole('dialog');
    await userEvent.click(within(question).getByRole('button', { name: 'sendToTelegram.confirm' }));
};

beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(booksApi.listBooks).mockResolvedValue({ books: [book], length: 1 });
    vi.mocked(getBotStatus).mockResolvedValue(connected);
    authState.user = {
        username: 'reader',
        first_name: '',
        last_name: '',
        is_superuser: false,
        books_lang: 'ru',
    };
});

describe('a reader without a working connection', () => {
    it.each(['none', 'pending_link', 'blocked', 'token_rejected'] as const)(
        'in state %s is taken to the Telegram tab, and nothing is sent',
        async (state) => {
            vi.mocked(getBotStatus).mockResolvedValue({ state });
            await renderList();

            await userEvent.click(await telegramButton('sendToTelegram.setup'));

            await waitFor(() => expect(openProfile).toHaveBeenCalledWith('telegram'));
            expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
            expect(sendToTelegram).not.toHaveBeenCalled();
        },
    );

    it('is asked once the bot was connected since the list opened', async () => {
        vi.mocked(getBotStatus)
            .mockResolvedValueOnce({ state: 'none' })
            .mockResolvedValue(connected);
        await renderList();

        await userEvent.click(await telegramButton('sendToTelegram.setup'));

        expect(await screen.findByRole('dialog')).toHaveAccessibleName(
            'sendToTelegram.confirmTitle',
        );
        expect(openProfile).not.toHaveBeenCalled();
    });
});

describe('a reader with a connected bot', () => {
    it('is asked first, with the book, the bot and the format', async () => {
        await renderList();

        await userEvent.click(await telegramButton('sendToTelegram.send'));

        const question = await screen.findByRole('dialog');
        expect(question).toHaveAccessibleName('sendToTelegram.confirmTitle');
        expect(question).toHaveTextContent(book.title);
        expect(question).toHaveTextContent('Аркадий Стругацкий');
        expect(within(question).getByTestId('telegram-confirm-bot')).toHaveTextContent(
            '@reader_library_bot',
        );
        expect(within(question).getByTestId('telegram-confirm-format')).toHaveTextContent('EPUB');
        expect(sendToTelegram).not.toHaveBeenCalled();
    });

    it('sends the book and says it was sent', async () => {
        sendToTelegram.mockResolvedValue({ status: 'sent' });
        await renderList();

        await sendBook();

        expect(sendToTelegram).toHaveBeenCalledWith(5);
        await waitFor(() => expect(toast.success).toHaveBeenCalledWith('sendToTelegram.sent'));
    });

    it('shows the spinner while the book is on its way, and sends it once', async () => {
        let answer: (reply: TelegramReply) => void = () => {};
        sendToTelegram.mockReturnValue(new Promise((resolve) => (answer = resolve)));
        await renderList();

        const button = await telegramButton('sendToTelegram.send');
        await sendBook();
        expect(await screen.findByRole('button', { name: 'sendToTelegram.sending' })).toBe(button);
        await userEvent.click(button);
        expect(sendToTelegram).toHaveBeenCalledTimes(1);

        answer({ status: 'sent' });
        expect(await screen.findByRole('button', { name: 'sendToTelegram.send' })).toBe(button);
    });

    it.each([
        ['not_found'],
        ['too_large'],
        ['conversion_failed'],
        ['send_failed'],
        ['uncertain'],
        ['busy'],
        ['unavailable'],
        ['connection_lost'],
    ])('names the %s error', async (code) => {
        sendToTelegram.mockResolvedValue({ status: 'error', code });
        await renderList();

        await sendBook();

        await waitFor(() =>
            expect(toast.error).toHaveBeenCalledWith(`sendToTelegram.error.${code}`),
        );
        expect(toast.success).not.toHaveBeenCalled();
        expect(openProfile).not.toHaveBeenCalled();
    });

    it('reads an unknown code as send_failed', async () => {
        sendToTelegram.mockResolvedValue({ status: 'error', code: 'something_new' });
        await renderList();

        await sendBook();

        await waitFor(() =>
            expect(toast.error).toHaveBeenCalledWith('sendToTelegram.error.send_failed'),
        );
    });

    it.each([['not_connected'], ['bot_blocked'], ['token_rejected']])(
        'names %s and shows the Telegram tab',
        async (code) => {
            sendToTelegram.mockResolvedValue({ status: 'error', code });
            await renderList();

            await sendBook();

            await waitFor(() =>
                expect(toast.error).toHaveBeenCalledWith(`sendToTelegram.error.${code}`),
            );
            expect(openProfile).toHaveBeenCalledWith('telegram');
        },
    );

    it('treats a book already on its way as news, not failure', async () => {
        sendToTelegram.mockResolvedValue({ status: 'error', code: 'in_progress' });
        await renderList();

        await sendBook();

        await waitFor(() =>
            expect(toast.info).toHaveBeenCalledWith('sendToTelegram.error.in_progress'),
        );
        expect(toast.error).not.toHaveBeenCalled();
    });

    it('signs out a reader whose session could not be renewed', async () => {
        sendToTelegram.mockResolvedValue({ status: 'error', code: 'session_expired' });
        await renderList();

        await sendBook();

        await waitFor(() => expect(logout).toHaveBeenCalledTimes(1));
        expect(toast.error).toHaveBeenCalledWith('sendToTelegram.error.session_expired');
    });
});

// As for Kindle: the focus a closing question gives back to the button is the
// script's, and the button's hint must not open on it.
describe('the Telegram hint after the question closes', () => {
    const closedWithFocusBack = async (button: HTMLElement) => {
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        await waitFor(() => expect(button).toHaveFocus());
        await new Promise((resolve) => setTimeout(resolve, 50));
        expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
    };

    it('stays shut after Cancel, Escape and Send', async () => {
        sendToTelegram.mockResolvedValue({ status: 'sent' });
        await renderList();
        const button = await telegramButton('sendToTelegram.send');

        await userEvent.click(button);
        await userEvent.click(
            within(await screen.findByRole('dialog')).getByRole('button', { name: 'cancel' }),
        );
        await closedWithFocusBack(button);

        await userEvent.click(button);
        await screen.findByRole('dialog');
        await userEvent.keyboard('{Escape}');
        await closedWithFocusBack(button);

        await userEvent.click(button);
        await screen.findByRole('dialog');
        await userEvent.keyboard('{Enter}');
        await closedWithFocusBack(button);
        await waitFor(() => expect(toast.success).toHaveBeenCalled());
    });

    it('opens for a Tab onto the button and for a hover', async () => {
        await renderList();
        const button = await telegramButton('sendToTelegram.send');

        while (document.activeElement !== button) await userEvent.tab();
        expect(await screen.findByRole('tooltip')).toHaveTextContent('sendToTelegram.send');

        await userEvent.click(document.body);
        await waitFor(() => expect(screen.queryByRole('tooltip')).not.toBeInTheDocument());
        await userEvent.hover(button);
        expect(await screen.findByRole('tooltip')).toHaveTextContent('sendToTelegram.send');
    });
});
