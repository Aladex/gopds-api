import { ApiError } from '../errors';
import { http, request } from '../http';

// The contract the previous axios transport was pinned to before removal, plus
// the behaviours that are new: errors arrive as ApiError, a 404 no longer
// navigates, and an empty body is not a parse failure.

const CSRF = 'csrf-token-value';

let fetchSpy: ReturnType<typeof vi.fn>;

function jsonResponse(body: unknown, status = 200) {
    return new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
    });
}

function setCsrfCookie(value: string | null) {
    Object.defineProperty(document, 'cookie', {
        configurable: true,
        get: () => (value === null ? '' : `csrf_token=${value}`),
        set: () => undefined,
    });
}

let navigations: string[];

function setLocation(pathname: string) {
    navigations = [];
    Object.defineProperty(window, 'location', {
        configurable: true,
        value: {
            pathname,
            get href() {
                return `http://localhost${pathname}`;
            },
            set href(value: string) {
                navigations.push(value);
            },
        },
    });
}

/** lastInit returns the RequestInit of the nth fetch call. */
function initOf(call: number): RequestInit {
    return fetchSpy.mock.calls[call][1] as RequestInit;
}

function urlOf(call: number): string {
    return fetchSpy.mock.calls[call][0] as string;
}

beforeEach(() => {
    fetchSpy = vi.fn();
    globalThis.fetch = fetchSpy as unknown as typeof fetch;
    setCsrfCookie(CSRF);
    setLocation('/books/page/1');
});

describe('request', () => {
    it('sends credentials with every call', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ ok: true }));

        await http.get('/books/list');

        expect(initOf(0).credentials).toBe('include');
    });

    it('prefixes /api and keeps the given path', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({}));

        await http.get('/books/list');

        expect(urlOf(0)).toBe('/api/books/list');
    });

    it('appends query parameters, skipping empty ones', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({}));

        await http.get('/books/list', { query: { page: 2, title: 'dune', lang: undefined } });

        expect(urlOf(0)).toBe('/api/books/list?page=2&title=dune');
    });

    it('attaches the CSRF token to writes', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({}));

        await http.post('/books/fav', { id: 1 });

        expect(new Headers(initOf(0).headers).get('X-CSRF-Token')).toBe(CSRF);
    });

    it('does not attach the CSRF token to reads', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({}));

        await http.get('/books/list');

        expect(new Headers(initOf(0).headers).get('X-CSRF-Token')).toBeNull();
    });

    it('serialises an object body as JSON', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({}));

        await http.post('/books/fav', { id: 7 });

        expect(initOf(0).body).toBe('{"id":7}');
        expect(new Headers(initOf(0).headers).get('Content-Type')).toBe('application/json');
    });

    it('passes FormData through untouched', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({}));
        const form = new FormData();
        form.append('file', 'x');

        await http.post('/admin/books/1/cover', form);

        expect(initOf(0).body).toBe(form);
        expect(new Headers(initOf(0).headers).get('Content-Type')).toBeNull();
    });

    it('refreshes once on 401 and replays the call', async () => {
        fetchSpy
            .mockResolvedValueOnce(new Response(null, { status: 401 }))
            .mockResolvedValueOnce(new Response(null, { status: 200 }))
            .mockResolvedValueOnce(jsonResponse({ books: [] }));

        const result = await http.get<{ books: unknown[] }>('/books/list');

        expect(result).toEqual({ books: [] });
        expect(urlOf(1)).toBe('/api/refresh-token');
        expect(fetchSpy).toHaveBeenCalledTimes(3);
    });

    it('retries at most once, so an expired session cannot loop', async () => {
        // The refresh succeeds but the replayed call is unauthorised again.
        // Without a guard this is the shape that loops forever.
        fetchSpy
            .mockResolvedValueOnce(new Response(null, { status: 401 }))
            .mockResolvedValueOnce(new Response(null, { status: 200 }))
            .mockResolvedValue(new Response(null, { status: 401 }));

        await expect(http.get('/books/list')).rejects.toBeInstanceOf(ApiError);

        // original + refresh + one replay, then it gives up
        expect(fetchSpy).toHaveBeenCalledTimes(3);
    });

    it('gives up without replaying when the refresh itself is rejected', async () => {
        fetchSpy.mockResolvedValue(new Response(null, { status: 401 }));

        await expect(http.get('/books/list')).rejects.toBeInstanceOf(ApiError);

        // original + refresh, and no replay because the refresh failed
        expect(fetchSpy).toHaveBeenCalledTimes(2);
        expect(navigations).toContain('/login');
    });

    it('does not refresh the auth endpoints themselves', async () => {
        fetchSpy.mockResolvedValue(new Response(null, { status: 401 }));

        await expect(http.post('/login', {})).rejects.toBeInstanceOf(ApiError);

        expect(fetchSpy).toHaveBeenCalledTimes(1);
    });

    it('does not refresh while the user is on an auth page', async () => {
        setLocation('/login');
        fetchSpy.mockResolvedValue(new Response(null, { status: 401 }));

        await expect(http.get('/books/list')).rejects.toBeInstanceOf(ApiError);

        expect(fetchSpy).toHaveBeenCalledTimes(1);
        expect(navigations).toHaveLength(0);
    });

    it('sends the user to login when the refresh fails', async () => {
        fetchSpy
            .mockResolvedValueOnce(new Response(null, { status: 401 }))
            .mockResolvedValueOnce(new Response(null, { status: 401 }));

        await expect(http.get('/books/list')).rejects.toBeInstanceOf(ApiError);

        expect(navigations).toContain('/login');
    });

    it('rejects with the status and body on an error response', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ error: 'book is gone', code: 'BOOK_GONE' }, 410));

        const error = await http.get('/books/1').catch((e: unknown) => e);

        expect(error).toBeInstanceOf(ApiError);
        expect((error as ApiError).status).toBe(410);
        expect((error as ApiError).code).toBe('BOOK_GONE');
        expect((error as ApiError).message).toBe('book is gone');
    });

    it('does not navigate on a 404 — the page decides what to show', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ error: 'not found' }, 404));

        const error = await http.get('/books/999').catch((e: unknown) => e);

        expect((error as ApiError).isNotFound).toBe(true);
        expect(navigations).toHaveLength(0);
    });

    it('wraps a transport failure as a network error', async () => {
        fetchSpy.mockRejectedValue(new TypeError('Failed to fetch'));

        const error = await http.get('/books/list').catch((e: unknown) => e);

        expect(error).toBeInstanceOf(ApiError);
        expect((error as ApiError).isNetworkError).toBe(true);
    });

    it('returns undefined for an empty body instead of failing to parse', async () => {
        fetchSpy.mockResolvedValue(new Response(null, { status: 204 }));

        await expect(request('/books/theme', { method: 'DELETE' })).resolves.toBeUndefined();
    });
});
