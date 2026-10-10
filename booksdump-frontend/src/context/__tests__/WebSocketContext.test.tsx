import { act, render } from '@testing-library/react';
import { ReactNode, useEffect } from 'react';

import { getCurrentUser } from '@/api/auth';
import { ApiError } from '@/api/errors';
import { BookConversionProvider, useBookConversion } from '@/context/BookConversionContext';
import { WebSocketProvider, useWebSocket, WSMessage } from '@/context/WebSocketContext';

// After a refused handshake the provider asks the read-only self-user
// endpoint (through the refresh-and-replay transport) whether the session is
// worth retrying for; tests steer that probe through this mock.
vi.mock('@/api/auth', () => ({
    getCurrentUser: vi.fn(),
}));
const selfUserProbe = vi.mocked(getCurrentUser);

// Test suites that do not import the auth module directly still resolve the
// mock above, so a bare vi.fn() count would mix this suite's calls with any
// component that genuinely fetches the current user. These tests count only
// calls made while they run.

// One mock socket class per test run: every `new WebSocket(...)` lands in
// `instances`, so a test can count connections and drive open/message/close.
class MockWebSocket {
    static instances: MockWebSocket[] = [];

    onopen: (() => void) | null = null;
    onmessage: ((event: { data: string }) => void) | null = null;
    onclose: (() => void) | null = null;
    onerror: ((event: unknown) => void) | null = null;
    sent: string[] = [];
    closed = false;

    constructor(public url: string) {
        MockWebSocket.instances.push(this);
    }

    send(data: string) {
        this.sent.push(data);
    }

    close() {
        this.closed = true;
    }

    // Test drivers, standing in for the network.
    open() {
        this.onopen?.();
    }

    receive(message: unknown) {
        this.onmessage?.({ data: JSON.stringify(message) });
    }

    serverClose() {
        this.onclose?.();
    }

    sentObjects(): Record<string, unknown>[] {
        return this.sent.map((frame) => JSON.parse(frame));
    }
}

const lastSocket = () => MockWebSocket.instances[MockWebSocket.instances.length - 1];

const Subscriber = ({
    topic,
    handler,
}: {
    topic: string;
    handler: (message: WSMessage) => void;
}) => {
    const { subscribe } = useWebSocket();
    useEffect(() => subscribe(topic, handler), [subscribe, topic, handler]);
    return null;
};

const renderSocket = (isAuthenticated: boolean, children?: ReactNode) =>
    render(
        <BookConversionProvider>
            <WebSocketProvider isAuthenticated={isAuthenticated}>{children}</WebSocketProvider>
        </BookConversionProvider>,
    );

// A user payload for a probe that finds the session alive.
const liveUser = {
    username: 'reader',
    first_name: '',
    last_name: '',
    is_superuser: false,
};

beforeEach(() => {
    MockWebSocket.instances = [];
    vi.stubGlobal('WebSocket', MockWebSocket);
    // The session is alive unless a test says otherwise.
    selfUserProbe.mockResolvedValue(liveUser);
});

afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
});

it('stays offline while the user is not authenticated', () => {
    renderSocket(false, <Subscriber topic="scan" handler={vi.fn()} />);
    expect(MockWebSocket.instances).toHaveLength(0);
});

it('opens exactly one socket for subscribers from all four admin pages', () => {
    // BookScanning subscribes to scan + fix_scan, Duplicates to duplicates,
    // GenreManagement to genres; the app-wide conversion traffic rides along.
    renderSocket(
        true,
        <>
            <Subscriber topic="scan" handler={vi.fn()} />
            <Subscriber topic="fix_scan" handler={vi.fn()} />
            <Subscriber topic="duplicates" handler={vi.fn()} />
            <Subscriber topic="genres" handler={vi.fn()} />
        </>,
    );
    expect(MockWebSocket.instances).toHaveLength(1);

    act(() => lastSocket().open());
    const subscriptions = lastSocket()
        .sentObjects()
        .filter((frame) => frame.type === 'subscribe')
        .map((frame) => frame.topic);
    expect(subscriptions.sort()).toEqual(['duplicates', 'fix_scan', 'genres', 'scan']);
});

it('delivers a topic message only to that topic’s handlers', () => {
    const scanHandler = vi.fn();
    const duplicatesHandler = vi.fn();
    renderSocket(
        true,
        <>
            <Subscriber topic="scan" handler={scanHandler} />
            <Subscriber topic="duplicates" handler={duplicatesHandler} />
        </>,
    );
    act(() => lastSocket().open());

    act(() =>
        lastSocket().receive({ type: 'scan_progress', topic: 'scan', data: { progress: 50 } }),
    );
    expect(scanHandler).toHaveBeenCalledTimes(1);
    expect(scanHandler.mock.calls[0][0]).toMatchObject({ type: 'scan_progress', topic: 'scan' });
    expect(duplicatesHandler).not.toHaveBeenCalled();

    act(() => lastSocket().receive({ type: 'duplicate_scan_progress', topic: 'duplicates' }));
    expect(duplicatesHandler).toHaveBeenCalledTimes(1);
    expect(scanHandler).toHaveBeenCalledTimes(1);
});

it('unsubscribes when the last handler of a topic unmounts', () => {
    // Stable handler identities: an effect that re-runs would resubscribe.
    const scanHandler = vi.fn();
    const genresHandler = vi.fn();
    const tree = (showScan: boolean) => (
        <BookConversionProvider>
            <WebSocketProvider isAuthenticated={true}>
                {/* Fixed slots: the genres subscriber must not remount when
                    the scan one leaves. */}
                <div>{showScan && <Subscriber topic="scan" handler={scanHandler} />}</div>
                <div>
                    <Subscriber topic="genres" handler={genresHandler} />
                </div>
            </WebSocketProvider>
        </BookConversionProvider>
    );
    const { rerender } = render(
        <BookConversionProvider>
            <WebSocketProvider isAuthenticated={true}>{null}</WebSocketProvider>
        </BookConversionProvider>,
    );
    rerender(tree(true));
    act(() => lastSocket().open());

    rerender(tree(false));

    const frames = lastSocket().sentObjects();
    expect(frames).toContainEqual({ type: 'unsubscribe', topic: 'scan' });
    expect(frames).not.toContainEqual({ type: 'unsubscribe', topic: 'genres' });
});

it('reconnects with backoff and re-sends every subscription', async () => {
    vi.useFakeTimers();
    renderSocket(true, <Subscriber topic="scan" handler={vi.fn()} />);
    act(() => lastSocket().open());
    expect(MockWebSocket.instances).toHaveLength(1);

    // The server drops the connection; the provider must replace it.
    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(MockWebSocket.instances.length).toBeGreaterThan(1);

    const reopened = lastSocket();
    expect(reopened.sentObjects()).toHaveLength(0);
    act(() => reopened.open());
    expect(reopened.sentObjects()).toContainEqual({ type: 'subscribe', topic: 'scan' });
});

it('sends conversion requests and consumes typed conversion replies', () => {
    let dispatchRef: ReturnType<typeof useBookConversion>['dispatch'] | null = null;
    let getConverting: () => unknown[] = () => [];
    const ConversionProbe = () => {
        const { state, dispatch } = useBookConversion();
        useEffect(() => {
            dispatchRef = dispatch;
            getConverting = () => state.convertingBooks;
        });
        return null;
    };

    renderSocket(true, <ConversionProbe />);
    act(() => lastSocket().open());

    act(() => {
        dispatchRef?.({ type: 'ADD_CONVERTING_BOOK', payload: { bookID: 42, format: 'mobi' } });
    });
    expect(lastSocket().sentObjects()).toContainEqual({
        type: 'convert',
        bookID: 42,
        format: 'mobi',
    });
    expect(getConverting()).toHaveLength(1);

    act(() =>
        lastSocket().receive({ type: 'conversion', bookID: 42, format: 'mobi', status: 'ready' }),
    );
    expect(getConverting()).toHaveLength(0);
    // The ready file is pulled through the hidden download frame.
    expect(document.querySelector('iframe')).not.toBeNull();
});

it('surfaces a failed conversion without taking the socket down', () => {
    let dispatchRef: ReturnType<typeof useBookConversion>['dispatch'] | null = null;
    let getErrors: () => unknown[] = () => [];
    const ConversionProbe = () => {
        const { state, dispatch } = useBookConversion();
        useEffect(() => {
            dispatchRef = dispatch;
            getErrors = () => state.conversionErrors;
        });
        return null;
    };

    renderSocket(true, <ConversionProbe />);
    act(() => lastSocket().open());
    act(() => {
        dispatchRef?.({ type: 'ADD_CONVERTING_BOOK', payload: { bookID: 7, format: 'epub' } });
    });

    act(() =>
        lastSocket().receive({
            type: 'conversion',
            bookID: 7,
            format: 'epub',
            status: 'error',
            error: 'boom',
        }),
    );
    expect(getErrors()).toEqual([{ bookID: 7, format: 'epub', message: 'boom' }]);
});

// Round 3: one timer, one owner, minimum delays, and no self-declared dead
// session — the transport's own logout path is the only stop.

it('keeps exactly one pending socket and the minimum delays through fast 503 probes', async () => {
    vi.useFakeTimers();
    renderSocket(true, <Subscriber topic="scan" handler={vi.fn()} />);
    selfUserProbe.mockRejectedValue(new ApiError('unavailable', 503));

    // Attempt one is refused before it ever opens; the probe answers at once.
    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
    });

    // Backoff step one is 1 s with up to 25% jitter: nothing may dial earlier.
    await act(async () => {
        await vi.advanceTimersByTimeAsync(999);
    });
    expect(MockWebSocket.instances).toHaveLength(1);

    await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
    });
    expect(MockWebSocket.instances).toHaveLength(2);

    // The second attempt fails too; still exactly one new socket per step.
    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
    });
    await act(async () => {
        await vi.advanceTimersByTimeAsync(2500);
    });
    expect(MockWebSocket.instances.length).toBeLessThanOrEqual(3);

    // Never a zero-delay retry: right after a refusal no new socket exists.
    for (let i = 0; i < 4; i++) {
        act(() => lastSocket().serverClose());
        await act(async () => {
            await vi.advanceTimersByTimeAsync(0);
        });
        const before = MockWebSocket.instances.length;
        await act(async () => {
            await vi.advanceTimersByTimeAsync(999);
        });
        expect(MockWebSocket.instances.length).toBe(before);
        await act(async () => {
            await vi.advanceTimersByTimeAsync(60_000);
        });
    }
    // Every refusal still schedules its retry: attempts keep coming.
    expect(MockWebSocket.instances.length).toBeGreaterThan(3);
});

it('a slow probe never overlaps the scheduled retry into a second socket', async () => {
    vi.useFakeTimers();
    renderSocket(true, <Subscriber topic="scan" handler={vi.fn()} />);

    // The probe hangs until the test releases it.
    let releaseProbe: (value: Awaited<ReturnType<typeof getCurrentUser>>) => void = () => {};
    selfUserProbe.mockImplementation(
        () =>
            new Promise((resolve) => {
                releaseProbe = resolve;
            }),
    );

    act(() => lastSocket().serverClose());
    // Run far past the first backoff step while the probe is still pending.
    await act(async () => {
        await vi.advanceTimersByTimeAsync(45_000);
    });
    const whileProbing = MockWebSocket.instances.length;
    // At most the one scheduled retry may have dialed; nothing else.
    expect(whileProbing).toBeLessThanOrEqual(2);

    // The late probe answer must not spawn another socket either.
    await act(async () => {
        releaseProbe(liveUser);
        await Promise.resolve();
    });
    await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
    });
    expect(MockWebSocket.instances.length).toBe(whileProbing);
});

it('keeps the capped backoff when the probe succeeds: no immediate retry', async () => {
    vi.useFakeTimers();
    renderSocket(true, <Subscriber topic="scan" handler={vi.fn()} />);
    selfUserProbe.mockResolvedValue(liveUser);

    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
    });
    // The session is alive, yet a live session is not a reason to hammer the
    // server: the 1 s minimum still applies.
    await act(async () => {
        await vi.advanceTimersByTimeAsync(999);
    });
    expect(MockWebSocket.instances).toHaveLength(1);
    await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
    });
    expect(MockWebSocket.instances).toHaveLength(2);
});

it('probes at most once per backoff step', async () => {
    vi.useFakeTimers();
    renderSocket(true, <Subscriber topic="scan" handler={vi.fn()} />);
    selfUserProbe.mockResolvedValue(liveUser);
    selfUserProbe.mockClear();

    // The initial close-before-open probes once.
    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
    });
    const probesForFirstRefusal = selfUserProbe.mock.calls.length;
    expect(probesForFirstRefusal).toBe(1);

    // A dropped live connection retries without probing at all.
    act(() => lastSocket().open());
    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(selfUserProbe).toHaveBeenCalledTimes(1);

    // Refused handshakes probe once per attempt, never more.
    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
    });
    expect(selfUserProbe).toHaveBeenCalledTimes(2);

    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(selfUserProbe).toHaveBeenCalledTimes(3);
});

it('resets the backoff after a successful open', async () => {
    vi.useFakeTimers();
    renderSocket(true, <Subscriber topic="scan" handler={vi.fn()} />);

    // Burn two backoff steps (1 s, then 2 s).
    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(1_500);
    });
    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(2_500);
    });
    expect(MockWebSocket.instances.length).toBe(3);

    // A successful open re-arms the schedule from the beginning.
    act(() => lastSocket().open());
    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(999);
    });
    expect(MockWebSocket.instances.length).toBe(3);
    await act(async () => {
        await vi.advanceTimersByTimeAsync(300);
    });
    expect(MockWebSocket.instances.length).toBe(4);
});

// Route-state probe: the counter lives outside the component so React's
// double-invoked render passes cannot lose it.
const routeMounts = vi.fn();
const RouteStateProbe = () => {
    useEffect(() => {
        routeMounts();
    }, []);
    return null;
};

it('route state survives a reconnect: nothing remounts', async () => {
    vi.useFakeTimers();
    routeMounts.mockClear();
    renderSocket(true, <RouteStateProbe />);
    act(() => lastSocket().open());
    // React's dev double mount makes an absolute count brittle; what matters
    // is that a reconnect adds none.
    const mountsBefore = routeMounts.mock.calls.length;

    act(() => lastSocket().serverClose());
    await act(async () => {
        await vi.advanceTimersByTimeAsync(60_000);
    });
    act(() => lastSocket().open());
    expect(routeMounts.mock.calls.length).toBe(mountsBefore);
});

it('keeps reconnecting through an outage and recovers when the server is back', async () => {
    vi.useFakeTimers();
    renderSocket(true, <Subscriber topic="scan" handler={vi.fn()} />);
    act(() => lastSocket().open());
    expect(MockWebSocket.instances).toHaveLength(1);

    // The server dies; every reconnect attempt fails before onopen, and the
    // probe cannot reach the server either (network-level failure).
    selfUserProbe.mockRejectedValue(new ApiError('network down', 0));
    act(() => lastSocket().serverClose());
    for (let i = 0; i < 6; i++) {
        act(() => lastSocket().serverClose());
        await act(async () => {
            await vi.advanceTimersByTimeAsync(40_000);
        });
    }
    // Far more than three attempts: an outage must never stop the socket.
    expect(MockWebSocket.instances.length).toBeGreaterThan(4);

    // The server is back: the next attempt opens and re-subscribes.
    act(() => lastSocket().open());
    expect(lastSocket().sentObjects()).toContainEqual({ type: 'subscribe', topic: 'scan' });
});

it('consumes the legacy untyped conversion reply during the compatibility window', () => {
    let dispatchRef: ReturnType<typeof useBookConversion>['dispatch'] | null = null;
    let getConverting: () => unknown[] = () => [];
    const ConversionProbe = () => {
        const { state, dispatch } = useBookConversion();
        useEffect(() => {
            dispatchRef = dispatch;
            getConverting = () => state.convertingBooks;
        });
        return null;
    };

    renderSocket(true, <ConversionProbe />);
    act(() => lastSocket().open());

    act(() => {
        dispatchRef?.({ type: 'ADD_CONVERTING_BOOK', payload: { bookID: 9, format: 'epub' } });
    });
    expect(getConverting()).toHaveLength(1);

    // The shape the pre-topics backend sent: no `type`, same fields.
    act(() => lastSocket().receive({ bookID: 9, format: 'epub', status: 'ready' }));
    expect(getConverting()).toHaveLength(0);
    expect(document.querySelector('iframe')).not.toBeNull();
});

it('ignores a legacy-shaped frame that is not a conversion reply', () => {
    const handler = vi.fn();
    renderSocket(true, <Subscriber topic="scan" handler={handler} />);
    act(() => lastSocket().open());

    // Missing pieces of the envelope must not reach any handler nor dispatch.
    const iframesBefore = document.querySelectorAll('iframe').length;
    act(() => lastSocket().receive({ bookID: 'nine', format: 'epub', status: 'ready' }));
    act(() => lastSocket().receive({ bookID: 9, status: 'ready' }));
    act(() => lastSocket().receive({ bookID: 9, format: 'epub', status: 'odd' }));
    expect(handler).not.toHaveBeenCalled();
    expect(document.querySelectorAll('iframe')).toHaveLength(iframesBefore);
});

it('signals a reconnect through the callback on a fresh open', () => {
    const onReconnect = vi.fn();
    render(
        <BookConversionProvider>
            <WebSocketProvider isAuthenticated={true} onReconnect={onReconnect}>
                {null}
            </WebSocketProvider>
        </BookConversionProvider>,
    );

    // The first open is not a reconnect.
    act(() => lastSocket().open());
    expect(onReconnect).not.toHaveBeenCalled();

    vi.useFakeTimers();
    act(() => lastSocket().serverClose());
    act(() => {
        vi.advanceTimersByTime(60_000);
    });
    act(() => lastSocket().open());
    expect(onReconnect).toHaveBeenCalledTimes(1);
});
