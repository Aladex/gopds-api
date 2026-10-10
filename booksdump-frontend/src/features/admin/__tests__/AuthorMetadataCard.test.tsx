import { readFileSync } from 'node:fs';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

import AuthorMetadataCard, {
    cardState,
    pilotArchive,
    runErrors,
} from '@/features/admin/AuthorMetadataCard';
import * as adminApi from '@/api/admin';
import type { AuthorMetadataReport, AuthorMetadataRun } from '@/api/admin';
import { ApiError } from '@/api/errors';
import enTranslation from '@/locales/en/translation.json';
import ruTranslation from '@/locales/ru/translation.json';

const { i18nHolder } = vi.hoisted(() => ({
    i18nHolder: {
        // A string second argument is the fallback, { defaultValue }
        // interpolates like the real translator, anything else is the key.
        t: (key: string, opts?: unknown) =>
            typeof opts === 'string'
                ? opts
                : opts &&
                    typeof opts === 'object' &&
                    typeof (opts as { defaultValue?: unknown }).defaultValue === 'string'
                  ? (opts as { defaultValue: string }).defaultValue.replace(
                        /\{\{(\w+)\}\}/g,
                        (_match: string, name: string) =>
                            String((opts as Record<string, unknown>)[name] ?? ''),
                    )
                  : key,
    },
}));

vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: i18nHolder.t }),
}));

vi.mock('@/api/admin', async (importOriginal) => {
    const actual = await importOriginal<typeof import('@/api/admin')>();
    return {
        ...actual,
        getLatestAuthorMetadataRun: vi.fn(),
        getAuthorMetadataRunReport: vi.fn(),
        startAuthorMetadataRun: vi.fn(),
        approveAuthorMetadataFullRun: vi.fn(),
        pauseAuthorMetadataRun: vi.fn(),
        resumeAuthorMetadataRun: vi.fn(),
        retryAuthorMetadataRun: vi.fn(),
        listScannedArchives: vi.fn(),
        getAuthorMetadataRunArchives: vi.fn(),
        retryAuthorMetadataArchive: vi.fn(),
        deleteAuthorMetadataArchive: vi.fn(),
    };
});

const api = vi.mocked(adminApi);

/** One anchor plus offsets: no calendar literals in fixtures. */
const anchorMs = Date.now();
const minutesBefore = (minutes: number) => new Date(anchorMs - minutes * 60_000).toISOString();

const run = (overrides: Partial<AuthorMetadataRun> = {}): AuthorMetadataRun => ({
    id: 5,
    mode: 'pilot_archive',
    status: 'completed',
    extractor_version: 'fb2-metadata-v1',
    normalizer_version: 'authornorm-local-v1',
    created_at: minutesBefore(30),
    started_at: minutesBefore(30),
    extraction_completed_at: minutesBefore(20),
    completed_at: minutesBefore(10),
    last_error_class: null,
    approved_for_full: false,
    stages: {
        extraction: {
            total: 120,
            done: 120,
            pending: 0,
            leased: 0,
            oldest_pending_age_s: 0,
            by_status: {
                extracted: 118,
                extracted_no_author: 2,
                already_current: 0,
                entry_missing: 0,
                invalid_fb2: 0,
                unsupported_encoding: 0,
                metadata_parse_failed: 0,
                archive_missing: 0,
                archive_unreadable: 0,
            },
            current_archive: null,
            items_per_minute: 0,
        },
        local: { total: 200, done: 200, pending: 0, leased: 0, failed: 0, oldest_pending_age_s: 0 },
        review: { open: 3, closed: 0 },
    },
    credits: { selected: 150, invalid: 0, review: 3, pending: 0, unresolved: {} },
    seeding: null,
    aggregates_as_of: minutesBefore(1),
    ...overrides,
});

const report = (
    r: AuthorMetadataRun,
    overrides: Partial<AuthorMetadataReport> = {},
): AuthorMetadataReport => ({
    ...r,
    ready: true,
    not_ready_reasons: [],
    duration_s: 600,
    db_growth_bytes: 3 * 1024 * 1024,
    by_class: {},
    by_script: {},
    ...overrides,
});

const withFailures = (r: AuthorMetadataRun): AuthorMetadataRun => ({
    ...r,
    stages: {
        ...r.stages,
        extraction: {
            ...r.stages.extraction,
            by_status: { ...r.stages.extraction.by_status, invalid_fb2: 2 },
        },
        local: { ...r.stages.local, failed: 1 },
    },
});

const withOneBrokenBook = (r: AuthorMetadataRun): AuthorMetadataRun => ({
    ...r,
    stages: {
        ...r.stages,
        extraction: {
            ...r.stages.extraction,
            by_status: { ...r.stages.extraction.by_status, metadata_parse_failed: 1 },
        },
    },
});

const showRun = (latest: AuthorMetadataRun | null, latestReport?: AuthorMetadataReport) => {
    api.getLatestAuthorMetadataRun.mockResolvedValue({ run: latest });
    if (latest !== null) {
        api.getAuthorMetadataRunReport.mockResolvedValue({
            report: latestReport ?? report(latest),
        });
    }
};

beforeEach(() => {
    vi.clearAllMocks();
    api.startAuthorMetadataRun.mockResolvedValue({ run: run({ status: 'running' }) });
    api.approveAuthorMetadataFullRun.mockResolvedValue({ run: run({ approved_for_full: true }) });
    api.pauseAuthorMetadataRun.mockResolvedValue({ run: run({ status: 'paused' }) });
    api.resumeAuthorMetadataRun.mockResolvedValue({ run: run({ status: 'running' }) });
    api.retryAuthorMetadataRun.mockResolvedValue({ reopened: 1 });
    api.listScannedArchives.mockResolvedValue({
        scanned_archives: [
            { name: 'big.zip', books_count: 900 },
            { name: 'empty.zip', books_count: 0 },
            { name: 'small-b.zip', books_count: 40 },
            { name: 'small-a.zip', books_count: 40 },
        ],
    });
});

describe('pure state', () => {
    it('picks the smallest non-empty archive, ties by name', () => {
        expect(
            pilotArchive([
                { name: 'b.zip', books_count: 3 },
                { name: 'a.zip', books_count: 3 },
                { name: 'c.zip', books_count: 0 },
            ]),
        ).toBe('a.zip');
        expect(pilotArchive([{ name: 'c.zip', books_count: 0 }])).toBeNull();
    });

    it('counts failed books of both stages', () => {
        expect(runErrors(run())).toBe(0);
        expect(runErrors(withFailures(run()))).toBe(3);
    });

    it('maps each run to the one thing the button does', () => {
        expect(cardState(null, null).kind).toBe('idle');
        expect(cardState(run({ status: 'running' }), null).kind).toBe('active');
        expect(cardState(run({ status: 'paused', mode: 'full' }), null).kind).toBe('active');
        expect(cardState(run(), report(run())).kind).toBe('checkReady');
        expect(cardState(run({ approved_for_full: true }), report(run())).kind).toBe('approved');
        expect(cardState(withFailures(run()), report(withFailures(run()))).kind).toBe(
            'checkNotReady',
        );
        expect(
            cardState(
                run(),
                report(run(), { ready: false, not_ready_reasons: ['credits_pending'] }),
            ).kind,
        ).toBe('checkNotReady');
        expect(cardState(run({ mode: 'full' }), report(run({ mode: 'full' }))).kind).toBe('done');
        expect(
            cardState(withFailures(run({ mode: 'full' })), report(run({ mode: 'full' }))).kind,
        ).toBe('notDone');
        // One broken book in 120 is within the 1% tolerance; three are not.
        expect(cardState(withOneBrokenBook(run()), report(run())).kind).toBe('checkReady');
        expect(
            cardState(withOneBrokenBook(run({ mode: 'full' })), report(run({ mode: 'full' }))).kind,
        ).toBe('done');
        expect(cardState(run({ mode: 'smoke' }), report(run({ mode: 'smoke' }))).kind).toBe('idle');
        expect(cardState(run({ mode: 'full', status: 'failed_systemic' }), null).kind).toBe('idle');
    });
});

describe('the card', () => {
    it('starts with a check on the smallest archive', async () => {
        showRun(null);
        render(<AuthorMetadataCard />);
        fireEvent.click(await screen.findByRole('button', { name: 'Walk the catalogue' }));
        await waitFor(() =>
            expect(api.startAuthorMetadataRun).toHaveBeenCalledWith({
                mode: 'pilot_archive',
                archive: 'small-a.zip',
            }),
        );
        expect(api.approveAuthorMetadataFullRun).not.toHaveBeenCalled();
    });

    it('after a clean check the same button approves it and walks the catalogue', async () => {
        showRun(run());
        render(<AuthorMetadataCard />);
        expect(
            await screen.findByText(/The check is clean: 120 books, no errors, 3.0 MB added/),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Walk the catalogue' }));
        await waitFor(() =>
            expect(api.startAuthorMetadataRun).toHaveBeenCalledWith({ mode: 'full' }),
        );
        expect(api.approveAuthorMetadataFullRun).toHaveBeenCalledWith(5);
        const approveOrder = api.approveAuthorMetadataFullRun.mock.invocationCallOrder[0];
        const startOrder = api.startAuthorMetadataRun.mock.invocationCallOrder[0];
        expect(approveOrder).toBeLessThan(startOrder);
    });

    it('a check with a few broken books shows their count and still walks the catalogue', async () => {
        showRun(withOneBrokenBook(run()), report(run()));
        render(<AuthorMetadataCard />);
        expect(
            await screen.findByText(/The check passed: 120 books, errors: 1, 3.0 MB added/),
        ).toBeInTheDocument();
        const walk = screen.getByRole('button', { name: 'Walk the catalogue' });
        expect(walk).toBeEnabled();
        expect(screen.getByRole('button', { name: 'Retry errors' })).toBeEnabled();
        fireEvent.click(walk);
        await waitFor(() =>
            expect(api.startAuthorMetadataRun).toHaveBeenCalledWith({ mode: 'full' }),
        );
        expect(api.approveAuthorMetadataFullRun).toHaveBeenCalledWith(5);
    });

    it('a check with errors leaves the button disabled with its reason, and retries every class', async () => {
        showRun(withFailures(run()), report(withFailures(run())));
        render(<AuthorMetadataCard />);
        expect(await screen.findByRole('button', { name: 'Walk the catalogue' })).toBeDisabled();
        expect(screen.getByText('Not finished: some books could not be read.')).toBeInTheDocument();
        expect(screen.getByText('Errors: 3')).toBeInTheDocument();

        fireEvent.click(screen.getByRole('button', { name: 'Retry errors' }));
        const expected =
            adminApi.AUTHOR_METADATA_EXTRACTION_RETRY_CLASSES.length +
            adminApi.AUTHOR_METADATA_LOCAL_RETRY_CLASSES.length;
        await waitFor(() => expect(api.retryAuthorMetadataRun).toHaveBeenCalledTimes(expected));
        for (const errorClass of adminApi.AUTHOR_METADATA_EXTRACTION_RETRY_CLASSES) {
            expect(api.retryAuthorMetadataRun).toHaveBeenCalledWith(5, {
                stage: 'extraction',
                error_class: errorClass,
            });
        }
        for (const errorClass of adminApi.AUTHOR_METADATA_LOCAL_RETRY_CLASSES) {
            expect(api.retryAuthorMetadataRun).toHaveBeenCalledWith(5, {
                stage: 'local',
                error_class: errorClass,
            });
        }
        expect(await screen.findByText(`Books queued again: ${expected}.`)).toBeInTheDocument();
    });

    it('shows progress of an active pass and pauses and resumes it', async () => {
        const running = run({
            mode: 'full',
            status: 'running',
            stages: {
                ...run().stages,
                extraction: {
                    ...run().stages.extraction,
                    total: 5000,
                    done: 1250,
                    current_archive: 'part-3.zip',
                },
            },
        });
        showRun(running);
        const { unmount } = render(<AuthorMetadataCard />);
        expect(await screen.findByText('Processed 1250 of 5000 books')).toBeInTheDocument();
        expect(screen.getByText(/part-3\.zip/)).toBeInTheDocument();
        expect(api.getAuthorMetadataRunReport).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
        await waitFor(() => expect(api.pauseAuthorMetadataRun).toHaveBeenCalledWith(5));
        unmount();

        showRun({ ...running, status: 'paused' });
        render(<AuthorMetadataCard />);
        fireEvent.click(await screen.findByRole('button', { name: 'Resume' }));
        await waitFor(() => expect(api.resumeAuthorMetadataRun).toHaveBeenCalledWith(5));
    });

    it('says done when the catalogue is done, with no button left', async () => {
        showRun(run({ mode: 'full' }));
        render(<AuthorMetadataCard />);
        expect(await screen.findByText('Done')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Walk the catalogue' })).toBeNull();
        expect(screen.queryByRole('button', { name: 'Retry errors' })).toBeNull();
    });

    it('shows no modes, versions or report', async () => {
        showRun(run());
        render(<AuthorMetadataCard />);
        await screen.findByRole('button', { name: 'Walk the catalogue' });
        for (const hidden of [
            'fb2-metadata-v1',
            'authornorm-local-v1',
            'pilot_archive',
            'smoke',
            'Report',
        ]) {
            expect(screen.queryByText(new RegExp(hidden))).toBeNull();
        }
    });

    it('keeps both locales complete', () => {
        const en = enTranslation as Record<string, string>;
        const ru = ruTranslation as Record<string, string>;
        const keys = Object.keys(en).filter((key) => key.startsWith('authorMetadataCard.'));
        expect(keys.length).toBeGreaterThan(15);
        for (const key of keys) {
            expect(ru[key], `ru ${key}`).toBeTruthy();
            expect(ru[key], `ru ${key}`).not.toBe(en[key]);
        }
        expect(ru['authorMetadataCard.walk']).toBe('Пройти каталог');
        expect(ru['authorMetadataCard.retryErrors']).toBe('Повторить ошибки');
        expect(ru['authorMetadataCard.title']).toBe('Метаданные авторов');
        expect(ru['authorMetadataCard.done']).toBe('Готово');
        // Every literal key the card uses is in both locales.
        const source = readFileSync('src/features/admin/AuthorMetadataCard.tsx', 'utf8');
        const used = [...source.matchAll(/'(authorMetadataCard\.[a-zA-Z_.]+)'/g)].map((m) => m[1]);
        expect(used.length).toBeGreaterThan(15);
        for (const key of used) {
            expect(en[key], `en ${key}`).toBeTruthy();
            expect(ru[key], `ru ${key}`).toBeTruthy();
        }
    });
});

// Review B5–B7 and the retained controls of the removed dashboard: the one
// button must never act on data it cannot trust, must not act twice on a
// state it has not confirmed, must recover from a failed read, and must find
// its way past a pilot approved for older versions.
describe('retained action safety', () => {
    // JSON parsing rounds 9007199254740993 onto 9007199254740992, another run.
    const roundedId = JSON.parse('9007199254740993') as number;

    it('refuses every action on a rounded run id, and never reads its report', async () => {
        showRun(run({ id: roundedId, status: 'running' }));
        render(<AuthorMetadataCard />);

        expect(
            await screen.findByText(
                'The run data from the server is invalid; actions are disabled.',
            ),
        ).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull();
        expect(screen.queryByRole('button', { name: 'Walk the catalogue' })).toBeNull();
        expect(screen.queryByRole('button', { name: 'Retry errors' })).toBeNull();
        expect(api.pauseAuthorMetadataRun).not.toHaveBeenCalled();
        expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled();
    });

    it('does not request the report of a completed run with an unusable id', async () => {
        showRun(withFailures(run({ id: roundedId })));
        render(<AuthorMetadataCard />);
        await screen.findByText('The run data from the server is invalid; actions are disabled.');
        await act(async () => {
            await Promise.resolve();
        });
        expect(api.getAuthorMetadataRunReport).not.toHaveBeenCalled();
        expect(screen.queryByRole('button', { name: 'Retry errors' })).toBeNull();
    });

    it('treats an unknown run status as invalid data', async () => {
        showRun(run({ status: 'unexpected_status' as unknown as AuthorMetadataRun['status'] }));
        render(<AuthorMetadataCard />);
        expect(
            await screen.findByText(
                'The run data from the server is invalid; actions are disabled.',
            ),
        ).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Walk the catalogue' })).toBeNull();
    });

    it('keeps the button disabled until the first read is known', async () => {
        let release!: (value: { run: AuthorMetadataRun | null }) => void;
        api.getLatestAuthorMetadataRun.mockReturnValue(
            new Promise((resolve) => {
                release = resolve;
            }),
        );
        render(<AuthorMetadataCard />);
        expect(screen.queryByRole('button', { name: 'Walk the catalogue' })).toBeNull();
        await act(async () => {
            release({ run: null });
        });
        expect(await screen.findByRole('button', { name: 'Walk the catalogue' })).toBeEnabled();
    });

    it('recovers from a failed first read through Refresh, with no action offered before', async () => {
        api.getLatestAuthorMetadataRun.mockRejectedValueOnce(new Error('offline'));
        render(<AuthorMetadataCard />);

        expect(await screen.findByRole('alert')).toHaveTextContent(
            'Could not load the author metadata status.',
        );
        expect(screen.queryByRole('button', { name: 'Walk the catalogue' })).toBeNull();
        api.getLatestAuthorMetadataRun.mockResolvedValue({ run: null });
        fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));

        expect(await screen.findByRole('button', { name: 'Walk the catalogue' })).toBeEnabled();
        expect(screen.queryByRole('alert')).toBeNull();
    });

    it('withdraws the controls when a later read fails, until one succeeds again', async () => {
        api.getLatestAuthorMetadataRun
            .mockResolvedValueOnce({ run: run({ status: 'running' }) })
            .mockRejectedValueOnce(new Error('offline'))
            .mockResolvedValue({ run: run({ status: 'running' }) });
        render(<AuthorMetadataCard />);
        expect(await screen.findByRole('button', { name: 'Pause' })).toBeEnabled();

        fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
        expect(await screen.findByRole('alert')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull();

        fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
        expect(await screen.findByRole('button', { name: 'Pause' })).toBeEnabled();
    });

    it('keeps the controls disabled until the read after an action settles', async () => {
        let resolveRead!: (value: { run: AuthorMetadataRun }) => void;
        api.getLatestAuthorMetadataRun
            .mockResolvedValueOnce({ run: run({ status: 'running' }) })
            .mockReturnValueOnce(
                new Promise((resolve) => {
                    resolveRead = resolve;
                }),
            );
        render(<AuthorMetadataCard />);
        const pause = await screen.findByRole('button', { name: 'Pause' });
        fireEvent.click(pause);
        await waitFor(() => expect(api.getLatestAuthorMetadataRun).toHaveBeenCalledTimes(2));

        expect(pause).toBeDisabled();
        fireEvent.click(pause);
        expect(api.pauseAuthorMetadataRun).toHaveBeenCalledTimes(1);

        await act(async () => {
            resolveRead({ run: run({ status: 'paused' }) });
        });
        expect(await screen.findByRole('button', { name: 'Resume' })).toBeEnabled();
    });

    it('starts a fresh check once the server refuses a pilot approved for older versions', async () => {
        showRun(run({ approved_for_full: true, extractor_version: 'fb2-metadata-v0' }));
        api.startAuthorMetadataRun.mockRejectedValueOnce(
            new ApiError('denied', 409, { body: { error: 'full_run_not_approved' } }),
        );
        render(<AuthorMetadataCard />);

        fireEvent.click(await screen.findByRole('button', { name: 'Walk the catalogue' }));
        expect(
            await screen.findByText(
                'The check was made by an older version. The button starts a new check.',
            ),
        ).toBeInTheDocument();
        await waitFor(() =>
            expect(screen.getByRole('button', { name: 'Walk the catalogue' })).toBeEnabled(),
        );
        fireEvent.click(screen.getByRole('button', { name: 'Walk the catalogue' }));

        await waitFor(() => expect(api.startAuthorMetadataRun).toHaveBeenCalledTimes(2));
        expect(api.startAuthorMetadataRun).toHaveBeenNthCalledWith(1, { mode: 'full' });
        expect(api.startAuthorMetadataRun).toHaveBeenLastCalledWith({
            mode: 'pilot_archive',
            archive: 'small-a.zip',
        });
    });

    it('does not approve a pilot on the report of another run', async () => {
        showRun(run(), report(run({ id: 99 })));
        render(<AuthorMetadataCard />);
        const button = await screen.findByRole('button', { name: 'Walk the catalogue' });
        expect(button).toBeDisabled();
        expect(api.approveAuthorMetadataFullRun).not.toHaveBeenCalled();
    });

    it('never renders an unrecognised not-ready reason', async () => {
        showRun(run(), report(run(), { ready: false, not_ready_reasons: ['privacy_canary_name'] }));
        render(<AuthorMetadataCard />);
        await screen.findByRole('button', { name: 'Walk the catalogue' });
        expect(document.body.textContent).not.toContain('privacy_canary_name');
        expect(document.body.textContent).toContain('the check is not ready');
    });
});

// A full run answers its start at once and is seeded in the background; a
// broken archive no longer stops the pass, and its books can be cleaned up or
// read again from the card; a paused run says why.
describe('runs that start fast and survive a broken archive', () => {
    const fullRun = (overrides: Partial<AuthorMetadataRun> = {}) =>
        run({ mode: 'full', approved_for_full: false, ...overrides });

    const withMissingArchive = (r: AuthorMetadataRun): AuthorMetadataRun => ({
        ...r,
        stages: {
            ...r.stages,
            extraction: {
                ...r.stages.extraction,
                by_status: { ...r.stages.extraction.by_status, archive_missing: 2 },
            },
        },
    });

    it('shows the preparation of a full run being seeded, with nothing to press but Refresh', async () => {
        const seeding = fullRun({
            status: 'pending',
            started_at: null,
            extraction_completed_at: null,
            completed_at: null,
            seeding: { seeded: 120000, target: 558609 },
        });
        showRun(seeding);
        render(<AuthorMetadataCard />);

        expect(await screen.findByText('Preparing: 120000 of 558609 books')).toBeInTheDocument();
        expect(screen.queryByRole('alert')).toBeNull();
        expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull();
        expect(screen.queryByRole('button', { name: 'Walk the catalogue' })).toBeNull();
        expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled();
    });

    it('a paused run says why and continues', async () => {
        showRun(
            fullRun({
                status: 'paused',
                last_error_class: 'archive_unreadable',
                completed_at: null,
            }),
        );
        render(<AuthorMetadataCard />);

        expect(
            await screen.findByText(
                'Paused: the books volume could not be read. Continue once it is back.',
            ),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Resume' }));
        await waitFor(() => expect(api.resumeAuthorMetadataRun).toHaveBeenCalledWith(5));
    });

    it('lists the problem archives and reads one again', async () => {
        const r = withMissingArchive(fullRun({ status: 'running', completed_at: null }));
        showRun(r);
        api.getAuthorMetadataRunArchives.mockResolvedValue({
            archives: [
                { archive: 'user_books.zip', books: 2, reason: 'archive_missing', deletion: null },
            ],
        });
        api.retryAuthorMetadataArchive.mockResolvedValue({ reopened: 2 });
        render(<AuthorMetadataCard />);

        expect(await screen.findByText('user_books.zip')).toBeInTheDocument();
        expect(screen.getByText('2 books · the archive is missing')).toBeInTheDocument();
        expect(api.getAuthorMetadataRunArchives).toHaveBeenCalledWith(5);

        fireEvent.click(screen.getByRole('button', { name: 'Retry user_books.zip' }));
        await waitFor(() =>
            expect(api.retryAuthorMetadataArchive).toHaveBeenCalledWith(5, 'user_books.zip'),
        );
        expect(await screen.findByText('Books queued again: 2.')).toBeInTheDocument();
    });

    it('deletes the book records of a problem archive only after a confirmation', async () => {
        const r = withMissingArchive(fullRun({ status: 'running', completed_at: null }));
        showRun(r);
        api.getAuthorMetadataRunArchives.mockResolvedValue({
            archives: [
                { archive: 'user_books.zip', books: 2, reason: 'archive_missing', deletion: null },
            ],
        });
        api.deleteAuthorMetadataArchive.mockResolvedValue({
            deletion: {
                archive: 'user_books.zip',
                books_total: 2,
                books_deleted: 0,
                status: 'pending',
            },
        });
        render(<AuthorMetadataCard />);

        fireEvent.click(
            await screen.findByRole('button', { name: 'Delete book records of user_books.zip' }),
        );
        expect(await screen.findByRole('dialog')).toBeInTheDocument();
        expect(api.deleteAuthorMetadataArchive).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', { name: 'Delete records' }));
        await waitFor(() =>
            expect(api.deleteAuthorMetadataArchive).toHaveBeenCalledWith(5, 'user_books.zip'),
        );
        expect(
            await screen.findByText('Deleting the book records of user_books.zip: 2 books.'),
        ).toBeInTheDocument();
    });

    it('does not ask for problem archives when the run found none', async () => {
        showRun(fullRun({ status: 'running', completed_at: null }));
        render(<AuthorMetadataCard />);
        await screen.findByRole('button', { name: 'Pause' });
        expect(api.getAuthorMetadataRunArchives).not.toHaveBeenCalled();
    });

    it('counts the books of broken archives as errors', () => {
        expect(runErrors(withMissingArchive(run()))).toBe(2);
    });
});

// The card keeps reading what is still on its way — a completed run's final
// figures, an archive deletion — and changes when it lands, with no click.
describe('the card follows what is still on its way', () => {
    afterEach(() => {
        vi.useRealTimers();
    });

    it('reads a completed run again until its final figures arrive', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        const done = run();
        api.getLatestAuthorMetadataRun.mockResolvedValue({ run: done });
        api.getAuthorMetadataRunReport
            .mockResolvedValueOnce({
                report: report(done, { ready: false, not_ready_reasons: ['figures_pending'] }),
            })
            .mockResolvedValue({ report: report(done) });
        render(<AuthorMetadataCard />);

        expect(await screen.findByText('Counting the final figures…')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Walk the catalogue' })).toBeNull();
        expect(screen.queryByText(/Not finished/)).toBeNull();

        await act(async () => {
            await vi.advanceTimersByTimeAsync(5000);
        });
        expect(await screen.findByRole('button', { name: 'Walk the catalogue' })).toBeEnabled();
        expect(screen.queryByText('Counting the final figures…')).toBeNull();

        const reads = api.getLatestAuthorMetadataRun.mock.calls.length;
        await act(async () => {
            await vi.advanceTimersByTimeAsync(20000);
        });
        expect(api.getLatestAuthorMetadataRun.mock.calls.length).toBe(reads);
    });

    it('shows a deletion in progress and keeps reading until the archive is gone', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        const broken = {
            ...run({ mode: 'full' }),
            stages: {
                ...run().stages,
                extraction: {
                    ...run().stages.extraction,
                    by_status: { ...run().stages.extraction.by_status, archive_missing: 1000 },
                },
            },
        };
        const clean = run({ mode: 'full' });
        api.getLatestAuthorMetadataRun
            .mockResolvedValueOnce({ run: broken })
            .mockResolvedValue({ run: clean });
        api.getAuthorMetadataRunReport.mockResolvedValue({ report: report(clean) });
        api.getAuthorMetadataRunArchives.mockResolvedValue({
            archives: [
                {
                    archive: 'user_books.zip',
                    books: 1000,
                    reason: 'archive_missing',
                    deletion: { deleted: 400, total: 1000 },
                },
            ],
        });
        render(<AuthorMetadataCard />);

        expect(await screen.findByText('Deleting: 400 of 1000')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Retry user_books.zip' })).toBeNull();
        expect(
            screen.queryByRole('button', { name: 'Delete book records of user_books.zip' }),
        ).toBeNull();

        await act(async () => {
            await vi.advanceTimersByTimeAsync(5000);
        });
        await waitFor(() => expect(screen.queryByText('user_books.zip')).toBeNull());
        expect(await screen.findByText('Done')).toBeInTheDocument();
    });
});

// The server is the gate of an approval: the card enables the button from
// the figures it shows, and a refusal on the credits as they are now is
// explained, not a generic failure.
describe('the approval is the server’s decision', () => {
    it('explains a refusal of a check that is no longer settled', async () => {
        showRun(run());
        api.approveAuthorMetadataFullRun.mockRejectedValueOnce(
            new ApiError('refused', 409, { body: { error: 'not_a_completed_pilot' } }),
        );
        render(<AuthorMetadataCard />);

        fireEvent.click(await screen.findByRole('button', { name: 'Walk the catalogue' }));
        expect(
            await screen.findByText(
                'Some authors of the check are being processed again. Approve it once they are done.',
            ),
        ).toBeInTheDocument();
        expect(api.startAuthorMetadataRun).not.toHaveBeenCalled();
    });
});
