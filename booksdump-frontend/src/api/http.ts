import { ApiError, messageFromBody } from '@/api/errors';

/**
 * The single JSON transport. Every resource client goes through request(); file
 * downloads and WebSockets deliberately do not, because they are not JSON and
 * have their own lifecycles.
 *
 * In production API_BASE is empty, so requests are same-origin against the Go
 * binary serving the SPA. In development it points at the backend.
 */
const API_BASE = import.meta.env.VITE_API_URL ?? '';

/** Endpoints that must never trigger a refresh: they are the refresh machinery. */
const AUTH_ENDPOINTS = ['/login', '/refresh-token', '/csrf-token'];

/** Routes where a 401 is expected and must not bounce the user anywhere. */
const AUTH_ROUTES = [
    '/login',
    '/register',
    '/forgot-password',
    '/activation',
    '/activate',
    '/change-password',
];

const WRITE_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

export interface RequestOptions extends Omit<RequestInit, 'body'> {
    /** Serialised as JSON unless it is already a string, FormData or Blob. */
    body?: unknown;
    /** Appended as a query string, skipping undefined and null values. */
    query?: Record<string, string | number | boolean | undefined | null>;
    /** Set internally when replaying a request after a refresh. */
    skipRefresh?: boolean;
}

function csrfToken(): string | null {
    const value = document.cookie
        .split('; ')
        .find((row) => row.startsWith('csrf_token='))
        ?.split('=')[1];
    return value || null;
}

function buildUrl(path: string, query?: RequestOptions['query']): string {
    const url = `${API_BASE}/api${path.startsWith('/') ? path : `/${path}`}`;
    if (!query) {
        return url;
    }

    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) {
        if (value !== undefined && value !== null) {
            params.append(key, String(value));
        }
    }
    const search = params.toString();
    return search ? `${url}?${search}` : url;
}

function isAuthEndpoint(path: string): boolean {
    return AUTH_ENDPOINTS.some((endpoint) => path.includes(endpoint));
}

function onAuthRoute(): boolean {
    const current = window.location.pathname;
    return AUTH_ROUTES.some((route) => current.includes(route));
}

function prepareBody(body: unknown, headers: Headers): BodyInit | undefined {
    if (body === undefined || body === null) {
        return undefined;
    }
    if (typeof body === 'string' || body instanceof FormData || body instanceof Blob) {
        return body;
    }
    if (!headers.has('Content-Type')) {
        headers.set('Content-Type', 'application/json');
    }
    return JSON.stringify(body);
}

/** parseBody returns undefined for empty responses rather than throwing. */
async function parseBody(response: Response): Promise<unknown> {
    if (response.status === 204) {
        return undefined;
    }
    const text = await response.text();
    if (text === '') {
        return undefined;
    }
    try {
        return JSON.parse(text);
    } catch {
        return text;
    }
}

async function refreshSession(): Promise<boolean> {
    try {
        // Registered without the CSRF middleware on the backend, so no header.
        const response = await fetch(`${API_BASE}/api/refresh-token`, {
            method: 'POST',
            credentials: 'include',
        });
        return response.ok;
    } catch {
        return false;
    }
}

/**
 * request performs a JSON call and returns the parsed body.
 *
 * On 401 it refreshes the session once and replays the call. It never retries
 * twice: a second failure means the session is genuinely gone, and looping would
 * turn one expired cookie into a request storm.
 *
 * A 404 rejects with ApiError like any other status. It does not navigate: a
 * missing book is for the page to render, not a reason to replace the whole
 * application with an error screen.
 */
export async function request<T = unknown>(path: string, options: RequestOptions = {}): Promise<T> {
    const { body, query, skipRefresh, headers: initHeaders, ...rest } = options;
    const method = (rest.method ?? 'GET').toUpperCase();

    const headers = new Headers(initHeaders);
    if (WRITE_METHODS.has(method)) {
        const token = csrfToken();
        if (token) {
            headers.set('X-CSRF-Token', token);
        }
    }

    const preparedBody = prepareBody(body, headers);

    let response: Response;
    try {
        response = await fetch(buildUrl(path, query), {
            ...rest,
            method,
            headers,
            body: preparedBody,
            credentials: 'include',
        });
    } catch (cause) {
        throw ApiError.network(cause);
    }

    if (response.status === 401 && !skipRefresh && !isAuthEndpoint(path) && !onAuthRoute()) {
        if (await refreshSession()) {
            return request<T>(path, { ...options, skipRefresh: true });
        }
        if (!onAuthRoute()) {
            window.location.href = '/login';
        }
    }

    const parsed = await parseBody(response);

    if (!response.ok) {
        const code =
            parsed && typeof parsed === 'object'
                ? ((parsed as Record<string, unknown>).code as string | undefined)
                : undefined;
        throw new ApiError(messageFromBody(parsed, response.statusText || `HTTP ${response.status}`), response.status, {
            code,
            body: parsed,
        });
    }

    return parsed as T;
}

/**
 * requestBlob is the same call as request(), for endpoints that answer with
 * binary data rather than JSON. It exists so image and file responses still go
 * through one place for credentials, CSRF and the refresh dance.
 */
export async function requestBlob(path: string, options: RequestOptions = {}): Promise<Blob> {
    const { body, query, skipRefresh, headers: initHeaders, ...rest } = options;
    const method = (rest.method ?? 'GET').toUpperCase();

    const headers = new Headers(initHeaders);
    if (WRITE_METHODS.has(method)) {
        const token = csrfToken();
        if (token) {
            headers.set('X-CSRF-Token', token);
        }
    }

    let response: Response;
    try {
        response = await fetch(buildUrl(path, query), {
            ...rest,
            method,
            headers,
            body: prepareBody(body, headers),
            credentials: 'include',
        });
    } catch (cause) {
        throw ApiError.network(cause);
    }

    if (response.status === 401 && !skipRefresh && !isAuthEndpoint(path) && !onAuthRoute()) {
        if (await refreshSession()) {
            return requestBlob(path, { ...options, skipRefresh: true });
        }
    }

    if (!response.ok) {
        throw new ApiError(response.statusText || `HTTP ${response.status}`, response.status);
    }

    return response.blob();
}

export const http = {
    get: <T>(path: string, options?: RequestOptions) => request<T>(path, { ...options, method: 'GET' }),
    post: <T>(path: string, body?: unknown, options?: RequestOptions) =>
        request<T>(path, { ...options, method: 'POST', body }),
    put: <T>(path: string, body?: unknown, options?: RequestOptions) =>
        request<T>(path, { ...options, method: 'PUT', body }),
    patch: <T>(path: string, body?: unknown, options?: RequestOptions) =>
        request<T>(path, { ...options, method: 'PATCH', body }),
    delete: <T>(path: string, options?: RequestOptions) => request<T>(path, { ...options, method: 'DELETE' }),
};
