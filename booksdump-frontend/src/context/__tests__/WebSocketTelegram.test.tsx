import { act, render } from '@testing-library/react';
import { useEffect } from 'react';

import { getCurrentUser } from '@/api/auth';
import { BookConversionProvider } from '@/context/BookConversionContext';
import {
    WebSocketProvider,
    useWebSocket,
    type KindleReply,
    type TelegramReply,
} from '@/context/WebSocketContext';

// sendToTelegram is the Kindle request's twin over the same socket: its own
// frame, its own replies, the same waiting and the same session renewal.

vi.mock('@/api/auth', () => ({ getCurrentUser: vi.fn() }));

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
}

const socket = () => MockWebSocket.instances[MockWebSocket.instances.length - 1];
const frames = () => socket().sent.map((frame) => JSON.parse(frame));

const senders: {
    telegram: (bookID: number) => Promise<TelegramReply>;
    kindle: (bookID: number) => Promise<KindleReply>;
} = {
    telegram: () => Promise.reject(new Error('unset')),
    kindle: () => Promise.reject(new Error('unset')),
};
const Grab = () => {
    const { sendToTelegram, sendToKindle } = useWebSocket();
    useEffect(() => {
        senders.telegram = sendToTelegram;
        senders.kindle = sendToKindle;
    }, [sendToTelegram, sendToKindle]);
    return null;
};

const renderSocket = () =>
    render(
        <BookConversionProvider>
            <WebSocketProvider isAuthenticated>
                <Grab />
            </WebSocketProvider>
        </BookConversionProvider>,
    );

beforeEach(() => {
    MockWebSocket.instances = [];
    vi.stubGlobal('WebSocket', MockWebSocket);
    vi.mocked(getCurrentUser).mockResolvedValue({
        username: 'reader',
        first_name: '',
        last_name: '',
        is_superuser: false,
    });
});

afterEach(() => {
    vi.unstubAllGlobals();
});

it('sends a telegram frame and resolves with the telegram reply, not the kindle one', async () => {
    renderSocket();
    act(() => socket().open());

    const telegram = senders.telegram(42);
    const kindle = senders.kindle(42);
    expect(frames()).toContainEqual({ type: 'telegram', bookID: 42 });
    expect(frames()).toContainEqual({ type: 'kindle', bookID: 42 });

    act(() => socket().receive({ type: 'kindle', bookID: 42, status: 'sent' }));
    act(() =>
        socket().receive({ type: 'telegram', bookID: 42, status: 'error', code: 'bot_blocked' }),
    );
    await expect(telegram).resolves.toEqual({ status: 'error', code: 'bot_blocked' });
    await expect(kindle).resolves.toEqual({ status: 'sent', code: undefined });
});

it('asks once for a book already asked for', async () => {
    renderSocket();
    act(() => socket().open());

    const first = senders.telegram(42);
    const second = senders.telegram(42);
    expect(frames().filter((frame) => frame.type === 'telegram')).toEqual([
        { type: 'telegram', bookID: 42 },
    ]);

    act(() => socket().receive({ type: 'telegram', bookID: 42, status: 'sent' }));
    await expect(first).resolves.toEqual({ status: 'sent', code: undefined });
    await expect(second).resolves.toEqual({ status: 'sent', code: undefined });
});

it('says the answer was lost when the connection goes before the reply', async () => {
    renderSocket();
    act(() => socket().open());

    const pending = senders.telegram(42);
    act(() => socket().serverClose());

    await expect(pending).resolves.toEqual({ status: 'error', code: 'connection_lost' });
});

it('says there was nothing to ask over when the socket is not open', async () => {
    renderSocket();

    await expect(senders.telegram(42)).resolves.toEqual({ status: 'error', code: 'unavailable' });
});

it('renews an expired session and asks for the book again on the next socket', async () => {
    renderSocket();
    act(() => socket().open());
    const expired = socket();

    const pending = senders.telegram(42);
    act(() =>
        expired.receive({ type: 'telegram', bookID: 42, status: 'error', code: 'session_expired' }),
    );
    await act(async () => {
        await Promise.resolve();
    });
    act(() => expired.serverClose());

    // The close schedules a reconnect; the new socket asks again.
    await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 1_400));
    });
    const next = socket();
    expect(next).not.toBe(expired);
    act(() => next.open());
    expect(next.sent.map((frame) => JSON.parse(frame))).toContainEqual({
        type: 'telegram',
        bookID: 42,
    });
    act(() => next.receive({ type: 'telegram', bookID: 42, status: 'sent' }));
    await expect(pending).resolves.toEqual({ status: 'sent', code: undefined });
});
