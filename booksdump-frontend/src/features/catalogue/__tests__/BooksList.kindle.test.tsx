import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
// sent — once they have said yes to the question — and the outcome said.

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

/** Presses the card's button and says yes. */
const sendBook = async () => {
    await userEvent.click(screen.getByRole('button', { name: 'sendToKindle.send' }));
    const question = await screen.findByRole('dialog');
    await userEvent.click(within(question).getByRole('button', { name: 'sendToKindle.confirm' }));
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

        await sendBook();

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
        await sendBook();
        expect(await screen.findByRole('button', { name: 'sendToKindle.sending' })).toBe(button);
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        // A press while it is on its way neither asks again nor sends again.
        await userEvent.click(button);
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
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
        ['busy', 'sendToKindle.error.busy'],
        ['something_new', 'sendToKindle.error.send_failed'],
    ])('names the %s error', async (code, key) => {
        sendToKindle.mockResolvedValue({ status: 'error', code });
        await renderList();

        await sendBook();

        await waitFor(() => expect(toast.error).toHaveBeenCalledWith(key));
        expect(toast.success).not.toHaveBeenCalled();
    });

    it('signs out a reader whose session could not be renewed', async () => {
        sendToKindle.mockResolvedValue({ status: 'error', code: 'session_expired' });
        await renderList();

        await sendBook();

        await waitFor(() => expect(logout).toHaveBeenCalledTimes(1));
        expect(toast.error).toHaveBeenCalledWith('sendToKindle.error.session_expired');
    });

    it('treats a second request for a book already on its way as news, not failure', async () => {
        sendToKindle.mockResolvedValue({ status: 'error', code: 'in_progress' });
        await renderList();

        await sendBook();

        await waitFor(() =>
            expect(toast.info).toHaveBeenCalledWith('sendToKindle.error.in_progress'),
        );
        expect(toast.error).not.toHaveBeenCalled();
    });

    it('offers the setup again when the server finds no address after all', async () => {
        sendToKindle.mockResolvedValue({ status: 'error', code: 'not_configured' });
        await renderList();

        await sendBook();

        expect(await screen.findByRole('dialog')).toHaveTextContent('kindleSetup.title');
    });
});

describe('the question before sending', () => {
    beforeEach(() => {
        authState.user.kindle_email = 'reader@kindle.com';
        sendToKindle.mockResolvedValue({ status: 'sent' });
    });

    it('names the book, its author and the address, and sends nothing yet', async () => {
        await renderList();

        await userEvent.click(screen.getByRole('button', { name: 'sendToKindle.send' }));

        const question = await screen.findByRole('dialog');
        expect(question).toHaveAccessibleName('sendToKindle.confirmTitle');
        expect(question).toHaveTextContent(book.title);
        expect(question).toHaveTextContent('Аркадий Стругацкий');
        expect(within(question).getByTestId('kindle-confirm-address')).toHaveTextContent(
            'reader@kindle.com',
        );
        expect(
            within(question).getByRole('button', { name: 'sendToKindle.confirm' }),
        ).toBeVisible();
        expect(within(question).getByRole('button', { name: 'cancel' })).toBeVisible();
        expect(sendToKindle).not.toHaveBeenCalled();
    });

    it('sends nothing when cancelled, and gives the focus back to the card', async () => {
        await renderList();
        const button = screen.getByRole('button', { name: 'sendToKindle.send' });

        await userEvent.click(button);
        await userEvent.click(
            within(await screen.findByRole('dialog')).getByRole('button', { name: 'cancel' }),
        );

        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        expect(sendToKindle).not.toHaveBeenCalled();
        await waitFor(() => expect(button).toHaveFocus());
    });

    it('cancels on Escape', async () => {
        await renderList();
        const button = screen.getByRole('button', { name: 'sendToKindle.send' });

        await userEvent.click(button);
        await screen.findByRole('dialog');
        await userEvent.keyboard('{Escape}');

        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        expect(sendToKindle).not.toHaveBeenCalled();
        await waitFor(() => expect(button).toHaveFocus());
    });

    it('sends on Enter, Send having the focus, and gives the focus back', async () => {
        await renderList();
        const button = screen.getByRole('button', { name: 'sendToKindle.send' });

        await userEvent.click(button);
        const question = await screen.findByRole('dialog');
        await waitFor(() =>
            expect(
                within(question).getByRole('button', { name: 'sendToKindle.confirm' }),
            ).toHaveFocus(),
        );
        await userEvent.keyboard('{Enter}');

        await waitFor(() => expect(sendToKindle).toHaveBeenCalledWith(5));
        expect(sendToKindle).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        await waitFor(() => expect(button).toHaveFocus());
    });
});

describe('the reader’s switches', () => {
    it('hide each button from the card', async () => {
        authState.user.show_kindle_button = false;
        authState.user.show_telegram_button = false;
        await renderList();

        expect(screen.queryByRole('button', { name: /sendToKindle/ })).not.toBeInTheDocument();
        expect(screen.queryByRole('button', { name: /sendToTelegram/ })).not.toBeInTheDocument();
    });

    it('show both when the profile says nothing about them', async () => {
        await renderList();

        expect(screen.getByRole('button', { name: 'sendToKindle.setup' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'sendToTelegram.setup' })).toBeInTheDocument();
    });
});

// Closing the question gives the focus back to the card's Kindle button. That
// focus is the script's: the button's hint must not open on it and stay over
// the card — it opens for a Tab and for a hover only.
describe('the Kindle hint after the question closes', () => {
    beforeEach(() => {
        authState.user.kindle_email = 'reader@kindle.com';
        sendToKindle.mockResolvedValue({ status: 'sent' });
    });

    const ask = async () => {
        await renderList();
        const button = screen.getByRole('button', { name: 'sendToKindle.send' });
        await userEvent.click(button);
        return { button, question: await screen.findByRole('dialog') };
    };
    const closedWithFocusBack = async (button: HTMLElement) => {
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        await waitFor(() => expect(button).toHaveFocus());
        // Give a late open every chance to land.
        await new Promise((resolve) => setTimeout(resolve, 50));
        expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
    };

    it('stays shut after Cancel', async () => {
        const { button, question } = await ask();
        await userEvent.click(within(question).getByRole('button', { name: 'cancel' }));
        await closedWithFocusBack(button);
    });

    it('stays shut after Escape', async () => {
        const { button } = await ask();
        await userEvent.keyboard('{Escape}');
        await closedWithFocusBack(button);
    });

    it('stays shut after a press outside the question', async () => {
        const { button } = await ask();
        const overlay = document.querySelector('[data-slot="dialog-overlay"]')!;
        fireEvent.pointerDown(overlay);
        fireEvent.pointerUp(overlay);
        fireEvent.click(overlay);
        await closedWithFocusBack(button);
    });

    it('stays shut after Send, by click and by Enter', async () => {
        const first = await ask();
        await userEvent.click(
            within(first.question).getByRole('button', { name: 'sendToKindle.confirm' }),
        );
        await closedWithFocusBack(first.button);
        await waitFor(() => expect(toast.success).toHaveBeenCalled());

        await userEvent.click(first.button);
        await screen.findByRole('dialog');
        await userEvent.keyboard('{Enter}');
        await closedWithFocusBack(first.button);
    });

    it('opens for a Tab onto the button and for a hover', async () => {
        await renderList();
        const button = screen.getByRole('button', { name: 'sendToKindle.send' });

        while (document.activeElement !== button) await userEvent.tab();
        expect(await screen.findByRole('tooltip')).toHaveTextContent('sendToKindle.send');

        await userEvent.click(document.body);
        await waitFor(() => expect(screen.queryByRole('tooltip')).not.toBeInTheDocument());
        await userEvent.hover(button);
        expect(await screen.findByRole('tooltip')).toHaveTextContent('sendToKindle.send');
    });
});
