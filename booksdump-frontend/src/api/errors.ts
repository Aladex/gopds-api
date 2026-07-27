/**
 * ApiError is what every failed request rejects with, so callers can branch on
 * a status instead of digging through a transport-specific error shape.
 */
export class ApiError extends Error {
    readonly status: number;
    /** Stable machine-readable code, when the backend sent one. */
    readonly code?: string;
    /** Parsed response body, when there was one. */
    readonly body?: unknown;

    constructor(message: string, status: number, options: { code?: string; body?: unknown } = {}) {
        super(message);
        this.name = 'ApiError';
        this.status = status;
        this.code = options.code;
        this.body = options.body;
    }

    /** A request that never reached the server, or whose response was unreadable. */
    static network(cause: unknown): ApiError {
        const message = cause instanceof Error ? cause.message : 'Network request failed';
        return new ApiError(message, 0, { body: cause });
    }

    get isNetworkError(): boolean {
        return this.status === 0;
    }

    get isNotFound(): boolean {
        return this.status === 404;
    }

    get isUnauthorized(): boolean {
        return this.status === 401;
    }
}

/** isApiError narrows an unknown catch binding. */
export function isApiError(value: unknown): value is ApiError {
    return value instanceof ApiError;
}

/**
 * messageFromBody digs a human-readable message out of the shapes this backend
 * currently uses. It exists because the API has no single error envelope yet —
 * that is Phase 6. Until then the guesswork lives here rather than in every
 * component.
 */
export function messageFromBody(body: unknown, fallback: string): string {
    if (typeof body === 'string' && body.trim() !== '') {
        return body;
    }
    if (body && typeof body === 'object') {
        for (const key of ['message', 'error', 'detail'] as const) {
            const value = (body as Record<string, unknown>)[key];
            if (typeof value === 'string' && value.trim() !== '') {
                return value;
            }
        }
    }
    return fallback;
}
