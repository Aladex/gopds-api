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
vi.mock('@/api/auth', () => ({
    getInit: vi.fn(),
    getCurrentUser: vi.fn(),
    getCsrfToken: vi.fn(),
    logout: vi.fn(),
    refreshSession: vi.fn(),
}));

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
        <button type="button" onClick={() => kindle.send(5)}>
            send
        </button>
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
    vi.mocked(authApi.getInit).mockResolvedValue({ csrf_token: 'csrf', user: reader });
    vi.mocked(authApi.getCsrfToken).mockResolvedValue({ csrf_token: 'csrf' });
    vi.mocked(authApi.logout).mockResolvedValue(undefined);
});

afterEach(() => {
    vi.unstubAllGlobals();
});

it('renews the session, reconnects and sends the book once more — with no error on the way', async () => {
    // The transport's refresh-and-replay renews the session.
    vi.mocked(authApi.getCurrentUser).mockResolvedValue(reader);
    await renderApp();
    const first = await expireTheFirstSocket();

    expect(authApi.getCurrentUser).toHaveBeenCalled();
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
    vi.mocked(authApi.getCurrentUser).mockRejectedValue(new Error('401'));
    await renderApp();
    await expireTheFirstSocket();

    // Through the real logout and the real route guard: the login form
    // renders, rather than the guard bouncing a still-signed-in reader back.
    expect(await screen.findByText('login-form', {}, { timeout: 4000 })).toBeInTheDocument();
    expect(authApi.logout).toHaveBeenCalledTimes(1);
    expect(toast.error).toHaveBeenCalledWith('sendToKindle.error.session_expired');
    expect(screen.queryByRole('button', { name: 'send' })).not.toBeInTheDocument();
});
