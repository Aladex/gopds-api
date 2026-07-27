import { http, requestBlob } from '@/api/http';
import type { Author, Book, Series } from '@/api/books';

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

// --- Invites -------------------------------------------------------------

export const listInvites = <TInvite>() =>
    http.get<{ result: TInvite[] }>('/admin/invites');

/** changeInvite performs create, update or delete depending on the action. */
export const changeInvite = <TInvite>(action: 'create' | 'update' | 'delete', invite: TInvite) =>
    http.post<unknown>('/admin/invite', { action, invite });

// --- Users ---------------------------------------------------------------

export interface UsersQuery {
    limit: number;
    offset: number;
    username?: string;
    order?: string;
    desc?: boolean;
}

export const listUsers = <TUser>(query: UsersQuery) =>
    http.post<{ users: TUser[]; length: number }>('/admin/users', query);

export const changeUser = <TUser>(action: 'create' | 'update' | 'delete', user: TUser) =>
    http.post<{ user?: TUser }>('/admin/user', { action, user });

export const deleteUser = (userID: number | string) =>
    http.delete<unknown>(`/admin/user/${userID}`);

// --- Genres --------------------------------------------------------------

export const listGenres = <TGenre>() => http.get<{ result: TGenre[] }>('/admin/genres');

export const updateGenre = <TGenre>(genreID: number, genre: unknown) =>
    http.put<{ result?: TGenre }>(`/admin/genres/${genreID}`, genre);

export const generateGenreTitles = (payload?: unknown) =>
    http.post<unknown>('/admin/genres/generate-titles', payload);

// --- Duplicates ----------------------------------------------------------

export const listDuplicates = <TGroup>(query?: Record<string, string | number | boolean | undefined>) =>
    http.get<TGroup>('/admin/duplicates', { query });

export const getActiveDuplicateScan = <TScan>() =>
    http.get<TScan>('/admin/duplicates/scan/active');

export const startDuplicateScan = <TScan>(payload?: unknown) =>
    http.post<TScan>('/admin/duplicates/scan', payload);

export const stopDuplicateScan = (scanID: number | string) =>
    http.post<unknown>(`/admin/duplicates/scan/${scanID}/stop`);

export const forceStopDuplicateScan = (scanID: number | string) =>
    http.post<unknown>(`/admin/duplicates/scan/${scanID}/force-stop`);

export const hideDuplicates = <TResult>(payload: unknown) =>
    http.post<TResult>('/admin/duplicates/hide', payload);

// --- Archive scanning ----------------------------------------------------

export const getScanStatus = <TStatus>() => http.get<TStatus>('/admin/scan/status');

export const listScannedArchives = <TArchives>(query?: Record<string, string | number | boolean | undefined>) =>
    http.get<TArchives>('/admin/scan/scanned', { query });

export const listUnscannedArchives = <TArchives>(query?: Record<string, string | number | boolean | undefined>) =>
    http.get<TArchives>('/admin/scan/unscanned', { query });

export const listScanErrors = <TErrors>(query?: Record<string, string | number | boolean | undefined>) =>
    http.get<TErrors>('/admin/scan/errors', { query });

/** getScanErrorFile downloads the offending file itself, so it is not JSON. */
export const getScanErrorFile = (archive: string, file: string) =>
    requestBlob('/admin/scan/errors/file', { query: { archive, file } });

/** resetArchive forgets a scanned archive, optionally deleting its books. */
export const resetArchive = (archiveName: string, deleteBooks: boolean) =>
    http.delete<unknown>(`/admin/scan/reset/${encodeURIComponent(archiveName)}`, {
        query: { confirm: true, delete_books: deleteBooks },
    });

export const startScan = <TResult>(payload?: unknown) => http.post<TResult>('/admin/scan', payload);

export const scanArchive = <TResult>(payload: unknown) =>
    http.post<TResult>('/admin/scan/archive', payload);

export const getFixScanStatus = <TStatus>() => http.get<TStatus>('/admin/scan/fix/status');

export const startFixScan = <TResult>(payload?: unknown) =>
    http.post<TResult>('/admin/scan/fix', payload);

export const cancelFixScan = () => http.post<unknown>('/admin/scan/fix/cancel');
