import { ApiError } from '@/api/errors';
import { previewClient, classifyPreviewError } from '@/api/preview';

// ponytail: test helpers only - minimal duplication with http.test.ts

function jsonResponse(body: unknown, status = 200, headers: Record<string, string> = {}) {
    return new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json', ...headers },
    });
}

let fetchSpy: ReturnType<typeof vi.fn>;

function urlOf(call: number): string {
    return fetchSpy.mock.calls[call][0] as string;
}

function initOf(call: number): RequestInit {
    return fetchSpy.mock.calls[call][1] as RequestInit;
}

beforeEach(() => {
    fetchSpy = vi.fn();
    globalThis.fetch = fetchSpy as unknown as typeof fetch;
});

describe('previewClient.getPreview', () => {
    it('returns preview metadata', async () => {
        const preview = {
            revision: 'abc123',
            chunk_count: 42,
            toc: [{ title: 'Chapter 1', depth: 1, chunk: 0, anchor: 'c1' }],
            images: [{ ordinal: 1, mime: 'image/jpeg', bytes: 1234 }],
            first_chunk: '<p>Content</p>',
        };
        fetchSpy.mockResolvedValue(jsonResponse(preview));

        const result = await previewClient.getPreview(123);

        expect(result).toEqual(preview);
        expect(urlOf(0)).toBe('/api/books/preview/123');
    });

    it('forwards abort signal to transport', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({}));
        const controller = new AbortController();

        await previewClient.getPreview(123, controller.signal);

        expect(initOf(0).signal).toBe(controller.signal);
    });
});

describe('previewClient.getChunk', () => {
    it('returns chunk HTML with revision', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ chunk: '<p>Chunk content</p>' }));

        const result = await previewClient.getChunk(123, 5, 'rev1');

        expect(result).toEqual({ chunk: '<p>Chunk content</p>' });
        expect(urlOf(0)).toContain('/api/books/preview/123/chunk/5');
        expect(urlOf(0)).toContain('revision=rev1');
    });

    it('throws if revision is missing', async () => {
        await expect(previewClient.getChunk(123, 5, '')).rejects.toThrow('revision is required');
        await expect(previewClient.getChunk(123, 5, '')).rejects.toThrow(ApiError);
        await expect(previewClient.getChunk(123, 5, '')).rejects.toSatisfy((e: ApiError) => e.status === 400);
        expect(fetchSpy).not.toHaveBeenCalled();
    });

    it('forwards abort signal to transport', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ chunk: '<p>Content</p>' }));
        const controller = new AbortController();

        await previewClient.getChunk(123, 5, 'rev1', controller.signal);

        expect(initOf(0).signal).toBe(controller.signal);
    });
});

describe('previewClient.getImage', () => {
    it('returns image blob with revision', async () => {
        const blob = new Blob(['fake image'], { type: 'image/jpeg' });
        fetchSpy.mockResolvedValue(
            new Response(blob, {
                status: 200,
                headers: { 'Content-Type': 'image/jpeg' },
            })
        );

        const result = await previewClient.getImage(123, 3, 'rev1');

        expect(result.type).toBe('image/jpeg');
        expect(urlOf(0)).toContain('/api/books/preview/123/image/3');
        expect(urlOf(0)).toContain('revision=rev1');
    });

    it('throws if revision is missing', async () => {
        await expect(previewClient.getImage(123, 3, '')).rejects.toThrow('revision is required');
        expect(fetchSpy).not.toHaveBeenCalled();
    });

    it('forwards abort signal to transport', async () => {
        const blob = new Blob(['fake'], { type: 'image/jpeg' });
        fetchSpy.mockResolvedValue(
            new Response(blob, {
                status: 200,
                headers: { 'Content-Type': 'image/jpeg' },
            })
        );
        const controller = new AbortController();

        await previewClient.getImage(123, 3, 'rev1', controller.signal);

        expect(initOf(0).signal).toBe(controller.signal);
    });
});

describe('classifyPreviewError', () => {
    it('classifies 404 as notFound', () => {
        const error = new ApiError('not found', 404);
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('notFound');
        expect(result.retryAfterSeconds).toBeUndefined();
    });

    it('classifies 410 as gone', () => {
        const error = new ApiError('revision expired', 410);
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('gone');
        expect(result.retryAfterSeconds).toBeUndefined();
    });

    it('classifies 429 as retryable with Retry-After', () => {
        const headers = new Headers({ 'retry-after': '30' });
        const error = new ApiError('too many requests', 429, { responseHeaders: headers });
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('retryable');
        expect(result.retryAfterSeconds).toBe(30);
    });

    it('classifies 503 as retryable with Retry-After', () => {
        const headers = new Headers({ 'retry-after': '60' });
        const error = new ApiError('service unavailable', 503, { responseHeaders: headers });
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('retryable');
        expect(result.retryAfterSeconds).toBe(60);
    });

    it('classifies 429 without Retry-After as retryable', () => {
        const error = new ApiError('too many requests', 429);
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('retryable');
        expect(result.retryAfterSeconds).toBeUndefined();
    });

    it('classifies 415 as permanent', () => {
        const error = new ApiError('unsupported media type', 415);
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('permanent');
        expect(result.retryAfterSeconds).toBeUndefined();
    });

    it('classifies 413 as permanent', () => {
        const error = new ApiError('payload too large', 413);
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('permanent');
        expect(result.retryAfterSeconds).toBeUndefined();
    });

    it('classifies 400 as permanent', () => {
        const error = new ApiError('bad request', 400);
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('permanent');
        expect(result.retryAfterSeconds).toBeUndefined();
    });

    it('classifies 500 as unknown', () => {
        const error = new ApiError('internal server error', 500);
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('unknown');
        expect(result.retryAfterSeconds).toBeUndefined();
    });

    it('classifies unexpected status as unknown', () => {
        const error = new ApiError('something unexpected', 418);
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('unknown');
        expect(result.retryAfterSeconds).toBeUndefined();
    });

    it('handles AbortError as retryable', () => {
        const error = new DOMException('Aborted', 'AbortError');
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('retryable');
        expect(result.retryAfterSeconds).toBeUndefined();
    });

    it('handles network errors as retryable', () => {
        const error = new ApiError('Network request failed', 0);
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('retryable');
        expect(result.retryAfterSeconds).toBeUndefined();
    });

    it('handles unknown error types as unknown', () => {
        const error = new Error('some other error');
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('unknown');
        expect(result.retryAfterSeconds).toBeUndefined();
    });
});

describe('mutation tests', () => {
    it('mutation: merging gone into retryable fails - they must be distinct', async () => {
        const error = new ApiError('revision expired', 410);
        const result = classifyPreviewError(error);
        expect(result.kind).toBe('gone');
        expect(result.kind).not.toBe('retryable');
    });

    it('mutation: ignoring Retry-After header fails - must be parsed', () => {
        const headers = new Headers({ 'retry-after': '30' });
        const error = new ApiError('too many requests', 429, { responseHeaders: headers });
        const result = classifyPreviewError(error);
        expect(result.retryAfterSeconds).toBe(30);
    });

    it('mutation: sending request without revision fails - must reject early', async () => {
        await expect(previewClient.getChunk(123, 5, '')).rejects.toThrow('revision is required');
        expect(fetchSpy).not.toHaveBeenCalled();
    });

    it('mutation: ignoring AbortSignal fails - must be forwarded', async () => {
        const controller = new AbortController();

        // The double has to honour the signal, the way a browser's fetch does.
        // Resolving regardless would test only that the mock ignores
        // cancellation: the client would look correct while forwarding a
        // signal nobody acts on, which is precisely the bug this guards.
        fetchSpy.mockImplementation((_input: unknown, init?: RequestInit) => {
            if (init?.signal?.aborted) {
                return Promise.reject(new DOMException('Aborted', 'AbortError'));
            }
            return Promise.resolve(jsonResponse({ chunk: '<p>Content</p>' }));
        });

        controller.abort();

        await expect(previewClient.getChunk(123, 5, 'rev1', controller.signal)).rejects.toSatisfy(
            (e: ApiError) => {
                return e.status === 0 && e.isNetworkError;
            }
        );
        expect(initOf(0).signal).toBe(controller.signal);
    });
});