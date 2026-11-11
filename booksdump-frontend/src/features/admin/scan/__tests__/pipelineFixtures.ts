import type {
    AuthorAutopilotStatus,
    AuthorEntityReport,
    AuthorEntityStatus,
    AuthorLLMStatus,
    AuthorMetadataRun,
} from '@/api/admin';
import type { PipelineInput } from '@/features/admin/scan/pipeline';

/*
 * The scan tab's fixtures, shared by the pipeline and source tests. Every date
 * is an offset from one anchor, never a calendar literal.
 */

export const DAY_MS = 24 * 60 * 60 * 1000;

export const ago = (now: Date, ms: number): string => new Date(now.getTime() - ms).toISOString();

export const metadataRun = (
    now: Date,
    overrides: Partial<AuthorMetadataRun> = {},
): AuthorMetadataRun => ({
    id: 3,
    mode: 'full',
    status: 'completed',
    extractor_version: 'fb2-metadata-v1',
    normalizer_version: 'authornorm-local-v1',
    created_at: ago(now, 40 * DAY_MS),
    started_at: ago(now, 40 * DAY_MS),
    extraction_completed_at: ago(now, 32 * DAY_MS),
    completed_at: ago(now, 32 * DAY_MS),
    last_error_class: null,
    approved_for_full: true,
    stages: {
        extraction: {
            total: 558_608,
            done: 558_608,
            pending: 0,
            leased: 0,
            oldest_pending_age_s: 0,
            by_status: {
                extracted: 558_000,
                extracted_no_author: 400,
                already_current: 188,
                entry_missing: 0,
                invalid_fb2: 0,
                unsupported_encoding: 0,
                metadata_parse_failed: 20,
                archive_missing: 0,
                archive_unreadable: 0,
            },
            current_archive: null,
            items_per_minute: 0,
        },
        local: {
            total: 700_000,
            done: 700_000,
            pending: 0,
            leased: 0,
            failed: 0,
            oldest_pending_age_s: 0,
        },
        review: { open: 13_023, closed: 19_982 },
    },
    credits: {
        selected: 642_138,
        invalid: 121,
        review: 33_787,
        pending: 0,
        unresolved: { llm_disagreement: 797 },
    },
    seeding: null,
    aggregates_as_of: ago(now, 60 * 60 * 1000),
    ...overrides,
});

export const llmStatus = (overrides: Partial<AuthorLLMStatus> = {}): AuthorLLMStatus => ({
    enabled: true,
    run: {
        id: 138,
        kind: 'resolve',
        mode: 'full',
        status: 'completed',
        current_config: true,
        estimate_tokens: 500_000,
        limit_tokens: 600_000,
        spent_tokens: 477_557,
        resolve: { sha256: 'x', applied: 49, total: 56 },
    },
    steps: [],
    participants: [
        { slot: 'production_a', model: 'model-a' },
        { slot: 'production_b', model: 'model-b' },
    ],
    budget_tokens: 2_500_000_000,
    spent_tokens: 1_856_780_476,
    ...overrides,
});

export const autopilot = (
    now: Date,
    overrides: Partial<AuthorAutopilotStatus> = {},
): AuthorAutopilotStatus => ({
    enabled: true,
    state: 'idle',
    resolve_inputs: 0,
    merge_enabled: true,
    merge_candidates: 0,
    last_action: 'build',
    last_action_run_id: 97,
    last_action_at: ago(now, 5 * DAY_MS),
    failures: 0,
    updated_at: ago(now, 5 * DAY_MS),
    quiet_seconds: 1800,
    waiting_jobs: 0,
    build_pending: false,
    skipped_units: 0,
    census_failures: 0,
    ...overrides,
});

/** The newest entity pass's report, whole, so the entity block can render it. */
export const entityReport = (): AuthorEntityReport => ({
    credits: 632_742,
    books: 517_439,
    fingerprints: 166_103,
    entities: 146_788,
    initials_entities: 13_935,
    sizes: [{ label: '1', entities: 91_303, books: 91_303 }],
    top: [{ id: '01a163ad-4511-71e5-91b3-7c1e4de8eb47', books: 1_296 }],
    typo_merges: 198,
    no_entity: {},
    buckets: {
        buckets: 100_261,
        books: 394_222,
        with_full_names: 12_507,
        with_full_names_books: 98_626,
        moved: 25_016,
        moved_by: { title: 10_497, series: 10_835, coauthor: 7_494 },
        conflicting: 298,
        samizdat_books: 4_088,
        samizdat_kept: 89,
        remaining: 369_206,
        emptied: 1_182,
        entities: 99_079,
    },
    ids: { inherited: 0, new: 159_604, merged: 0, orphaned: 0 },
    legacy: {
        authors: 144_101,
        entities_per_author: [{ label: '1', entities: 131_394, books: 400_206 }],
        shared_entities: 1_973,
    },
    unselected_credits: 45_273,
    units_skipped: 0,
    changes: 509_129,
    timings: { load_ms: 2_420, plan_ms: 6_620, apply_ms: 8_288 },
});

export const entityStatus = (
    now: Date,
    overrides: Partial<AuthorEntityStatus> = {},
): AuthorEntityStatus => ({
    run: {
        id: 100,
        status: 'completed',
        units_total: 10,
        units_done: 10,
        units_skipped: 0,
        changes: 3,
        created_at: ago(now, 5 * DAY_MS),
        finished_at: ago(now, 5 * DAY_MS),
    },
    report: entityReport(),
    wikidata: null,
    autopilot: autopilot(now),
    ...overrides,
});

/** Prod as it was read on the day the tab was designed: everything healthy. */
export const healthy = (now: Date): PipelineInput => ({
    scanned: [
        // Old archives keep old error counts; they must stay quiet.
        {
            name: 'fb2-119691-132107.zip',
            books_count: 0,
            errors_count: 16,
            scanned_at: ago(now, 400 * DAY_MS),
        },
        {
            name: 'fb2-200000-210000.zip',
            books_count: 0,
            errors_count: 35,
            scanned_at: ago(now, 300 * DAY_MS),
        },
        {
            name: 'fb2-822000-824013.zip',
            books_count: 1_900,
            errors_count: 0,
            scanned_at: ago(now, 38 * DAY_MS),
        },
        {
            name: 'fb2-824014-826219.zip',
            books_count: 2_034,
            errors_count: 0,
            scanned_at: ago(now, 8 * DAY_MS),
        },
    ],
    unscannedCount: 0,
    scanStatus: { is_running: false, archives_processed: 0, total_archives: 0 },
    fixScan: null,
    metadataRun: metadataRun(now),
    llmNames: llmStatus(),
    llmMerge: llmStatus({
        run: {
            id: 520,
            kind: 'merge',
            mode: 'run',
            status: 'completed',
            current_config: true,
            estimate_tokens: 20_000,
            limit_tokens: 30_000,
            spent_tokens: 18_883,
            resolve: { sha256: 'y', applied: 1, total: 3 },
        },
    }),
    entity: entityStatus(now),
    mergesUnanchored: false,
});

/**
 * The December fixture of the mocks: a new archive with 37 unreadable files,
 * and the LLM provider out of quota for nine days, so 2 316 new names wait.
 */
export const twoProblems = (now: Date): PipelineInput => {
    const base = healthy(now);
    return {
        ...base,
        scanned: [
            ...(base.scanned ?? []),
            {
                name: 'fb2-826220-828301.zip',
                books_count: 2_081,
                errors_count: 37,
                scanned_at: ago(now, 1 * DAY_MS),
            },
        ],
        metadataRun: metadataRun(now, {
            credits: {
                selected: 642_138,
                invalid: 121,
                review: 34_176,
                pending: 2_316,
                unresolved: {},
            },
            stages: {
                ...metadataRun(now).stages,
                review: { open: 13_412, closed: 19_982 },
            },
        }),
        llmNames: llmStatus({
            endpoint_paused_reason: 'quota_exhausted',
            endpoint_paused_at: ago(now, 9 * DAY_MS),
        }),
        entity: entityStatus(now, { autopilot: autopilot(now, { build_pending: true }) }),
    };
};
