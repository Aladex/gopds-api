import { http, requestBlob } from '@/api/http';
import type { Author, Book, Series } from '@/api/books';
import { isApiError } from '@/api/errors';

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
    http.post<{ result?: TResult; error?: string }>(
        `/admin/books/${bookID}/rescan/approve`,
        payload,
    );

// --- Invites -------------------------------------------------------------

export const listInvites = <TInvite>() => http.get<{ result: TInvite[] }>('/admin/invites');

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

export const listDuplicates = <TGroup>(
    query?: Record<string, string | number | boolean | undefined>,
) => http.get<TGroup>('/admin/duplicates', { query });

export const getActiveDuplicateScan = <TScan>() => http.get<TScan>('/admin/duplicates/scan/active');

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

export const listScannedArchives = <TArchives>(
    query?: Record<string, string | number | boolean | undefined>,
) => http.get<TArchives>('/admin/scan/scanned', { query });

export const listUnscannedArchives = <TArchives>(
    query?: Record<string, string | number | boolean | undefined>,
) => http.get<TArchives>('/admin/scan/unscanned', { query });

export const listScanErrors = <TErrors>(
    query?: Record<string, string | number | boolean | undefined>,
) => http.get<TErrors>('/admin/scan/errors', { query });

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

// --- Author normalization -------------------------------------------------
//
// Types and routes follow the admin author-metadata contract exactly; the
// backend implements the same contract, so nothing here may drift from it.

export type AuthorMetadataRunMode = 'smoke' | 'pilot_archive' | 'full';

export type AuthorMetadataRunStatus =
    'pending' | 'running' | 'paused' | 'completed' | 'failed_systemic';

/** Terminal counts per closed extraction outcome, keyed as the backend sends them. */
export interface AuthorMetadataExtractionByStatus {
    extracted: number;
    extracted_no_author: number;
    already_current: number;
    entry_missing: number;
    invalid_fb2: number;
    unsupported_encoding: number;
    metadata_parse_failed: number;
}

export interface AuthorMetadataExtractionStage {
    total: number;
    done: number;
    pending: number;
    leased: number;
    oldest_pending_age_s: number;
    by_status: AuthorMetadataExtractionByStatus;
    current_archive: string | null;
    items_per_minute: number;
}

export interface AuthorMetadataLocalStage {
    total: number;
    done: number;
    pending: number;
    leased: number;
    failed: number;
    oldest_pending_age_s: number;
}

export interface AuthorMetadataReviewStage {
    open: number;
    closed: number;
}

export interface AuthorMetadataStages {
    extraction: AuthorMetadataExtractionStage;
    local: AuthorMetadataLocalStage;
    review: AuthorMetadataReviewStage;
}

/** Unresolved credits broken down by closed reason; the keys are server-defined. */
export interface AuthorMetadataCredits {
    selected: number;
    invalid: number;
    review: number;
    pending: number;
    unresolved: Record<string, number>;
}

export interface AuthorMetadataRun {
    id: number;
    mode: AuthorMetadataRunMode;
    status: AuthorMetadataRunStatus;
    extractor_version: string;
    normalizer_version: string;
    created_at: string;
    started_at: string | null;
    extraction_completed_at: string | null;
    completed_at: string | null;
    last_error_class: string | null;
    approved_for_full: boolean;
    stages: AuthorMetadataStages;
    credits: AuthorMetadataCredits;
}

export interface AuthorMetadataReport extends AuthorMetadataRun {
    ready: boolean;
    not_ready_reasons: string[];
    duration_s: number;
    db_growth_bytes: number;
    by_class: Record<string, number>;
    by_script: Record<string, number>;
}

export interface AuthorMetadataRunStart {
    mode: AuthorMetadataRunMode;
    book_ids?: number[];
    archive?: string;
}

export const startAuthorMetadataRun = (payload: AuthorMetadataRunStart) =>
    http.post<{ run: AuthorMetadataRun }>('/admin/author-metadata/runs', payload);

/**
 * The closed retry error classes per stage, as the runs contract publishes
 * them. A class valid for one stage can be invalid for the other, so the
 * payload type below only admits pairs the server accepts.
 */
export const AUTHOR_METADATA_EXTRACTION_RETRY_CLASSES = [
    'entry_missing',
    'invalid_fb2',
    'unsupported_encoding',
    'metadata_parse_failed',
    'lease_expired',
    'max_attempts_exceeded',
    'transient_database',
    'archive_unreadable',
    'extraction_failed',
] as const;

export const AUTHOR_METADATA_LOCAL_RETRY_CLASSES = [
    'transient_database',
    'normalizer_failed',
    'lease_expired',
    'max_attempts_exceeded',
] as const;

export type AuthorMetadataExtractionRetryClass =
    (typeof AUTHOR_METADATA_EXTRACTION_RETRY_CLASSES)[number];

export type AuthorMetadataLocalRetryClass = (typeof AUTHOR_METADATA_LOCAL_RETRY_CLASSES)[number];

export type AuthorMetadataRetryPayload =
    | { stage: 'extraction'; error_class: AuthorMetadataExtractionRetryClass }
    | { stage: 'local'; error_class: AuthorMetadataLocalRetryClass };

export const isExtractionRetryClass = (
    value: string,
): value is AuthorMetadataExtractionRetryClass =>
    (AUTHOR_METADATA_EXTRACTION_RETRY_CLASSES as readonly string[]).includes(value);

export const isLocalRetryClass = (value: string): value is AuthorMetadataLocalRetryClass =>
    (AUTHOR_METADATA_LOCAL_RETRY_CLASSES as readonly string[]).includes(value);

export const getCurrentAuthorMetadataRun = () =>
    http.get<{ run: AuthorMetadataRun | null }>('/admin/author-metadata/runs/current');

/**
 * The most recent run by id, whatever its status: what keeps a completed run
 * on the dashboard once the active slot empties, with no client-side memory.
 */
export const getLatestAuthorMetadataRun = () =>
    http.get<{ run: AuthorMetadataRun | null }>('/admin/author-metadata/runs/latest');

export const getAuthorMetadataRun = (runID: number) =>
    http.get<{ run: AuthorMetadataRun }>(`/admin/author-metadata/runs/${runID}`);

export const getAuthorMetadataRunReport = (runID: number) =>
    http.get<{ report: AuthorMetadataReport }>(`/admin/author-metadata/runs/${runID}/report`);

export const pauseAuthorMetadataRun = (runID: number) =>
    http.post<{ run: AuthorMetadataRun }>(`/admin/author-metadata/runs/${runID}/pause`);

export const resumeAuthorMetadataRun = (runID: number) =>
    http.post<{ run: AuthorMetadataRun }>(`/admin/author-metadata/runs/${runID}/resume`);

export const approveAuthorMetadataFullRun = (runID: number) =>
    http.post<{ run: AuthorMetadataRun }>(`/admin/author-metadata/runs/${runID}/approve-full`);

export const retryAuthorMetadataRun = (runID: number, payload: AuthorMetadataRetryPayload) =>
    http.post<{ reopened: number }>(`/admin/author-metadata/runs/${runID}/retry`, payload);

// --- Author normalization: manual review ----------------------------------
//
// Types and routes follow the admin author-metadata contract's Review section
// exactly; the backend implements the same contract, so nothing here may drift
// from it.

/** Reads the closed error code out of a failed request's body, if there is one. */
export function closedErrorCode(error: unknown): string | null {
    if (!isApiError(error)) {
        return null;
    }
    const body = error.body;
    if (!body || typeof body !== 'object') {
        return null;
    }
    const code = (body as Record<string, unknown>).error;
    return typeof code === 'string' ? code : null;
}

export type AuthorReviewScope = 'credit' | 'fingerprint';

export type AuthorReviewKind = 'person' | 'collective' | 'unknown' | 'malformed';

export interface AuthorReviewListItem {
    id: number;
    scope: AuthorReviewScope;
    credit_id: number | null;
    source_fingerprint: string;
    reason: string;
    decision_class: string;
    display_name: string;
    credits_count: number;
    created_at: string;
}

export interface AuthorReviewSource {
    first: string | null;
    middle: string | null;
    last: string | null;
    nickname: string | null;
    display: string;
    flags: string[];
}

export interface AuthorReviewProposal {
    given_name: string | null;
    additional_names: string | null;
    family_name: string | null;
    nickname: string | null;
    display_name: string;
    sort_name: string | null;
    search_key: string;
    script: string;
    kind: AuthorReviewKind;
    method: string;
    quality_flags: string[];
}

export interface AuthorReviewLinkedBook {
    id: number;
    title: string;
}

export interface AuthorReviewDetail extends AuthorReviewListItem {
    source: AuthorReviewSource;
    proposal: AuthorReviewProposal | null;
    linked_books: AuthorReviewLinkedBook[];
}

export interface AuthorReviewListQuery {
    status: 'open' | 'closed';
    cursor?: string;
    limit?: number;
}

/** The edit payload's result object; every name field is nullable but the display name. */
export interface AuthorReviewEditResult {
    given_name: string | null;
    additional_names: string | null;
    family_name: string | null;
    nickname: string | null;
    display_name: string;
    sort_name: string | null;
    kind: AuthorReviewKind;
}

export interface AuthorReviewClassifyKind {
    kind: 'collective' | 'unknown' | 'malformed';
}

export const listAuthorReviewItems = (query: AuthorReviewListQuery) =>
    http.get<{ items: AuthorReviewListItem[]; next_cursor: string | null }>(
        '/admin/author-metadata/review',
        { query: { status: query.status, cursor: query.cursor, limit: query.limit } },
    );

export const getAuthorReviewItem = (itemID: number) =>
    http.get<{ item: AuthorReviewDetail }>(`/admin/author-metadata/review/${itemID}`);

export const acceptAuthorReviewItem = (itemID: number, payload: { scope: AuthorReviewScope }) =>
    http.post<{ item: AuthorReviewDetail }>(
        `/admin/author-metadata/review/${itemID}/accept`,
        payload,
    );

export const editAuthorReviewItem = (
    itemID: number,
    payload: { scope: AuthorReviewScope; result: AuthorReviewEditResult },
) =>
    http.post<{ item: AuthorReviewDetail }>(
        `/admin/author-metadata/review/${itemID}/edit`,
        payload,
    );

export const classifyAuthorReviewItem = (
    itemID: number,
    payload: { scope: AuthorReviewScope } & AuthorReviewClassifyKind,
) =>
    http.post<{ item: AuthorReviewDetail }>(
        `/admin/author-metadata/review/${itemID}/classify`,
        payload,
    );

export const markAuthorReviewUnresolved = (itemID: number, payload: { scope: AuthorReviewScope }) =>
    http.post<{ item: AuthorReviewDetail }>(
        `/admin/author-metadata/review/${itemID}/unresolved`,
        payload,
    );

/** Re-runs the local normalizer; the contract sends no body for this action. */
export const retryAuthorReviewNormalization = (itemID: number) =>
    http.post<{ item: AuthorReviewDetail }>(`/admin/author-metadata/review/${itemID}/retry`);
