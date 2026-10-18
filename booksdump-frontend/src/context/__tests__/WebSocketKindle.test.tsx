import { act, render } from '@testing-library/react';
import { useEffect } from 'react';

import { getCurrentUser } from '@/api/auth';
import { BookConversionProvider } from '@/context/BookConversionContext';
import { WebSocketProvider, useWebSocket, type KindleReply } from '@/context/WebSocketContext';

// sendToKindle is a request over the shared socket and a promise for its
// reply. These drive a fake socket and check the frame that goes out and what
// the promise settles with — including when no reply can ever come.

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

// The provider's sendToKindle, handed out of the tree through a ref.
const sender: { current: (bookID: number) => Promise<KindleReply> } = {
    current: () => Promise.reject(new Error('unset')),
};
const send = (bookID: number) => sender.current(bookID);
const Grab = () => {
    const { sendToKindle } = useWebSocket();
    useEffect(() => {
        sender.current = sendToKindle;
    }, [sendToKindle]);
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

it('sends a kindle frame and resolves with the reply for that book', async () => {
    renderSocket();
    act(() => socket().open());

    const pending = send(42);
    expect(socket().sent.map((frame) => JSON.parse(frame))).toContainEqual({
        type: 'kindle',
        bookID: 42,
    });

    // A reply for another book answers nothing here.
    act(() => socket().receive({ type: 'kindle', bookID: 7, status: 'sent' }));
    act(() => socket().receive({ type: 'kindle', bookID: 42, status: 'error', code: 'too_large' }));
    await expect(pending).resolves.toEqual({ status: 'error', code: 'too_large' });
});

it('answers every request waiting on the same book with the one reply', async () => {
    renderSocket();
    act(() => socket().open());

    const first = send(42);
    const second = send(42);
    act(() => socket().receive({ type: 'kindle', bookID: 42, status: 'sent' }));

    await expect(first).resolves.toEqual({ status: 'sent', code: undefined });
    await expect(second).resolves.toEqual({ status: 'sent', code: undefined });
});

it('says the answer was lost when the connection goes before the reply', async () => {
    renderSocket();
    act(() => socket().open());

    const pending = send(42);
    act(() => socket().serverClose());

    await expect(pending).resolves.toEqual({ status: 'error', code: 'connection_lost' });
});

it('does not send while there is no open connection', async () => {
    renderSocket();

    await expect(send(42)).resolves.toEqual({ status: 'error', code: 'unavailable' });
    expect(socket().sent).toHaveLength(0);
});
