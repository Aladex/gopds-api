import { http, requestBlob } from './http';
import type { Author, Book, Series } from './books';

/**
 * Administrative endpoints.
 *
 * A few of these are reached from screens that are not themselves admin-only —
 * the book list offers an approve toggle and an edit dialog to superusers — so
 * this client is not exclusive to the admin area.
 */

/** updateBook toggles moderation state and other editable fields. */
export const updateBook = (book: Book & Record<string, unknown>) =>
    http.post<Book>('/admin/update-book', book);

/**
 * saveBook writes the full edit form. The endpoint answers either with the book
 * directly or wrapped in { result }, so both shapes are declared.
 */
export const saveBook = (bookID: number, payload: Record<string, unknown>) =>
    http.put<{ result?: Book } & Partial<Book>>(`/admin/books/${bookID}`, payload);

/**
 * uploadBookCover posts the image. Content-Type is left unset on purpose: the
 * browser has to add the multipart boundary itself.
 */
export const uploadBookCover = (bookID: number, form: FormData) =>
    http.post<{ result?: Book } & Partial<Book>>(`/admin/books/${bookID}/cover`, form);

export const searchAuthors = (query: string, limit = 20) =>
    http.get<{ authors?: Author[] }>('/admin/authors/search', { query: { q: query, limit } });

export const searchSeries = (query: string, limit = 20) =>
    http.get<{ series?: Series[] }>('/admin/series/search', { query: { q: query, limit } });

/**
 * rescanBook asks the backend to re-read a book's metadata and returns the
 * proposed change. The preview shape belongs to the caller, which knows what it
 * renders, so it is a type parameter rather than a guess repeated here.
 */
export const rescanBook = <TPreview>(bookID: number, payload?: unknown) =>
    http.post<{ result?: TPreview; error?: string }>(`/admin/books/${bookID}/rescan`, payload);

/** getRescanCoverPreview returns the candidate cover image itself. */
export const getRescanCoverPreview = (bookID: number) =>
    requestBlob(`/admin/books/${bookID}/rescan/preview-cover`);

export const approveRescan = <TResult>(bookID: number, payload: unknown) =>
    http.post<{ result?: TResult; error?: string }>(`/admin/books/${bookID}/rescan/approve`, payload);
