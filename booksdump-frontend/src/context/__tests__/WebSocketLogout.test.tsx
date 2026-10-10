import { act, render } from '@testing-library/react';
import { useEffect } from 'react';
import { createMemoryRouter, Outlet, RouterProvider, useNavigate } from 'react-router';

import * as authApi from '@/api/auth';
import { ApiError } from '@/api/errors';
import { http, requestBlob } from '@/api/http';
import { AuthProvider, useAuth } from '@/context/AuthContext';
import { BookConversionProvider } from '@/context/BookConversionContext';
import { WebSocketProvider, useWebSocket } from '@/context/WebSocketContext';

// Round 4 (R3-B2): the whole real stack — AuthProvider, auth API, HTTP
// transport and WebSocketProvider — with only fetch and WebSocket mocked. A
// refused handshake probes self-user; a 401 that survives the transport's
// refresh-and-replay is a confirmed logout and must stop the provider.
// Round 5 (R4-B1/N1): expiration belongs to the session the request started
// under and is idempotent for it.
// Round 6 (R5-B1/B2): an obsolete request neither refreshes nor replays, and
// the generation tracks the auth identity boundary in AuthContext rather than
// endpoint names in the transport.

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

    open() {
        this.onopen?.();
    }

    serverClose() {
        this.onclose?.();
    }
}

const liveUser = {
    username: 'reader',
    first_name: '',
    last_name: '',
    is_superuser: false,
};

const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
    });

// Observed from inside the tree.
const authStates: boolean[] = [];
let loginFn: () => void = () => {};
let logoutFn: () => void = () => {};
let setUserFn: (user: unknown) => void = () => {};
let navigateFn: (path: string) => void = () => {};

const AuthProbe = () => {
    const { isAuthenticated, login, logout, setUser } = useAuth();
    useEffect(() => {
        authStates.push(isAuthenticated);
    });
    useEffect(() => {
        loginFn = login;
    }, [login]);
    useEffect(() => {
        logoutFn = logout;
    }, [logout]);
    useEffect(() => {
        setUserFn = (user) => setUser(user as never);
    }, [setUser]);
    return null;
};

const NavigateProbe = () => {
    const navigate = useNavigate();
    useEffect(() => {
        navigateFn = navigate;
    }, [navigate]);
    return null;
};

const Subscriber = () => {
    const { subscribe } = useWebSocket();
    useEffect(() => subscribe('scan', () => {}), [subscribe]);
    return null;
};

const Shell = () => {
    const { isAuthenticated } = useAuth();
    return (
        <BookConversionProvider>
            <WebSocketProvider isAuthenticated={isAuthenticated}>
                <Subscriber />
            </WebSocketProvider>
        </BookConversionProvider>
    );
};

let fetchLog: string[] = [];

// renderApp mounts the real provider stack under a data router and returns
// the navigation log. router.subscribe fires once per navigation — including
// a duplicate push to the current path, which effect-based counting cannot
// see under React's double-invoked effects.
const renderApp = (): string[] => {
    const navigations: string[] = [];
    const router = createMemoryRouter(
        [
            {
                path: '/',
                element: (
                    <AuthProvider>
                        <AuthProbe />
                        <NavigateProbe />
                        <Shell />
                        <Outlet />
                    </AuthProvider>
                ),
                children: [
                    { path: 'books/page/1', element: null },
                    { path: 'login', element: null },
                ],
            },
        ],
        { initialEntries: ['/books/page/1'] },
    );
    router.subscribe((state) => {
        navigations.push(state.location.pathname);
    });
    render(<RouterProvider router={router} />);
    return navigations;
};

beforeEach(() => {
    vi.useFakeTimers();
    MockWebSocket.instances = [];
    authStates.length = 0;
    fetchLog = [];
    loginFn = () => {};
    logoutFn = () => {};
    setUserFn = () => {};
    navigateFn = () => {};
    vi.stubGlobal('WebSocket', MockWebSocket);
});

afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
});

// settle drains the microtask queue: init, probe, refresh and replay are all
// promise chains, and none of them needs a real timer.
const settle = async () => {
    await act(async () => {
        for (let i = 0; i < 60; i++) {
            await Promise.resolve();
        }
    });
};

const countCalls = (suffix: string) => fetchLog.filter((url) => url.endsWith(suffix)).length;

const lastAuth = () => authStates[authStates.length - 1];

it('a replayed 401 after a successful refresh is a confirmed logout that stops the socket', async () => {
    // self-user answers: probe 401, replay 401, then (after the login below)
    // 200 with the user.
    const selfUserAnswers = [
        () => new Response(null, { status: 401 }),
        () => new Response(null, { status: 401 }),
        () => json(liveUser),
    ];
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        fetchLog.push(url);
        if (url.endsWith('/api/init')) {
            return json({ csrf_token: 'csrf', user: liveUser });
        }
        if (url.endsWith('/api/books/self-user')) {
            const next = selfUserAnswers.shift();
            return next ? next() : json(liveUser);
        }
        if (url.endsWith('/api/refresh-token')) {
            return new Response(null, { status: 200 });
        }
        return new Response(null, { status: 404 });
    }) as unknown as typeof fetch;

    const navigations = renderApp();
    await settle();

    // Logged in through the real init flow: one socket, no reconnect timer.
    expect(lastAuth()).toBe(true);
    expect(MockWebSocket.instances).toHaveLength(1);

    // The handshake is refused: close-before-open. The provider probes
    // self-user through the real transport: 401, refresh 200, replay 401.
    act(() => MockWebSocket.instances[0].serverClose());
    await settle();

    expect(countCalls('/api/books/self-user')).toBe(2);
    expect(countCalls('/api/refresh-token')).toBe(1);

    // Confirmed logout: the app flipped in-page, no reload needed.
    expect(lastAuth()).toBe(false);
    expect(navigations).toEqual(['/login']);

    // The provider is down for good: no socket, no reconnect timer, however
    // far the clock runs.
    expect(vi.getTimerCount()).toBe(0);
    await act(async () => {
        await vi.advanceTimersByTimeAsync(600_000);
    });
    expect(MockWebSocket.instances).toHaveLength(1);

    // A fresh login starts the socket again.
    act(() => loginFn());
    await settle();
    expect(lastAuth()).toBe(true);
    expect(MockWebSocket.instances).toHaveLength(2);

    act(() => MockWebSocket.instances[1].open());
    expect(MockWebSocket.instances[1].sent.map((frame) => JSON.parse(frame))).toContainEqual({
        type: 'subscribe',
        topic: 'scan',
    });
});

// Round 5 (R4-B1): the reviewer's sequence — a request issued under the old
// session answers late, after the session was expired and a fresh login
// succeeded. Its 401 must not expire the new session.
it('a late 401 from a request of the expired session does not log out the new login', async () => {
    let releaseReplay: (response: Response) => void = () => {};
    let listCalls = 0;
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        fetchLog.push(url);
        if (url.endsWith('/api/init')) {
            return json({ csrf_token: 'csrf', user: liveUser });
        }
        if (url.endsWith('/api/refresh-token')) {
            // Request A's refresh succeeds; request B's fails.
            const earlier = fetchLog.slice(0, -1).filter((u) => u.endsWith('/api/refresh-token'));
            return new Response(null, { status: earlier.length === 0 ? 200 : 401 });
        }
        if (url.endsWith('/api/login')) {
            return json(liveUser);
        }
        if (url.endsWith('/api/books/list')) {
            listCalls += 1;
            if (listCalls === 1) {
                return new Response(null, { status: 401 });
            }
            // A's replay: pending until the test releases it.
            return new Promise<Response>((resolve) => {
                releaseReplay = resolve;
            });
        }
        if (url.endsWith('/api/books/fav')) {
            return new Response(null, { status: 401 });
        }
        return new Response(null, { status: 404 });
    }) as unknown as typeof fetch;

    const navigations = renderApp();
    await settle();
    expect(lastAuth()).toBe(true);
    expect(MockWebSocket.instances).toHaveLength(1);

    // A starts under the original session: 401, refresh succeeds, and its
    // replay stays in flight.
    const reqA = http.get('/books/list').then(
        () => 'resolved',
        (error: unknown) => error,
    );
    await settle();
    expect(countCalls('/api/refresh-token')).toBe(1);

    // B's refresh fails: the original session is confirmed expired.
    const reqB = http.get('/books/fav').then(
        () => 'resolved',
        (error: unknown) => error,
    );
    await settle();
    await reqB;
    expect(lastAuth()).toBe(false);
    expect(navigations).toEqual(['/login']);

    // The user logs in again for real: endpoint, setUser, back to the books.
    await act(async () => {
        const user = await authApi.login({ username: 'new-reader', password: 'pw' });
        setUserFn(user);
        navigateFn('/books/page/1');
    });
    await settle();
    expect(lastAuth()).toBe(true);
    expect(MockWebSocket.instances).toHaveLength(2);
    expect(navigations).toEqual(['/login', '/books/page/1']);

    // A's stale replay finally answers 401. It belongs to the expired
    // session: it rejects its own promise and nothing else happens.
    await act(async () => {
        releaseReplay(new Response(null, { status: 401 }));
    });
    await settle();
    expect(await reqA).toBeInstanceOf(ApiError);
    expect(lastAuth()).toBe(true);
    expect(navigations).toEqual(['/login', '/books/page/1']);
    expect(MockWebSocket.instances).toHaveLength(2);

    // And the socket keeps its ordinary reconnect behavior.
    await act(async () => {
        await vi.advanceTimersByTimeAsync(600_000);
    });
    expect(MockWebSocket.instances.length).toBeGreaterThanOrEqual(2);
});

// Round 5 (R4-N1): expiration is idempotent — two requests whose refreshes
// fail concurrently expire the session once.
it('two concurrent failed refreshes expire the session exactly once', async () => {
    const refreshReleases: Array<() => void> = [];
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        fetchLog.push(url);
        if (url.endsWith('/api/init')) {
            return json({ csrf_token: 'csrf', user: liveUser });
        }
        if (url.endsWith('/api/refresh-token')) {
            // Held until both requests are waiting on their refresh.
            return new Promise<Response>((resolve) => {
                refreshReleases.push(() => resolve(new Response(null, { status: 401 })));
            });
        }
        return new Response(null, { status: 401 });
    }) as unknown as typeof fetch;

    const navigations = renderApp();
    await settle();
    expect(lastAuth()).toBe(true);

    // Both requests 401 and are parked inside their refresh.
    const reqA = http.get('/books/a').then(
        () => 'resolved',
        (error: unknown) => error,
    );
    await settle();
    const reqB = http.get('/books/b').then(
        () => 'resolved',
        (error: unknown) => error,
    );
    await settle();
    expect(refreshReleases).toHaveLength(2);

    // Both refreshes fail. The session expires once.
    await act(async () => {
        refreshReleases.forEach((release) => release());
    });
    await settle();
    expect(await reqA).toBeInstanceOf(ApiError);
    expect(await reqB).toBeInstanceOf(ApiError);
    expect(lastAuth()).toBe(false);
    expect(navigations).toEqual(['/login']);
});

// Round 5 (R4-B1, companion): the same stale-request protection when the
// session changes through an explicit logout/login instead of an expiration.
it('a late 401 from before an explicit logout/login does not expire the fresh login', async () => {
    let releaseReplay: (response: Response) => void = () => {};
    let listCalls = 0;
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        fetchLog.push(url);
        if (url.endsWith('/api/init')) {
            return json({ csrf_token: 'csrf', user: liveUser });
        }
        if (url.endsWith('/api/refresh-token')) {
            return new Response(null, { status: 200 });
        }
        if (url.endsWith('/api/login')) {
            return json(liveUser);
        }
        if (url.endsWith('/api/logout')) {
            return json({});
        }
        if (url.endsWith('/api/csrf-token')) {
            return json({ csrf_token: 'csrf2' });
        }
        if (url.endsWith('/api/books/list')) {
            listCalls += 1;
            if (listCalls === 1) {
                return new Response(null, { status: 401 });
            }
            return new Promise<Response>((resolve) => {
                releaseReplay = resolve;
            });
        }
        return new Response(null, { status: 404 });
    }) as unknown as typeof fetch;

    const navigations = renderApp();
    await settle();
    expect(lastAuth()).toBe(true);

    // A starts under the original session; its replay stays in flight.
    const reqA = http.get('/books/list').then(
        () => 'resolved',
        (error: unknown) => error,
    );
    await settle();
    expect(countCalls('/api/books/list')).toBe(2);

    // Explicit logout and a fresh login, both through the real context.
    await act(async () => {
        await logoutFn();
    });
    await settle();
    expect(lastAuth()).toBe(false);
    await act(async () => {
        const user = await authApi.login({ username: 'new-reader', password: 'pw' });
        setUserFn(user);
        navigateFn('/books/page/1');
    });
    await settle();
    expect(lastAuth()).toBe(true);
    expect(navigations).toEqual(['/login', '/books/page/1']);

    // The stale replay's 401 belongs to a session that no longer exists.
    await act(async () => {
        releaseReplay(new Response(null, { status: 401 }));
    });
    await settle();
    expect(await reqA).toBeInstanceOf(ApiError);
    expect(lastAuth()).toBe(true);
    expect(navigations).toEqual(['/login', '/books/page/1']);
});

// Round 6 (R5-B1, JSON): a request that is still waiting for its original
// response while the session is replaced — logout, then login as another
// user — is obsolete. Its late 401 rejects the request itself; it must not
// refresh, and it must not replay the write under the new account.
it('an obsolete 401 after logout and a fresh login neither refreshes nor replays', async () => {
    let releaseOriginal: (response: Response) => void = () => {};
    let favCalls = 0;
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        fetchLog.push(url);
        if (url.endsWith('/api/init')) {
            return json({ csrf_token: 'csrf', user: liveUser });
        }
        if (url.endsWith('/api/login')) {
            return json({ ...liveUser, username: 'new-reader' });
        }
        if (url.endsWith('/api/logout')) {
            return json({});
        }
        if (url.endsWith('/api/csrf-token')) {
            return json({ csrf_token: 'csrf2' });
        }
        if (url.endsWith('/api/refresh-token')) {
            return new Response(null, { status: 200 });
        }
        if (url.endsWith('/api/books/fav')) {
            favCalls += 1;
            if (favCalls === 1) {
                // The original write: held until the test releases it.
                return new Promise<Response>((resolve) => {
                    releaseOriginal = resolve;
                });
            }
            return json({ ok: true });
        }
        return new Response(null, { status: 404 });
    }) as unknown as typeof fetch;

    const navigations = renderApp();
    await settle();
    expect(lastAuth()).toBe(true);

    // A favorite write starts under the original session and is held.
    const fav = http.post('/books/fav', { id: 7 }).then(
        () => 'resolved',
        (error: unknown) => error,
    );
    await settle();
    expect(countCalls('/api/books/fav')).toBe(1);

    // The session is replaced: explicit logout, then login as another user.
    await act(async () => {
        await logoutFn();
    });
    await settle();
    expect(lastAuth()).toBe(false);
    await act(async () => {
        const user = await authApi.login({ username: 'new-reader', password: 'pw' });
        setUserFn(user);
        navigateFn('/books/page/1');
    });
    await settle();
    expect(lastAuth()).toBe(true);
    expect(navigations).toEqual(['/login', '/books/page/1']);

    // The original write answers 401 only now. It belongs to the old account.
    await act(async () => {
        releaseOriginal(new Response(null, { status: 401 }));
    });
    await settle();
    expect(await fav).toBeInstanceOf(ApiError);
    expect(countCalls('/api/books/fav')).toBe(1);
    expect(countCalls('/api/refresh-token')).toBe(0);
    expect(lastAuth()).toBe(true);
    expect(navigations).toEqual(['/login', '/books/page/1']);
});

// Round 6 (R5-B1, JSON): the second generation window — the refresh is in
// flight while the user logs out. When the refresh finally succeeds, the
// request's session is already gone, so there is no replay.
it('a request whose refresh was in flight during logout is not replayed', async () => {
    let releaseRefresh: (response: Response) => void = () => {};
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        fetchLog.push(url);
        if (url.endsWith('/api/init')) {
            return json({ csrf_token: 'csrf', user: liveUser });
        }
        if (url.endsWith('/api/logout')) {
            return json({});
        }
        if (url.endsWith('/api/csrf-token')) {
            return json({ csrf_token: 'csrf2' });
        }
        if (url.endsWith('/api/refresh-token')) {
            // Held until the test releases it.
            return new Promise<Response>((resolve) => {
                releaseRefresh = resolve;
            });
        }
        if (url.endsWith('/api/books/list')) {
            return new Response(null, { status: 401 });
        }
        return new Response(null, { status: 404 });
    }) as unknown as typeof fetch;

    const navigations = renderApp();
    await settle();
    expect(lastAuth()).toBe(true);

    // The request 401s and parks inside its refresh.
    const list = http.get('/books/list').then(
        () => 'resolved',
        (error: unknown) => error,
    );
    await settle();
    expect(countCalls('/api/books/list')).toBe(1);
    expect(countCalls('/api/refresh-token')).toBe(1);

    // While the refresh is in flight, the user logs out for real.
    await act(async () => {
        await logoutFn();
    });
    await settle();
    expect(lastAuth()).toBe(false);
    expect(navigations).toEqual(['/login']);

    // The refresh finally succeeds. Replaying now would run the old request
    // under whatever session is current — there is none.
    await act(async () => {
        releaseRefresh(new Response(null, { status: 200 }));
    });
    await settle();
    expect(await list).toBeInstanceOf(ApiError);
    expect(countCalls('/api/books/list')).toBe(1);
    expect(lastAuth()).toBe(false);
    expect(navigations).toEqual(['/login']);
});

// Round 6 (R5-B1, Blob): the same in-flight-refresh window through the blob
// transport — a cover download must not be replayed after logout either.
it('a blob request whose refresh was in flight during logout is not replayed', async () => {
    let releaseRefresh: (response: Response) => void = () => {};
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        fetchLog.push(url);
        if (url.endsWith('/api/init')) {
            return json({ csrf_token: 'csrf', user: liveUser });
        }
        if (url.endsWith('/api/logout')) {
            return json({});
        }
        if (url.endsWith('/api/csrf-token')) {
            return json({ csrf_token: 'csrf2' });
        }
        if (url.endsWith('/api/refresh-token')) {
            return new Promise<Response>((resolve) => {
                releaseRefresh = resolve;
            });
        }
        if (url.endsWith('/api/books/cover/7')) {
            return new Response(null, { status: 401 });
        }
        return new Response(null, { status: 404 });
    }) as unknown as typeof fetch;

    const navigations = renderApp();
    await settle();
    expect(lastAuth()).toBe(true);

    const cover = requestBlob('/books/cover/7').then(
        () => 'resolved',
        (error: unknown) => error,
    );
    await settle();
    expect(countCalls('/api/books/cover/7')).toBe(1);
    expect(countCalls('/api/refresh-token')).toBe(1);

    await act(async () => {
        await logoutFn();
    });
    await settle();
    expect(lastAuth()).toBe(false);

    await act(async () => {
        releaseRefresh(new Response(null, { status: 200 }));
    });
    await settle();
    expect(await cover).toBeInstanceOf(ApiError);
    expect(countCalls('/api/books/cover/7')).toBe(1);
    expect(lastAuth()).toBe(false);
    expect(navigations).toEqual(['/login']);
});

// Round 6 (R5-B2): the generation moves with the auth identity, not with
// endpoint success. A failed logout still clears the user, and the context's
// own restoration installs it again — a late 401 from before both must not
// expire the restored user.
it('a late 401 from before a failed logout does not expire the restored user', async () => {
    let releaseReplay: (response: Response) => void = () => {};
    let listCalls = 0;
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        fetchLog.push(url);
        if (url.endsWith('/api/init')) {
            return json({ csrf_token: 'csrf', user: liveUser });
        }
        if (url.endsWith('/api/refresh-token')) {
            return new Response(null, { status: 200 });
        }
        if (url.endsWith('/api/logout')) {
            // The logout endpoint fails; the context clears the user anyway.
            return json({ error: 'boom' }, 500);
        }
        if (url.endsWith('/api/csrf-token')) {
            return json({ csrf_token: 'csrf2' });
        }
        if (url.endsWith('/api/books/self-user')) {
            return json(liveUser);
        }
        if (url.endsWith('/api/books/list')) {
            listCalls += 1;
            if (listCalls === 1) {
                return new Response(null, { status: 401 });
            }
            // The replay: pending until the test releases it.
            return new Promise<Response>((resolve) => {
                releaseReplay = resolve;
            });
        }
        return new Response(null, { status: 404 });
    }) as unknown as typeof fetch;

    const navigations = renderApp();
    await settle();
    expect(lastAuth()).toBe(true);

    // A request starts under the original session: 401, refresh succeeds,
    // and its replay stays in flight.
    const list = http.get('/books/list').then(
        () => 'resolved',
        (error: unknown) => error,
    );
    await settle();
    expect(countCalls('/api/books/list')).toBe(2);

    // The logout endpoint fails, but the context still clears the user.
    await act(async () => {
        await logoutFn();
    });
    await settle();
    expect(lastAuth()).toBe(false);
    expect(navigations).toEqual(['/login']);

    // Restoration through the context's own login(): the self-user call
    // installs the user again.
    await act(async () => {
        loginFn();
    });
    await settle();
    expect(lastAuth()).toBe(true);
    await act(async () => {
        navigateFn('/books/page/1');
    });
    await settle();
    expect(navigations).toEqual(['/login', '/books/page/1']);

    // The old replay answers 401. Two identity changes separate it from the
    // current session: it rejects itself and expires nothing.
    await act(async () => {
        releaseReplay(new Response(null, { status: 401 }));
    });
    await settle();
    expect(await list).toBeInstanceOf(ApiError);
    expect(lastAuth()).toBe(true);
    expect(navigations).toEqual(['/login', '/books/page/1']);
});
