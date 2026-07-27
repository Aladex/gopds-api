import axios from 'axios';
import MockAdapter from 'axios-mock-adapter';

import { fetchWithAuth, fetchWithCsrf } from '../config';

// Characterisation tests for the transport as it exists today, written before
// it is replaced. Everything here is easy to break silently during the rewrite:
// credentials, the CSRF header, and above all the refresh dance — retrying more
// than once turns one expired session into a request storm.

const CSRF = 'csrf-token-value';

let mock: MockAdapter;
// The refresh call goes through the bare axios module rather than the instance,
// because POST /api/refresh-token is registered without the CSRF middleware on
// the backend. It therefore needs its own adapter.
let refreshMock: MockAdapter;

function setCsrfCookie(value: string | null) {
    Object.defineProperty(document, 'cookie', {
        configurable: true,
        get: () => (value === null ? '' : `csrf_token=${value}`),
        set: () => undefined,
    });
}

function setLocation(pathname: string) {
    const assigned: string[] = [];
    Object.defineProperty(window, 'location', {
        configurable: true,
        value: {
            pathname,
            get href() {
                return `http://localhost${pathname}`;
            },
            set href(value: string) {
                assigned.push(value);
            },
            assigned,
        },
    });
    return assigned;
}

beforeEach(() => {
    mock = new MockAdapter(fetchWithAuth);
    refreshMock = new MockAdapter(axios);
    setCsrfCookie(CSRF);
    setLocation('/books/page/1');
});

afterEach(() => {
    mock.restore();
    refreshMock.restore();
});

describe('fetchWithAuth', () => {
    it('sends credentials with every request', async () => {
        mock.onGet('/books/list').reply(200, { ok: true });

        await fetchWithAuth.get('/books/list');

        expect(mock.history.get[0].withCredentials).toBe(true);
    });

    it('attaches the CSRF token to state-changing requests', async () => {
        mock.onPost('/books/fav').reply(200, {});

        await fetchWithAuth.post('/books/fav', {});

        expect(mock.history.post[0].headers?.['X-CSRF-Token']).toBe(CSRF);
    });

    it('does not attach the CSRF token to reads', async () => {
        mock.onGet('/books/list').reply(200, {});

        await fetchWithAuth.get('/books/list');

        expect(mock.history.get[0].headers?.['X-CSRF-Token']).toBeUndefined();
    });

    it('refreshes the session on 401 and replays the original request', async () => {
        mock.onGet('/books/list').replyOnce(401);
        refreshMock.onPost(/refresh-token$/).replyOnce(200, {});
        mock.onGet('/books/list').replyOnce(200, { books: [] });

        const response = await fetchWithAuth.get('/books/list');

        expect(response.status).toBe(200);
        expect(mock.history.get.filter((r) => r.url === '/books/list')).toHaveLength(2);
    });

    it('retries at most once, so an expired session cannot loop', async () => {
        mock.onGet('/books/list').reply(401);
        refreshMock.onPost(/refresh-token$/).reply(200, {});

        await expect(fetchWithAuth.get('/books/list')).rejects.toBeDefined();

        expect(mock.history.get.filter((r) => r.url === '/books/list')).toHaveLength(2);
    });

    it('does not try to refresh the auth endpoints themselves', async () => {
        mock.onPost('/login').reply(401);

        await expect(fetchWithAuth.post('/login', {})).rejects.toBeDefined();

        expect(refreshMock.history.post).toHaveLength(0);
    });

    it('does not try to refresh while the user is on an auth page', async () => {
        setLocation('/login');
        mock.onGet('/books/list').reply(401);

        await expect(fetchWithAuth.get('/books/list')).rejects.toBeDefined();

        expect(refreshMock.history.post).toHaveLength(0);
    });
});

describe('fetchWithCsrf', () => {
    const originalFetch = globalThis.fetch;

    afterEach(() => {
        globalThis.fetch = originalFetch;
    });

    it('includes credentials and the CSRF header on writes', async () => {
        const spy = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
        globalThis.fetch = spy as unknown as typeof fetch;

        await fetchWithCsrf('/api/register', { method: 'POST', body: '{}' });

        const [, init] = spy.mock.calls[0];
        expect(init.credentials).toBe('include');
        expect((init.headers as Headers).get('X-CSRF-Token')).toBe(CSRF);
        expect((init.headers as Headers).get('Content-Type')).toBe('application/json');
    });

    it('omits the CSRF header on reads', async () => {
        const spy = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
        globalThis.fetch = spy as unknown as typeof fetch;

        await fetchWithCsrf('/api/status');

        const [, init] = spy.mock.calls[0];
        expect((init.headers as Headers).get('X-CSRF-Token')).toBeNull();
    });
});
