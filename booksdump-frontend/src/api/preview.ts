import { http, requestBlob } from '@/api/http';
import { ApiError } from '@/api/errors';

export interface TocItem {
    title: string;
    depth: number;
    chunk: number;
    anchor: string;
}

export interface PreviewImage {
    ordinal: number;
    mime: string;
    bytes: number;
}

export interface PreviewResponse {
    revision: string;
    chunk_count: number;
    toc: TocItem[];
    images: PreviewImage[];
    first_chunk: string;
}

export interface ChunkResponse {
    chunk: string;
}

export type PreviewErrorKind = 'gone' | 'retryable' | 'permanent' | 'notFound' | 'unknown';

export interface ClassifiedPreviewError {
    kind: PreviewErrorKind;
    retryAfterSeconds?: number;
}

function requireRevision(value: string): void {
    if (!value || value.trim() === '') {
        throw new ApiError('revision is required', 400);
    }
}

export const previewClient = {
    getPreview: (bookId: number, signal?: AbortSignal) =>
        http.get<PreviewResponse>(`/books/preview/${bookId}`, { signal }),

    // Async so a missing revision rejects like any other failure. Throwing
    // synchronously would make one error arrive by a different route than the
    // rest, and every caller would need a try/catch around the call as well
    // as a .catch on it.
    getChunk: async (bookId: number, chunkNumber: number, revision: string, signal?: AbortSignal) => {
        requireRevision(revision);
        return http.get<ChunkResponse>(`/books/preview/${bookId}/chunk/${chunkNumber}`, {
            query: { revision },
            signal,
        });
    },

    getImage: async (bookId: number, imageNumber: number, revision: string, signal?: AbortSignal) => {
        requireRevision(revision);
        return requestBlob(`/books/preview/${bookId}/image/${imageNumber}`, {
            query: { revision },
            signal,
        });
    },
};

export function classifyPreviewError(error: unknown): ClassifiedPreviewError {
    if (error instanceof DOMException && error.name === 'AbortError') {
        return { kind: 'retryable' };
    }

    if (!(error instanceof ApiError)) {
        return { kind: 'unknown' };
    }

    if (error.isNetworkError) {
        return { kind: 'retryable' };
    }

    const retryAfter = parseRetryAfter(error);

    switch (error.status) {
        case 404:
            return { kind: 'notFound' };
        case 410:
            return { kind: 'gone' };
        case 429:
        case 503:
            return { kind: 'retryable', retryAfterSeconds: retryAfter };
        case 400:
        case 413:
        case 415:
            return { kind: 'permanent' };
        default:
            return { kind: 'unknown' };
    }
}

function parseRetryAfter(error: ApiError): number | undefined {
    const headers = (error as any).responseHeaders as Headers | undefined;
    const retryAfter = headers?.get('Retry-After');
    if (!retryAfter) {
        return undefined;
    }
    const seconds = parseInt(retryAfter, 10);
    return isNaN(seconds) ? undefined : seconds;
}