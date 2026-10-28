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

/** An LLM run has the metadata runs' statuses plus closed (by an administrator). */
export type AuthorLLMRunStatus = AuthorMetadataRunStatus | 'closed';

/** Terminal counts per closed extraction outcome, keyed as the backend sends them. */
export interface AuthorMetadataExtractionByStatus {
    extracted: number;
    extracted_no_author: number;
    already_current: number;
    entry_missing: number;
    invalid_fb2: number;
    unsupported_encoding: number;
    metadata_parse_failed: number;
    archive_missing: number;
    archive_unreadable: number;
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
    /** How far a full run's seeding is; null once it started (and for every other run). */
    seeding: AuthorMetadataSeeding | null;
    /** When the local, review and credit figures were computed; null before the first time. */
    aggregates_as_of: string | null;
}

export interface AuthorMetadataSeeding {
    seeded: number;
    target: number;
}

/** An archive the run found missing or unreadable, with the run's books in it. */
export interface AuthorMetadataProblemArchive {
    archive: string;
    books: number;
    reason: 'archive_missing' | 'archive_unreadable';
    /** A requested deletion of the archive's book records, while it runs; null otherwise. */
    deletion: { deleted: number; total: number } | null;
}

/** A requested deletion of an archive's book records, worked off in the background. */
export interface AuthorMetadataArchiveDeletion {
    archive: string;
    books_total: number;
    books_deleted: number;
    status: 'pending' | 'done';
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
    'archive_missing',
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

export const getAuthorMetadataRunArchives = (runID: number) =>
    http.get<{ archives: AuthorMetadataProblemArchive[] }>(
        `/admin/author-metadata/runs/${runID}/archives`,
    );

/** Reads the run's books of one problem archive again, e.g. after the file came back. */
export const retryAuthorMetadataArchive = (runID: number, archive: string) =>
    http.post<{ reopened: number }>(`/admin/author-metadata/runs/${runID}/archives/retry`, {
        archive,
    });

/**
 * Requests the deletion of the catalogue records of the books of a problem
 * archive; the server answers at once and deletes them in the background.
 */
export const deleteAuthorMetadataArchive = (runID: number, archive: string) =>
    http.post<{ deletion: AuthorMetadataArchiveDeletion }>(
        `/admin/author-metadata/runs/${runID}/archives/delete`,
        { archive },
    );

// --- Author normalization: manual review ----------------------------------
//
// Types and routes follow the admin author-metadata contract's Review section
// exactly; the backend implements the same contract, so nothing here may drift
// from it.

/** Reads the closed error code out of a failed request's body, if there is one. */
// --- Author LLM block -----------------------------------------------------
//
// The LLM block of the author metadata card. The chain is fixed on the
// server: check quality (eval pilot) → check everything (eval full), or one
// of the four stage checks (eval stage0..3, each a fixed set of strata) →
// resolve pilot → resolve everything → check the swaps (eval swap) → swaps
// pilot → swap everything; each step says whether it is built, enabled and
// done, and why it is not enabled.

export type AuthorLLMStepKey =
    | 'eval_pilot'
    | 'eval_full'
    | 'eval_stage0'
    | 'eval_stage1'
    | 'eval_stage2'
    | 'eval_stage3'
    | 'resolve_pilot'
    | 'resolve_full'
    | 'swap_eval'
    | 'swap_pilot'
    | 'swap_full'
    // The merge configuration's one step (E4c).
    | 'merge0';

export interface AuthorLLMStep {
    key: AuthorLLMStepKey;
    kind: string;
    mode: string;
    available: boolean;
    enabled: boolean;
    done: boolean;
    blocked?: 'not_configured' | 'not_available' | 'prerequisite' | 'active_run';
    // A stage eval's strata and its token estimate before it starts.
    strata?: string[];
    estimate_tokens?: number;
}

export interface AuthorLLMReportSummary {
    sha256: string;
    strata_total: number;
    strata_passed: number;
    excerpt_helps: number;
    registrable: boolean;
    /** Each participant's changed repeated answers; watched, not a gate. */
    repeats?: AuthorLLMRepeatChange[];
    /** The version summed up, the latest; every version, oldest first. */
    version?: number;
    versions?: AuthorLLMReportVersion[];
}

/** One version of an eval report: a recomputation adds one, the old ones stay. */
export interface AuthorLLMReportVersion {
    version: number;
    sha256: string;
    registrable: boolean;
    created_at: string;
}

export interface AuthorLLMRepeatChange {
    participant: string;
    changed: number;
    total: number;
}

/** The one line the card shows of a resolve report. */
export interface AuthorLLMResolveSummary {
    sha256: string;
    applied: number;
    total: number;
}

export interface AuthorLLMRun {
    id: number;
    kind: string;
    mode: string;
    status: AuthorLLMRunStatus;
    stage?: string;
    paused_reason?: string;
    last_error_class?: string;
    current_config: boolean;
    progress?: { done: number; total: number };
    estimate_tokens: number;
    limit_tokens: number;
    spent_tokens: number;
    cost?: number;
    report?: AuthorLLMReportSummary;
    resolve?: AuthorLLMResolveSummary;
}

export interface AuthorLLMParticipant {
    slot: string;
    model: string;
    paused_reason?: string;
}

export interface AuthorLLMStatus {
    enabled: boolean;
    reason?: string;
    run: AuthorLLMRun | null;
    /** A run of the other live configuration (names or merges) is active. */
    other_active?: boolean;
    steps: AuthorLLMStep[];
    participants: AuthorLLMParticipant[] | null;
    endpoint_paused_reason?: string;
    budget_tokens: number;
    spent_tokens: number;
    price_unit?: string;
}

/** A distribution row of the author entity report. */
export interface AuthorEntitySize {
    label: string;
    entities: number;
    books: number;
}

/** The stored report of an author entity build pass: counts and IDs only. */
export interface AuthorEntityReport {
    credits: number;
    books: number;
    fingerprints: number;
    entities: number;
    initials_entities: number;
    sizes: AuthorEntitySize[];
    top: { id: string; books: number }[];
    typo_merges: number;
    no_entity: Record<string, number>;
    buckets: {
        buckets: number;
        books: number;
        with_full_names: number;
        with_full_names_books: number;
        moved: number;
        moved_by: Record<string, number>;
        conflicting: number;
        samizdat_books: number;
        samizdat_kept: number;
        remaining: number;
        emptied: number;
        entities: number;
    };
    ids: { inherited: number; new: number; merged: number; orphaned: number };
    legacy: {
        authors: number;
        entities_per_author: AuthorEntitySize[];
        shared_entities: number;
    };
    /** What the Wikidata evidence did (E4w); absent in a report from before it. */
    wikidata?: AuthorEntityWikidataReport;
    unselected_credits: number;
    units_skipped: number;
    changes: number;
    timings: { load_ms: number; plan_ms: number; apply_ms: number };
}

/** The Wikidata part of a build report: what the main-label rule merged and kept apart. */
export interface AuthorEntityWikidataReport {
    extract: string;
    named?: number;
    excluded?: Record<string, number>;
    groups: number;
    merges: Record<string, number>;
    script_conflicts?: number;
    shared_book: number;
    references?: number;
    references_withheld?: number;
}

/** One fetch of the Wikidata reference. */
export interface WikidataExtract {
    id: number;
    content_id?: string;
    status: 'fetching' | 'failed' | 'published' | 'duplicate' | 'superseded';
    source: string;
    people: number;
    names: number;
    works: number;
    last_error?: string;
    started_at: string;
    finished_at?: string;
    chunks_done: number;
    chunks_total: number;
}

/** The newest fetch and the published extract the build reads. */
export interface WikidataStatus {
    latest: WikidataExtract | null;
    published: WikidataExtract | null;
}

/** An author entity build pass. */
export interface AuthorEntityRun {
    id: number;
    status: 'pending' | 'running' | 'completed' | 'failed';
    stage?: 'plan' | 'apply' | 'report';
    units_total: number;
    units_done: number;
    units_skipped: number;
    changes: number;
    last_error_class?: string;
    created_at: string;
    started_at?: string;
    finished_at?: string;
}

/** The autopilot: the switch, what its last check found and did, and the scan. */
export interface AuthorAutopilotStatus {
    enabled: boolean;
    state?:
        | 'not_quiet'
        | 'normalizing'
        | 'llm_run_active'
        | 'entity_build_active'
        | 'resolve_started'
        | 'build_started'
        | 'idle';
    state_run_id?: number;
    resolve_note?: string;
    resolve_inputs: number;
    last_action?: 'resolve' | 'build';
    last_action_run_id?: number;
    last_action_at?: string;
    failures: number;
    last_error_class?: string;
    next_attempt_at?: string;
    updated_at: string;
    quiet_seconds: number;
    quiet_until?: string;
    waiting_jobs: number;
    build_pending: boolean;
    /** Units the newest completed pass skipped (a row was held), retried at the time given. */
    skipped_units: number;
    skipped_retry_at?: string;
}

/** The entity step of the card: the newest pass and the newest report. */
export interface AuthorEntityStatus {
    run: AuthorEntityRun | null;
    report: AuthorEntityReport | null;
    wikidata?: WikidataStatus | null;
    autopilot?: AuthorAutopilotStatus | null;
}

const entityBase = '/admin/author-metadata/entities';

export const getAuthorEntityStatus = () =>
    http.get<{ status: AuthorEntityStatus }>(`${entityBase}/status`);

/** startAuthorEntityBuild answers at once; the pass goes on in the background. */
export const startAuthorEntityBuild = () =>
    http.post<{ run: AuthorEntityRun }>(`${entityBase}/build`);

/** startWikidataFetch answers at once; the fetch goes on in the background. */
export const startWikidataFetch = () =>
    http.post<{ extract: WikidataExtract }>(`${entityBase}/wikidata`);

/** setAuthorAutopilot switches the autopilot on or off. */
export const setAuthorAutopilot = (enabled: boolean) =>
    http.post<{ autopilot: AuthorAutopilotStatus }>(`${entityBase}/autopilot`, { enabled });

/** One side of a model merge decision: its entity now, a name it goes by, its books. */
export interface AuthorMergeSide {
    entity?: string;
    display_name: string;
    books: number;
}

/** A live model merge decision with what the newest pass did with it. */
export interface AuthorMergeDecision {
    id: number;
    kind: string;
    script: string;
    item_kind: string;
    outcome: string;
    created_at: string;
    /** applied, lapsed or conflict; absent until a pass read it. */
    state?: string;
    reason?: string;
    side_a: AuthorMergeSide;
    side_b: AuthorMergeSide;
}

/** An entity holding a fingerprint of a veto's side: a choice of a re-anchor. */
export interface AuthorMergeVetoEntity {
    id: string;
    display_name: string;
}

/** A live veto: a revoke keeps its pair apart. */
export interface AuthorMergeVeto {
    id: number;
    revoke_decision_id: number;
    item_kind: string;
    book_id?: number;
    replaces_veto_id?: number;
    created_at: string;
    /** anchored, unanchored or gone; absent until a pass read it. */
    state?: string;
    held_books: number;
    lost_books: number;
    gone_books: number;
    widened_fingerprints: number;
    side_a: AuthorMergeVetoEntity[];
    side_b: AuthorMergeVetoEntity[];
}

/** The model merges of the entity step. */
export interface AuthorMerges {
    decisions: AuthorMergeDecision[];
    decisions_total: number;
    vetoes: AuthorMergeVeto[];
    /** A live veto the last completed build found unanchored. */
    unanchored: boolean;
}

export const getAuthorMerges = () => http.get<{ merges: AuthorMerges }>(`${entityBase}/merges`);

/** revokeMergeDecision splits the pair at the next pass; its veto keeps the pair apart. */
export const revokeMergeDecision = (id: number) =>
    http.post<{ revoke_id: number }>(`${entityBase}/merges/decisions/${id}/revoke`);

export const retireMergeVeto = (id: number) =>
    http.post<{ veto_id: number }>(`${entityBase}/merges/vetoes/${id}/retire`);

/** reanchorMergeVeto replaces the veto by one recorded from the two entities. */
export const reanchorMergeVeto = (id: number, sideA: string, sideB: string) =>
    http.post<{ veto_id: number }>(`${entityBase}/merges/vetoes/${id}/reanchor`, {
        side_a: sideA,
        side_b: sideB,
    });

const llmBase = '/admin/author-metadata/llm';

/** The merge configuration's LLM routes (E4c): the same block, its own runs. */
export const llmMergeBase = `${llmBase}/merge`;

export const getAuthorLLMStatus = (base = llmBase) =>
    http.get<{ status: AuthorLLMStatus }>(`${base}/status`);

/** startAuthorLLMRun answers at once; the run's stages go on in the background. */
export const startAuthorLLMRun = (kind: string, mode: string, base = llmBase) =>
    http.post<{ run: AuthorLLMRun }>(`${base}/runs`, { kind, mode });

export const pauseAuthorLLMRun = (runID: number, base = llmBase) =>
    http.post<{ run: AuthorLLMRun }>(`${base}/runs/${runID}/pause`);

export const resumeAuthorLLMRun = (runID: number, base = llmBase) =>
    http.post<{ run: AuthorLLMRun }>(`${base}/runs/${runID}/resume`);

/** closeAuthorLLMRun ends a run of an older configuration, or a paused one, for good. */
export const closeAuthorLLMRun = (runID: number, base = llmBase) =>
    http.post<{ run: AuthorLLMRun }>(`${base}/runs/${runID}/close`);

export const resumeAuthorLLMParticipant = (slot: string, base = llmBase) =>
    http.post<unknown>(`${base}/participants/${encodeURIComponent(slot)}/resume`);

export const resumeAuthorLLMEndpoint = (base = llmBase) =>
    http.post<unknown>(`${base}/endpoint/resume`);

/**
 * getAuthorLLMEvalReport downloads the stored report, byte for byte: the
 * latest version, or the given one of an eval run's report.
 */
export const getAuthorLLMEvalReport = (runID: number, version?: number, base = llmBase) =>
    requestBlob(
        `${base}/eval-report/${runID}${version === undefined ? '' : `?version=${version}`}`,
    );

/**
 * recomputeAuthorLLMEvalReport computes a completed eval run's report again
 * from its stored answers with the current gates, as a new version.
 */
export const recomputeAuthorLLMEvalReport = (runID: number, base = llmBase) =>
    http.post<{ run: AuthorLLMRun }>(`${base}/eval-report/${runID}/recompute`);

/** One side of an accepted adversarial suite item, as the models saw it. */
export interface AuthorLLMAdversarialSide {
    names: string[];
    size: string;
    books: { title: string; year?: string; genres?: string[] }[];
}

/** A human label of an accepted suite item. */
export interface AuthorLLMAdjudication {
    eval_item_id: number;
    label: 'same' | 'different' | 'unclear';
    note: string;
    adjudicated_by: string;
    created_at: string;
}

/** An adversarial suite item a merge eval accepted. */
export interface AuthorLLMAdversarialItem {
    eval_item_id: number;
    stratum: string;
    a: AuthorLLMAdversarialSide;
    b: AuthorLLMAdversarialSide;
    verdicts: Record<string, string> | null;
    adjudication?: AuthorLLMAdjudication;
}

/** getAuthorLLMAdversarial lists a merge eval run's accepted suite items. */
export const getAuthorLLMAdversarial = (runID: number) =>
    http.get<{ review: { run_id: number; items: AuthorLLMAdversarialItem[] } }>(
        `${llmMergeBase}/eval-report/${runID}/adversarial`,
    );

/** adjudicateAuthorLLMItem labels an accepted suite item; recompute the report to apply it. */
export const adjudicateAuthorLLMItem = (itemID: number, label: string, note = '') =>
    http.put<unknown>(`${llmMergeBase}/adjudications/${itemID}`, { label, note });

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
