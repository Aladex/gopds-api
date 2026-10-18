import React from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { toast } from 'sonner';

import * as authApi from '@/api/auth';
import { AuthProvider, useAuth } from '@/context/AuthContext';
import { BookConversionProvider } from '@/context/BookConversionContext';
import { WebSocketProvider } from '@/context/WebSocketContext';
import PublicRoute from '@/app/PublicRoute';
import { useSendToKindle } from '@/features/kindle/useSendToKindle';

// The socket keeps the access token it was opened with, which lives fifteen
// minutes; past that the server answers a Kindle request session_expired and
// closes the socket. Everything real here but the network: the auth provider,
// the socket provider, the send hook and the public route guard. The reader
// must either get the book sent after the session is renewed — without an
// error on the way — or, when it cannot be renewed, actually land on the
// login form.

const translation = { t: (key: string) => key, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));
vi.mock('sonner', () => ({
    toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), info: vi.fn() }),
}));
// The auth provider's own start-up and logout calls are stubbed; the renewal
// itself — getCurrentUser — is the real one, through the real HTTP transport
// and its refresh-and-replay, with only fetch replaced below.
vi.mock('@/api/auth', async (importOriginal) => ({
    ...(await importOriginal<typeof import('@/api/auth')>()),
    getInit: vi.fn(),
    getCsrfToken: vi.fn(),
    logout: vi.fn(),
}));

/**
 * The server's side of the renewal, modelled on the production contract:
 * self-user answers 401 until the session has been refreshed; the first
 * refresh rotates the refresh token, and any later one presents the rotated
 * token and is refused, as the blacklist refuses it.
 */
type Network = { selfUser: 'expired' | 'down'; refresh: 'rotates' | 'revoked' };
let network: Network;
let refreshes = 0;
let refreshed = false;
const json = (status: number, body: unknown) =>
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const fakeFetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/api/refresh-token')) {
        refreshes += 1;
        if (network.refresh === 'rotates' && refreshes === 1) {
            refreshed = true;
            return json(200, { expires_in: 900 });
        }
        return json(401, { message: 'refresh_token_revoked' });
    }
    if (url.endsWith('/api/books/self-user')) {
        if (network.selfUser === 'down') return json(503, { message: 'unavailable' });
        return refreshed ? json(200, reader) : json(401, { message: 'invalid_session' });
    }
    return json(404, {});
});
const refreshPosts = () =>
    fakeFetch.mock.calls.filter(([input]) => String(input).endsWith('/api/refresh-token')).length;

class MockWebSocket {
    static instances: MockWebSocket[] = [];
    onopen: (() => void) | null = null;
    onmessage: ((event: { data: string }) => void) | null = null;
    onclose: (() => void) | null = null;
    onerror: ((event: unknown) => void) | null = null;
    sent: string[] = [];

    constructor(public url: string) {
        MockWebSocket.instances.push(this);
    }
    send(data: string) {
        this.sent.push(data);
    }
    close() {}
    open() {
        this.onopen?.();
    }
    receive(message: unknown) {
        this.onmessage?.({ data: JSON.stringify(message) });
    }
    serverClose() {
        this.onclose?.();
    }
    kindleFrames() {
        return this.sent
            .map((frame) => JSON.parse(frame))
            .filter((frame) => frame.type === 'kindle');
    }
}

const sockets = () => MockWebSocket.instances;
const lastSocket = () => sockets()[sockets().length - 1];

const reader = {
    username: 'reader',
    first_name: '',
    last_name: '',
    is_superuser: false,
    kindle_email: 'reader@kindle.com',
};

const Sender = () => {
    const kindle = useSendToKindle();
    return (
        <>
            <button type="button" onClick={() => kindle.send(5)}>
                send
            </button>
            <button type="button" onClick={() => kindle.send(6)}>
                send other
            </button>
        </>
    );
};

const Socket = ({ children }: React.PropsWithChildren) => {
    const { isAuthenticated } = useAuth();
    return <WebSocketProvider isAuthenticated={isAuthenticated}>{children}</WebSocketProvider>;
};

const renderApp = async () => {
    render(
        <MemoryRouter initialEntries={['/books']}>
            <AuthProvider>
                <BookConversionProvider>
                    <Socket>
                        <Routes>
                            <Route element={<PublicRoute />}>
                                <Route path="/login" element={<p>login-form</p>} />
                            </Route>
                            <Route path="/books" element={<Sender />} />
                        </Routes>
                    </Socket>
                </BookConversionProvider>
            </AuthProvider>
        </MemoryRouter>,
    );
    await screen.findByRole('button', { name: 'send' });
    await waitFor(() => expect(sockets().length).toBeGreaterThan(0));
    act(() => lastSocket().open());
};

// The first socket answers the send with session_expired and closes, as the
// server does.
const expireTheFirstSocket = async () => {
    await userEvent.click(screen.getByRole('button', { name: 'send' }));
    const first = lastSocket();
    expect(first.kindleFrames()).toEqual([{ type: 'kindle', bookID: 5 }]);
    act(() => {
        first.receive({ type: 'kindle', bookID: 5, status: 'error', code: 'session_expired' });
        first.serverClose();
    });
    return first;
};

beforeEach(() => {
    vi.clearAllMocks();
    MockWebSocket.instances = [];
    vi.stubGlobal('WebSocket', MockWebSocket);
    vi.stubGlobal('fetch', fakeFetch);
    network = { selfUser: 'expired', refresh: 'rotates' };
    refreshes = 0;
    refreshed = false;
    vi.mocked(authApi.getInit).mockResolvedValue({ csrf_token: 'csrf', user: reader });
    vi.mocked(authApi.getCsrfToken).mockResolvedValue({ csrf_token: 'csrf' });
    vi.mocked(authApi.logout).mockResolvedValue(undefined);
});

afterEach(() => {
    vi.unstubAllGlobals();
});

it('renews the session, reconnects and sends the book once more — with no error on the way', async () => {
    // The transport's refresh-and-replay renews the session.
    await renderApp();
    const first = await expireTheFirstSocket();

    await waitFor(() => expect(refreshPosts()).toBe(1));
    // The provider reconnects on its own backoff; the new socket carries the
    // renewed cookie and the book is asked for on it, once.
    await waitFor(() => expect(lastSocket()).not.toBe(first), { timeout: 4000 });
    const second = lastSocket();
    act(() => second.open());
    await waitFor(() => expect(second.kindleFrames()).toEqual([{ type: 'kindle', bookID: 5 }]));
    expect(first.kindleFrames()).toHaveLength(1);

    act(() => second.receive({ type: 'kindle', bookID: 5, status: 'sent' }));

    await waitFor(() => expect(toast.success).toHaveBeenCalledTimes(1));
    expect(toast.error).not.toHaveBeenCalled();
    expect(authApi.logout).not.toHaveBeenCalled();
});

it('signs the reader out and shows the login form when the session cannot be renewed', async () => {
    // The refresh itself is refused: the session really is gone.
    network.refresh = 'revoked';
    await renderApp();
    await expireTheFirstSocket();

    // Through the real logout and the real route guard: the login form
    // renders, rather than the guard bouncing a still-signed-in reader back.
    expect(await screen.findByText('login-form', {}, { timeout: 4000 })).toBeInTheDocument();
    expect(authApi.logout).toHaveBeenCalledTimes(1);
    expect(toast.error).toHaveBeenCalledWith('sendToKindle.error.session_expired');
    expect(screen.queryByRole('button', { name: 'send' })).not.toBeInTheDocument();
});

it('does not ask again on the expired socket when the renewal beats its close', async () => {
    await renderApp();
    await userEvent.click(screen.getByRole('button', { name: 'send' }));
    const first = lastSocket();

    // The reply arrives, the renewal finishes, and only then the close.
    await act(async () => {
        first.receive({ type: 'kindle', bookID: 5, status: 'error', code: 'session_expired' });
    });
    await waitFor(() => expect(refreshPosts()).toBe(1));
    await act(async () => {});
    expect(first.kindleFrames()).toHaveLength(1);

    act(() => first.serverClose());
    await waitFor(() => expect(lastSocket()).not.toBe(first), { timeout: 4000 });
    const second = lastSocket();
    act(() => second.open());
    await waitFor(() => expect(second.kindleFrames()).toEqual([{ type: 'kindle', bookID: 5 }]));
    act(() => second.receive({ type: 'kindle', bookID: 5, status: 'sent' }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledTimes(1));
    expect(toast.error).not.toHaveBeenCalled();
});

// A 503 from the renewal proves nothing about the session. The reader stays
// signed in and is told to try again; nothing logs them out.
it('keeps the session when the renewal meets a temporary server failure', async () => {
    network.selfUser = 'down';
    await renderApp();
    await expireTheFirstSocket();

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('sendToKindle.error.unavailable'));
    expect(authApi.logout).not.toHaveBeenCalled();
    expect(refreshPosts()).toBe(0);
    expect(screen.queryByText('login-form')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'send' })).toBeInTheDocument();
});

// Two sends queued on one socket expire together. One renewal serves both —
// a refresh each would rotate the same refresh token twice, and the second,
// refused, would log out the session the first had renewed — and each book is
// asked for exactly once more on the new socket.
it('renews two expired books through one refresh and sends both', async () => {
    await renderApp();
    await userEvent.click(screen.getByRole('button', { name: 'send' }));
    await userEvent.click(screen.getByRole('button', { name: 'send other' }));
    const first = lastSocket();
    expect(first.kindleFrames()).toHaveLength(2);
    act(() => {
        first.receive({ type: 'kindle', bookID: 5, status: 'error', code: 'session_expired' });
        first.receive({ type: 'kindle', bookID: 6, status: 'error', code: 'session_expired' });
        first.serverClose();
    });

    await waitFor(() => expect(lastSocket()).not.toBe(first), { timeout: 4000 });
    const second = lastSocket();
    act(() => second.open());
    await waitFor(() => expect(second.kindleFrames()).toHaveLength(2));
    expect(second.kindleFrames()).toEqual(
        expect.arrayContaining([
            { type: 'kindle', bookID: 5 },
            { type: 'kindle', bookID: 6 },
        ]),
    );
    expect(refreshPosts()).toBe(1);

    act(() => {
        second.receive({ type: 'kindle', bookID: 5, status: 'sent' });
        second.receive({ type: 'kindle', bookID: 6, status: 'sent' });
    });
    await waitFor(() => expect(toast.success).toHaveBeenCalledTimes(2));
    expect(toast.error).not.toHaveBeenCalled();
    expect(authApi.logout).not.toHaveBeenCalled();
    expect(screen.queryByText('login-form')).not.toBeInTheDocument();
});
