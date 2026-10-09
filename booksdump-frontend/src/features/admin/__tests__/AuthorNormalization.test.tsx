import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';

import AdminSpace from '@/features/admin/AdminPanel';
import AuthorNormalization, {
    TRACKED_RUN_KEY,
    controlsFor,
    formatItemsPerMinute,
    fullStartAllowed,
    parseBookIds,
    percent,
} from '@/features/admin/AuthorNormalization';
import {
    AUTHOR_METADATA_EXTRACTION_RETRY_CLASSES,
    AUTHOR_METADATA_LOCAL_RETRY_CLASSES,
} from '@/api/admin';
import * as adminApi from '@/api/admin';
import type { AuthorMetadataRun } from '@/api/admin';
import { ApiError } from '@/api/errors';
import i18next from 'i18next';
import enTranslation from '@/locales/en/translation.json';
import ruTranslation from '@/locales/ru/translation.json';

// A stable t: a fresh one per render would change every effect's dependencies
// on each pass and loop the fetch effects. The holder lets a test swap in a
// real i18next translator while keeping the identity stable across renders.
const { i18nHolder } = vi.hoisted(() => ({
    i18nHolder: {
        t: (key: string, opts?: unknown) => (typeof opts === 'string' ? opts : key),
        language: 'en',
    },
}));

vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: i18nHolder.t, i18n: { language: i18nHolder.language } }),
}));

vi.mock('@/api/admin', async (importOriginal) => {
    // Spread the real module first: the closed retry-class constants and type
    // guards stay the production ones, so the tests exercise the actual lists.
    const actual = await importOriginal<typeof import('@/api/admin')>();
    return {
        ...actual,
        getCurrentAuthorMetadataRun: vi.fn(),
        getLatestAuthorMetadataRun: vi.fn(),
        getAuthorMetadataRun: vi.fn(),
        startAuthorMetadataRun: vi.fn(),
        getAuthorMetadataRunReport: vi.fn(),
        pauseAuthorMetadataRun: vi.fn(),
        resumeAuthorMetadataRun: vi.fn(),
        approveAuthorMetadataFullRun: vi.fn(),
        retryAuthorMetadataRun: vi.fn(),
    };
});

// The panel mounts every admin screen; the ones under test here are the link
// row and this feature, so the other screens become named placeholders whose
// texts cannot collide with the section labels themselves.
vi.mock('@/features/admin/UsersTable', () => ({ default: () => <div>users-screen</div> }));
vi.mock('@/features/admin/InvitesTable', () => ({ default: () => <div>invites-screen</div> }));
vi.mock('@/features/admin/Duplicates', () => ({ default: () => <div>duplicates-screen</div> }));
vi.mock('@/features/admin/BookScanning', () => ({ default: () => <div>scanning-screen</div> }));
vi.mock('@/features/admin/GenreManagement', () => ({ default: () => <div>genres-screen</div> }));
vi.mock('@/features/admin/CuratedCollections/CuratedCollectionsList', () => ({
    default: () => <div>collections</div>,
}));
vi.mock('@/features/admin/CuratedCollections/CuratedCollectionDetail', () => ({
    default: () => <div>collection</div>,
}));

const api = vi.mocked(adminApi);

/** The screen now reads the address (the review tab), so it renders inside a router. */
const renderScreen = (ui: React.ReactElement) =>
    render(<MemoryRouter initialEntries={['/admin/author-normalization']}>{ui}</MemoryRouter>);

/**
 * One fixture timeline from a single anchor plus offsets: the common rules
 * forbid calendar literals anywhere in tests or fixtures.
 */
const anchorMs = Date.now();
const minutesBefore = (minutes: number) => new Date(anchorMs - minutes * 60_000).toISOString();

/** A mid-flight pilot run with every stage and credit kind represented. */
const makeRun = (overrides: Partial<AuthorMetadataRun> = {}): AuthorMetadataRun => ({
    id: 7,
    mode: 'pilot_archive',
    status: 'running',
    extractor_version: 'fb2-metadata-v1',
    normalizer_version: 'authornorm-r1',
    created_at: minutesBefore(180),
    started_at: minutesBefore(179),
    extraction_completed_at: null,
    completed_at: null,
    last_error_class: null,
    approved_for_full: false,
    stages: {
        extraction: {
            total: 100,
            done: 40,
            pending: 55,
            leased: 5,
            oldest_pending_age_s: 12,
            by_status: {
                extracted: 40,
                extracted_no_author: 0,
                already_current: 0,
                entry_missing: 0,
                invalid_fb2: 0,
                unsupported_encoding: 0,
                metadata_parse_failed: 0,
            },
            current_archive: 'fb2-2024.zip',
            items_per_minute: 12.5,
        },
        local: { total: 40, done: 30, pending: 10, leased: 0, failed: 0, oldest_pending_age_s: 3 },
        review: { open: 2, closed: 5 },
    },
    credits: {
        selected: 20,
        invalid: 1,
        review: 2,
        pending: 10,
        unresolved: { policy_not_registered: 2, normalizer_failed: 1 },
    },
    ...overrides,
});

const currentWillReturn = (run: AuthorMetadataRun | null) => {
    api.getCurrentAuthorMetadataRun.mockResolvedValue({ run });
};

/**
 * The integrated current endpoint answers only from the active slot
 * (pending/running/paused) and the latest endpoint names the most recent run of
 * any status; with latest empty, a completed run still reaches the screen
 * through the tracked id and GET /runs/:id — the secondary fallback.
 */
const trackedWillReturn = (run: AuthorMetadataRun | null) => {
    if (run !== null) {
        window.localStorage.setItem(TRACKED_RUN_KEY, String(run.id));
        api.getAuthorMetadataRun.mockResolvedValue({ run });
    }
    api.getCurrentAuthorMetadataRun.mockResolvedValue({ run: null });
};

const completedWithBacklog = makeRun({
    status: 'completed',
    extraction_completed_at: minutesBefore(120),
    completed_at: minutesBefore(60),
    stages: {
        extraction: {
            total: 100,
            done: 100,
            pending: 0,
            leased: 0,
            oldest_pending_age_s: 0,
            by_status: {
                extracted: 98,
                extracted_no_author: 1,
                already_current: 0,
                entry_missing: 1,
                invalid_fb2: 0,
                unsupported_encoding: 0,
                metadata_parse_failed: 0,
            },
            current_archive: null,
            items_per_minute: 0,
        },
        local: { total: 98, done: 98, pending: 0, leased: 0, failed: 0, oldest_pending_age_s: 0 },
        review: { open: 3, closed: 5 },
    },
    credits: {
        selected: 60,
        invalid: 2,
        review: 3,
        pending: 0,
        unresolved: { policy_not_registered: 1 },
    },
});

beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.removeItem(TRACKED_RUN_KEY);
    currentWillReturn(null);
    api.getLatestAuthorMetadataRun.mockResolvedValue({ run: null });
    api.getAuthorMetadataRunReport.mockResolvedValue({
        report: {
            ...completedWithBacklog,
            ready: false,
            not_ready_reasons: ['review_backlog'],
            duration_s: 3600,
            db_growth_bytes: 1048576,
            by_class: { initials: 12, exact: 30 },
            by_script: { Cyrl: 40, Latn: 2 },
        },
    });
});

describe('AdminPanel section', () => {
    it('exposes the section as a link with a localized accessible name and routes to the screen', async () => {
        currentWillReturn(makeRun());
        render(
            <MemoryRouter initialEntries={['/admin/author-normalization']}>
                <Routes>
                    <Route path="/admin/*" element={<AdminSpace />} />
                </Routes>
            </MemoryRouter>,
        );

        const link = screen.getByRole('link', { name: 'Author normalization' });
        expect(link).toHaveAttribute('href', '/admin/author-normalization');
        expect(link).toHaveAttribute('aria-current', 'page');

        // The routed screen is the real dashboard, not a placeholder.
        const run = await screen.findByRole('region', { name: /Current run/ });
        expect(within(run).getByText('fb2-2024.zip')).toBeInTheDocument();
        expect(screen.queryByText('users-screen')).not.toBeInTheDocument();
    });
});

describe('AuthorNormalization dashboard', () => {
    it('renders the empty state with the start form when no run exists', async () => {
        renderScreen(<AuthorNormalization />);

        expect(
            await screen.findByText('No author normalization run yet. Start one below.'),
        ).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
        expect(api.getCurrentAuthorMetadataRun).toHaveBeenCalledTimes(1);
    });

    it('renders the active run with its stages, credits and versions', async () => {
        currentWillReturn(makeRun());
        renderScreen(<AuthorNormalization />);

        const run = await screen.findByRole('region', { name: /Current run/ });
        expect(within(run).getByText('Running')).toBeInTheDocument();
        expect(within(run).getByText('Pilot archive')).toBeInTheDocument();

        // Versions, per plan RED 8 (and no tokens or cost anywhere).
        expect(within(run).getByText('fb2-metadata-v1')).toBeInTheDocument();
        expect(within(run).getByText('authornorm-r1')).toBeInTheDocument();

        const extraction = within(run).getByRole('region', { name: 'Extraction' });
        expect(within(extraction).getByText('40 / 100')).toBeInTheDocument();
        expect(within(extraction).getByText('fb2-2024.zip')).toBeInTheDocument();
        expect(within(extraction).getByText('12.5')).toBeInTheDocument();
        expect(within(extraction).getByText('extracted')).toBeInTheDocument();

        const local = within(run).getByRole('region', { name: 'Local normalization' });
        expect(within(local).getByText('30 / 40')).toBeInTheDocument();

        const review = within(run).getByRole('region', { name: 'Manual review' });
        expect(within(review).getByText('Open')).toBeInTheDocument();
        expect(within(review).getByText('2')).toBeInTheDocument();
        expect(within(review).getByText('Closed')).toBeInTheDocument();
        expect(within(review).getByText('5')).toBeInTheDocument();

        // Credits with the unresolved breakdown by closed reason.
        const credits = within(run).getByRole('region', { name: 'Credits' });
        expect(within(credits).getByText('Selected')).toBeInTheDocument();
        expect(within(credits).getByText('20')).toBeInTheDocument();
        expect(within(credits).getByText('policy_not_registered')).toBeInTheDocument();
        expect(within(credits).getByText('normalizer_failed')).toBeInTheDocument();

        expect(screen.queryByText(/token/i)).toBeNull();
        expect(screen.queryByText(/cost/i)).toBeNull();
    });

    it('shows four separate stage/queue blocks and no single overall percent', async () => {
        currentWillReturn(makeRun());
        renderScreen(<AuthorNormalization />);

        const run = await screen.findByRole('region', { name: /Current run/ });
        for (const name of ['Extraction', 'Local normalization', 'Manual review', 'Credits']) {
            expect(within(run).getByRole('region', { name })).toBeInTheDocument();
        }

        const bars = within(run).getAllByRole('progressbar');
        expect(bars).toHaveLength(3);
        expect(bars.map((bar) => bar.getAttribute('aria-label'))).toEqual([
            'Extraction',
            'Local normalization',
            'Manual review',
        ]);
        expect(bars[0]).toHaveAttribute('aria-valuenow', '40');
        expect(bars[1]).toHaveAttribute('aria-valuenow', '75');
        expect(bars[2]).toHaveAttribute('aria-valuenow', '71');

        // The one misleading number the plan forbids: a percent for the whole run.
        expect(screen.queryByRole('progressbar', { name: /overall|total/i })).toBeNull();
    });

    it('renders completed-with-review-backlog semantics: completed, and the backlog spelled out', async () => {
        trackedWillReturn(completedWithBacklog);
        renderScreen(<AuthorNormalization />);

        const run = await screen.findByRole('region', { name: /Last run/ });
        expect(within(run).getByText('Completed')).toBeInTheDocument();
        expect(screen.getByText('authorNormalization.reviewBacklog')).toBeInTheDocument();
        const review = within(run).getByRole('region', { name: 'Manual review' });
        expect(within(review).getByText('Open')).toBeInTheDocument();
        expect(within(review).getByText('3')).toBeInTheDocument();
    });

    it('does not call a run complete from extraction progress while local and review are pending', async () => {
        currentWillReturn(
            makeRun({
                stages: {
                    extraction: {
                        total: 100,
                        done: 100,
                        pending: 0,
                        leased: 0,
                        oldest_pending_age_s: 0,
                        by_status: {
                            extracted: 100,
                            extracted_no_author: 0,
                            already_current: 0,
                            entry_missing: 0,
                            invalid_fb2: 0,
                            unsupported_encoding: 0,
                            metadata_parse_failed: 0,
                        },
                        current_archive: null,
                        items_per_minute: 0,
                    },
                    local: {
                        total: 100,
                        done: 60,
                        pending: 40,
                        leased: 0,
                        failed: 0,
                        oldest_pending_age_s: 30,
                    },
                    review: { open: 4, closed: 1 },
                },
            }),
        );
        renderScreen(<AuthorNormalization />);

        // The server status is the only source of completion semantics.
        const run = await screen.findByRole('region', { name: /Current run/ });
        expect(within(run).getByText('Running')).toBeInTheDocument();
        expect(within(run).queryByText('Completed')).toBeNull();
        expect(screen.getByRole('button', { name: 'Pause' })).toBeEnabled();
        const local = within(run).getByRole('region', { name: 'Local normalization' });
        expect(within(local).getByText('60 / 100')).toBeInTheDocument();
    });

    it('renders the failed-systemic state with the closed error class and no retry', async () => {
        currentWillReturn(
            makeRun({
                status: 'failed_systemic',
                last_error_class: 'archive_unreadable',
            }),
        );
        renderScreen(<AuthorNormalization />);

        const run = await screen.findByRole('region', { name: /Last run/ });
        expect(within(run).getByText('Failed (systemic)')).toBeInTheDocument();
        expect(within(run).getByText('Last error class')).toBeInTheDocument();
        expect(
            within(run).getByText('Unreadable archive (archive_unreadable)'),
        ).toBeInTheDocument();
        // Retry semantics: the server answers 409 invalid_transition for a
        // run ended failed_systemic, so no retry is offered there.
        expect(screen.queryByRole('group', { name: 'Retry stage' })).toBeNull();
    });
});

describe('start form', () => {
    it('blocks zero, non-numeric and duplicate book IDs client-side', async () => {
        renderScreen(<AuthorNormalization />);
        await screen.findByText('No author normalization run yet. Start one below.');

        const ids = screen.getByLabelText('Book IDs');
        const start = screen.getByRole('button', { name: 'Start' });

        fireEvent.change(ids, { target: { value: '0' } });
        fireEvent.click(start);
        expect(api.startAuthorMetadataRun).not.toHaveBeenCalled();
        expect(await screen.findByText('Book IDs must be positive numbers.')).toBeInTheDocument();

        fireEvent.change(ids, { target: { value: '7, abc' } });
        fireEvent.click(start);
        expect(api.startAuthorMetadataRun).not.toHaveBeenCalled();
        expect(await screen.findByText('Book IDs must be positive numbers.')).toBeInTheDocument();

        fireEvent.change(ids, { target: { value: '5, 5' } });
        fireEvent.click(start);
        expect(api.startAuthorMetadataRun).not.toHaveBeenCalled();
        expect(await screen.findByText('Book IDs must not repeat.')).toBeInTheDocument();

        fireEvent.change(ids, { target: { value: '' } });
        fireEvent.click(start);
        expect(api.startAuthorMetadataRun).not.toHaveBeenCalled();
        expect(await screen.findByText('Enter at least one book ID.')).toBeInTheDocument();
    });

    it('starts a smoke run with the parsed unique IDs', async () => {
        api.startAuthorMetadataRun.mockResolvedValue({ run: makeRun() });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('No author normalization run yet. Start one below.');

        fireEvent.change(screen.getByLabelText('Book IDs'), { target: { value: '12, 3 5' } });
        fireEvent.click(screen.getByRole('button', { name: 'Start' }));

        await waitFor(() =>
            expect(api.startAuthorMetadataRun).toHaveBeenCalledWith({
                mode: 'smoke',
                book_ids: [12, 3, 5],
            }),
        );
        // The dashboard appears from the created run.
        expect(await screen.findByRole('region', { name: /Current run/ })).toBeInTheDocument();
    });

    it('requires an archive name for the pilot mode and sends it', async () => {
        api.startAuthorMetadataRun.mockResolvedValue({ run: makeRun() });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('No author normalization run yet. Start one below.');

        fireEvent.click(screen.getByRole('button', { name: 'Pilot archive' }));
        fireEvent.click(screen.getByRole('button', { name: 'Start' }));
        expect(api.startAuthorMetadataRun).not.toHaveBeenCalled();
        expect(await screen.findByText('Enter the archive name.')).toBeInTheDocument();

        fireEvent.change(screen.getByLabelText('Archive name'), { target: { value: 'f.zip' } });
        fireEvent.click(screen.getByRole('button', { name: 'Start' }));

        await waitFor(() =>
            expect(api.startAuthorMetadataRun).toHaveBeenCalledWith({
                mode: 'pilot_archive',
                archive: 'f.zip',
            }),
        );
    });

    it('shows the closed server error when the start is rejected', async () => {
        api.startAuthorMetadataRun.mockRejectedValue(
            new ApiError('active_run_exists', 409, { body: { error: 'active_run_exists' } }),
        );
        renderScreen(<AuthorNormalization />);
        await screen.findByText('No author normalization run yet. Start one below.');

        fireEvent.change(screen.getByLabelText('Book IDs'), { target: { value: '1' } });
        fireEvent.click(screen.getByRole('button', { name: 'Start' }));

        expect(await screen.findByRole('alert')).toHaveTextContent('A run is already active.');
    });

    it('disables starting while a run is active', async () => {
        currentWillReturn(makeRun());
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
        expect(screen.getByText('A run is already active.')).toBeInTheDocument();
    });
});

describe('full-run approval gate', () => {
    it('offers the explicit approve action on a completed pilot and keeps full start disabled', async () => {
        trackedWillReturn(completedWithBacklog);
        renderScreen(<AuthorNormalization />);

        expect(await screen.findByRole('button', { name: 'Approve full run' })).toBeEnabled();

        fireEvent.click(screen.getByRole('button', { name: 'Full catalog' }));
        expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
        expect(
            screen.getByText('A full run needs an approved completed pilot run.'),
        ).toBeInTheDocument();
    });

    it('enables the full start only after the approval lands', async () => {
        api.approveAuthorMetadataFullRun.mockResolvedValue({
            run: { ...completedWithBacklog, approved_for_full: true },
        });
        trackedWillReturn(completedWithBacklog);
        renderScreen(<AuthorNormalization />);
        await screen.findByRole('button', { name: 'Approve full run' });

        fireEvent.click(screen.getByRole('button', { name: 'Approve full run' }));
        await waitFor(() => expect(api.approveAuthorMetadataFullRun).toHaveBeenCalledWith(7));
        expect(await screen.findByText('Full run approved')).toBeInTheDocument();

        fireEvent.click(screen.getByRole('button', { name: 'Full catalog' }));
        expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    });

    it('still shows the server rejection for a full start the server does not approve', async () => {
        trackedWillReturn({ ...completedWithBacklog, approved_for_full: true });
        api.startAuthorMetadataRun.mockRejectedValue(
            new ApiError('full_run_not_approved', 409, {
                body: { error: 'full_run_not_approved' },
            }),
        );
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Completed');

        fireEvent.click(screen.getByRole('button', { name: 'Full catalog' }));
        fireEvent.click(screen.getByRole('button', { name: 'Start' }));

        expect(await screen.findByRole('alert')).toHaveTextContent('The full run is not approved.');
    });
});

describe('run controls', () => {
    it.each([
        ['running', 'Running', true, false],
        ['paused', 'Paused', false, true],
        ['pending', 'Queued', false, false],
        ['completed', 'Completed', false, false],
    ] as const)(
        'enables pause/resume by server status only (%s)',
        async (status, statusText, pauseEnabled, resumeEnabled) => {
            currentWillReturn(makeRun({ status }));
            renderScreen(<AuthorNormalization />);
            await screen.findByText(statusText);

            const pause = screen.getByRole('button', { name: 'Pause' });
            const resume = screen.getByRole('button', { name: 'Resume' });
            if (pauseEnabled) {
                expect(pause).toBeEnabled();
            } else {
                expect(pause).toBeDisabled();
            }
            if (resumeEnabled) {
                expect(resume).toBeEnabled();
            } else {
                expect(resume).toBeDisabled();
            }
            // Every server-allowed status offers the retry choice.
            expect(screen.getByRole('group', { name: 'Retry stage' })).toBeInTheDocument();
        },
    );

    it('pauses through the API and adopts the returned run', async () => {
        api.pauseAuthorMetadataRun.mockResolvedValue({ run: makeRun({ status: 'paused' }) });
        currentWillReturn(makeRun());
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        fireEvent.click(screen.getByRole('button', { name: 'Pause' }));

        await waitFor(() => expect(api.pauseAuthorMetadataRun).toHaveBeenCalledWith(7));
        expect(await screen.findByText('Paused')).toBeInTheDocument();
    });

    /** Picks a stage and a class from the retry choice the dashboard offers. */
    const chooseRetry = (stageLabel: string, errorClass: string) => {
        fireEvent.click(screen.getByRole('button', { name: stageLabel }));
        fireEvent.click(screen.getByRole('button', { name: errorClass }));
    };

    it('retries the chosen stage and class and reports reopened items', async () => {
        api.retryAuthorMetadataRun.mockResolvedValue({ reopened: 4 });
        trackedWillReturn(makeRun({ status: 'completed', last_error_class: null }));
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Completed');

        chooseRetry('Local normalization', 'normalizer_failed');
        fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

        await waitFor(() =>
            expect(api.retryAuthorMetadataRun).toHaveBeenCalledWith(7, {
                stage: 'local',
                error_class: 'normalizer_failed',
            }),
        );
        expect(await screen.findByText('authorNormalization.reopened')).toBeInTheDocument();
        await waitFor(() =>
            expect(api.getCurrentAuthorMetadataRun.mock.calls.length).toBeGreaterThanOrEqual(2),
        );
    });

    it('reports the honest zero-reopened answer with an explanation', async () => {
        api.retryAuthorMetadataRun.mockResolvedValue({ reopened: 0 });
        trackedWillReturn(makeRun({ status: 'completed', last_error_class: null }));
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Completed');

        chooseRetry('Extraction', 'invalid_fb2');
        fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

        await waitFor(() =>
            expect(api.retryAuthorMetadataRun).toHaveBeenCalledWith(7, {
                stage: 'extraction',
                error_class: 'invalid_fb2',
            }),
        );
        expect(
            await screen.findByText(
                'Nothing was reopened: either no failed row matches this stage and class, or the matching rows have already spent their attempt budget.',
            ),
        ).toBeInTheDocument();
        await waitFor(() =>
            expect(api.getCurrentAuthorMetadataRun.mock.calls.length).toBeGreaterThanOrEqual(2),
        );
    });

    it('offers no retry for an approved pilot: the approval pins it', async () => {
        trackedWillReturn(
            makeRun({
                status: 'completed',
                last_error_class: 'lease_expired',
                approved_for_full: true,
            }),
        );
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Completed');

        expect(screen.queryByRole('group', { name: 'Retry stage' })).toBeNull();
    });

    it('maps a rejected action to its closed code', async () => {
        api.approveAuthorMetadataFullRun.mockRejectedValue(
            new ApiError('already_approved', 409, { body: { error: 'already_approved' } }),
        );
        currentWillReturn(completedWithBacklog);
        renderScreen(<AuthorNormalization />);
        await screen.findByRole('button', { name: 'Approve full run' });

        fireEvent.click(screen.getByRole('button', { name: 'Approve full run' }));

        expect(await screen.findByRole('alert')).toHaveTextContent(
            'The full run is already approved.',
        );
    });
});

describe('durable status', () => {
    it('refetches the current run on refresh and renders whatever the server says', async () => {
        api.getCurrentAuthorMetadataRun
            .mockResolvedValueOnce({ run: makeRun() })
            .mockResolvedValueOnce({ run: completedWithBacklog });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

        expect(await screen.findByText('Completed')).toBeInTheDocument();
        expect(api.getCurrentAuthorMetadataRun).toHaveBeenCalledTimes(2);
    });

    it('polls the durable status while the run is active', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            currentWillReturn(makeRun());
            renderScreen(<AuthorNormalization />);
            await screen.findByText('Running');
            expect(api.getCurrentAuthorMetadataRun).toHaveBeenCalledTimes(1);

            await act(async () => {
                await vi.advanceTimersByTimeAsync(15000);
            });
            expect(api.getCurrentAuthorMetadataRun.mock.calls.length).toBeGreaterThanOrEqual(2);
        } finally {
            vi.useRealTimers();
        }
    });

    it('loads the report for a completed run and shows its aggregates', async () => {
        trackedWillReturn(completedWithBacklog);
        renderScreen(<AuthorNormalization />);

        await waitFor(() => expect(api.getAuthorMetadataRunReport).toHaveBeenCalledWith(7));
        expect(await screen.findByText('Report')).toBeInTheDocument();
        expect(screen.getByText('Not ready')).toBeInTheDocument();
        expect(screen.getByText('review_backlog')).toBeInTheDocument();
        expect(screen.getByText('Duration')).toBeInTheDocument();
        expect(screen.getByText('3600')).toBeInTheDocument();
        expect(screen.getByText('1048576')).toBeInTheDocument();
        expect(screen.getByText('initials')).toBeInTheDocument();
        expect(screen.getByText('Cyrl')).toBeInTheDocument();
    });

    it('does not fetch the report while the run is active', async () => {
        currentWillReturn(makeRun());
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        expect(api.getAuthorMetadataRunReport).not.toHaveBeenCalled();
    });
});

describe('pure helpers', () => {
    it('parses IDs separated by any mix of commas, spaces and newlines', () => {
        expect(parseBookIds('12, 3\n5')).toEqual({ ids: [12, 3, 5] });
    });

    it('rejects empty, non-numeric, zero and duplicate ID lists', () => {
        expect(parseBookIds('')).toEqual({ ids: [], error: 'empty' });
        expect(parseBookIds('abc')).toEqual({ ids: [], error: 'bad' });
        expect(parseBookIds('0')).toEqual({ ids: [], error: 'bad' });
        expect(parseBookIds('-3')).toEqual({ ids: [], error: 'bad' });
        expect(parseBookIds('5, 5')).toEqual({ ids: [], error: 'duplicate' });
        expect(parseBookIds('1')).toEqual({ ids: [1] });
    });

    it('rejects IDs outside the safe integer range instead of rounding them', () => {
        expect(parseBookIds('9007199254740991')).toEqual({ ids: [9007199254740991] });
        expect(parseBookIds('9007199254740992')).toEqual({ ids: [], error: 'unsafe' });
        expect(parseBookIds('9007199254740993')).toEqual({ ids: [], error: 'unsafe' });
        expect(parseBookIds('9223372036854775807')).toEqual({ ids: [], error: 'unsafe' });
        expect(parseBookIds('9'.repeat(400))).toEqual({ ids: [], error: 'unsafe' });
    });

    it('caps the ID list at the contract limit of 10000', () => {
        const many = Array.from({ length: 10001 }, (_, index) => index + 1).join(',');
        expect(parseBookIds(many)).toEqual({ ids: [], error: 'too_many' });
    });

    it('derives controls and the full-run gate from the run alone', () => {
        expect(controlsFor(makeRun({ status: 'running' }))).toEqual({
            pause: true,
            resume: false,
            retry: true,
            retryStages: ['extraction', 'local'],
        });
        expect(controlsFor(makeRun({ status: 'paused' }))).toEqual({
            pause: false,
            resume: true,
            retry: true,
            retryStages: ['extraction', 'local'],
        });
        // Retry semantics: the server accepts retry for pending, running,
        // paused and an unapproved completed run; the stage and class are the
        // admin's explicit choice, so every allowed status offers both.
        const bothStages = ['extraction', 'local'];
        expect(controlsFor(makeRun({ status: 'pending' }))).toEqual({
            pause: false,
            resume: false,
            retry: true,
            retryStages: bothStages,
        });
        expect(controlsFor(makeRun({ status: 'running', last_error_class: null }))).toEqual({
            pause: true,
            resume: false,
            retry: true,
            retryStages: bothStages,
        });
        expect(controlsFor(makeRun({ status: 'paused' }))).toEqual({
            pause: false,
            resume: true,
            retry: true,
            retryStages: bothStages,
        });
        expect(controlsFor(makeRun({ status: 'completed' }))).toEqual({
            pause: false,
            resume: false,
            retry: true,
            retryStages: bothStages,
        });
        // The server answers 409 invalid_transition for failed_systemic and
        // for an approved pilot (the approval pins it) — no retry offered.
        expect(
            controlsFor(makeRun({ status: 'failed_systemic', last_error_class: 'invalid_fb2' })),
        ).toEqual({ pause: false, resume: false, retry: false, retryStages: [] });
        expect(
            controlsFor(
                makeRun({
                    status: 'completed',
                    approved_for_full: true,
                }),
            ),
        ).toEqual({ pause: false, resume: false, retry: false, retryStages: [] });

        expect(fullStartAllowed(completedWithBacklog)).toBe(false);
        expect(fullStartAllowed({ ...completedWithBacklog, approved_for_full: true })).toBe(true);
        expect(fullStartAllowed(makeRun({ status: 'completed' }))).toBe(false);
        expect(fullStartAllowed(null)).toBe(false);
    });

    it('computes per-stage percents with a zero guard', () => {
        expect(percent(40, 100)).toBe(40);
        expect(percent(5, 7)).toBe(71);
        expect(percent(0, 0)).toBe(0);
    });
});

describe('closed error mapping', () => {
    /** Every code in the contract's Error codes section, with the text it must render as. */
    const CLOSED_ERROR_CODES: Record<string, string> = {
        invalid_request: 'The request body is malformed.',
        invalid_mode: 'Unknown run mode.',
        invalid_selector:
            'The mode and its selector do not match, or the selector selects no books.',
        too_many_book_ids: 'At most 10000 book IDs are allowed.',
        invalid_book_id: 'Book IDs must be positive numbers.',
        duplicate_book_id: 'Book IDs must not repeat.',
        invalid_archive: 'The archive name is invalid.',
        invalid_id: 'The run ID is invalid.',
        invalid_stage: 'The retry stage is invalid.',
        invalid_error_class: 'This error class cannot be retried for that stage.',
        run_not_found: 'The run was not found.',
        active_run_exists: 'A run is already active.',
        full_run_not_approved: 'The full run is not approved.',
        invalid_transition: 'This action is not allowed in the current state.',
        not_a_completed_pilot: 'Only a completed pilot run can be approved.',
        already_approved: 'The full run is already approved.',
        internal_error: 'The server failed to handle the request.',
        run_service_unavailable: 'The run service is not available.',
    };

    it.each(Object.entries(CLOSED_ERROR_CODES))(
        'maps the closed code %s to a localized message, never the code',
        async (code, message) => {
            api.startAuthorMetadataRun.mockRejectedValue(
                new ApiError(code, 400, { body: { error: code } }),
            );
            renderScreen(<AuthorNormalization />);
            await screen.findByText('No author normalization run yet. Start one below.');

            fireEvent.change(screen.getByLabelText('Book IDs'), { target: { value: '1' } });
            fireEvent.click(screen.getByRole('button', { name: 'Start' }));

            const alert = await screen.findByRole('alert');
            expect(alert).toHaveTextContent(message);
            expect(alert).not.toHaveTextContent(code);
        },
    );

    it('shows the generic localized failure for an unknown code, never the raw body text', async () => {
        api.startAuthorMetadataRun.mockRejectedValue(
            new ApiError('unexpected private source details', 500, {
                body: { error: 'unexpected private source details' },
            }),
        );
        renderScreen(<AuthorNormalization />);
        await screen.findByText('No author normalization run yet. Start one below.');

        fireEvent.change(screen.getByLabelText('Book IDs'), { target: { value: '1' } });
        fireEvent.click(screen.getByRole('button', { name: 'Start' }));

        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent('Action failed.');
        expect(screen.queryByText('unexpected private source details')).toBeNull();
    });

    it('carries a localized message for every closed code in both locale files', () => {
        const en = enTranslation as Record<string, string>;
        const ru = ruTranslation as Record<string, string>;
        for (const [code, message] of Object.entries(CLOSED_ERROR_CODES)) {
            expect(en[`authorNormalization.errors.${code}`]).toEqual(message);
            expect(typeof ru[`authorNormalization.errors.${code}`]).toBe('string');
            expect(ru[`authorNormalization.errors.${code}`]).not.toBe('');
        }
    });
});

describe('last error class vocabulary', () => {
    it.each([
        ['archive_unreadable', 'Unreadable archive'],
        ['database_invariant', 'Database invariant broken'],
        ['version_mismatch', 'Version mismatch'],
        ['extractor_misconfigured', 'Extractor misconfigured'],
        ['other', 'Other error'],
    ])('labels the closed value %s without hiding the code', async (code, label) => {
        currentWillReturn(makeRun({ status: 'failed_systemic', last_error_class: code }));
        renderScreen(<AuthorNormalization />);

        const run = await screen.findByRole('region', { name: /Last run/ });
        expect(within(run).getByText(`${label} (${code})`)).toBeInTheDocument();
    });

    it.each(['constructor', '__proto__', 'unexpected private source details'])(
        'renders the generic localized label for the out-of-vocabulary value %s, never the value',
        async (value) => {
            currentWillReturn(makeRun({ status: 'failed_systemic', last_error_class: value }));
            renderScreen(<AuthorNormalization />);

            const run = await screen.findByRole('region', { name: /Last run/ });
            expect(within(run).getByText('Other error')).toBeInTheDocument();
            expect(screen.queryByText(value)).toBeNull();
            expect(document.body.textContent).not.toContain(value);
        },
    );

    it('renders the Russian generic label under a real translator, never the value', async () => {
        const ru = ruTranslation as Record<string, string>;
        const instance = i18next.createInstance();
        await instance.init({
            lng: 'ru',
            fallbackLng: 'en',
            resources: { ru: { translation: ruTranslation }, en: { translation: enTranslation } },
            keySeparator: false,
            interpolation: { escapeValue: false },
        });
        const fallback = i18nHolder.t;
        i18nHolder.t = (key: string, opts?: unknown) =>
            typeof opts === 'string'
                ? instance.t(key, { defaultValue: opts })
                : instance.t(key, (opts ?? {}) as Record<string, unknown>);
        try {
            currentWillReturn(
                makeRun({
                    status: 'failed_systemic',
                    last_error_class: 'unexpected private source details',
                }),
            );
            renderScreen(<AuthorNormalization />);

            const run = await screen.findByRole('region', { name: /Последний запуск/ });
            expect(
                within(run).getByText(ru['authorNormalization.errorClass.other']),
            ).toBeInTheDocument();
            expect(document.body.textContent).not.toContain('unexpected private source details');
        } finally {
            i18nHolder.t = fallback;
        }
    });
});

describe('retry eligibility and choice', () => {
    /** The closed per-stage class lists, as selectable buttons. */
    const classButtons = (stageLabel: string) => {
        fireEvent.click(screen.getByRole('button', { name: stageLabel }));
        const group = screen.getByRole('group', { name: 'Retry error class' });
        return within(group)
            .getAllByRole('button')
            .map((button) => button.textContent?.trim());
    };

    it.each(['pending', 'running', 'paused'] as const)(
        'offers the retry choice for an active %s run (the server allows it)',
        async (status) => {
            currentWillReturn(makeRun({ status, last_error_class: 'lease_expired' }));
            renderScreen(<AuthorNormalization />);
            await screen.findByText(
                status === 'running' ? 'Running' : status === 'paused' ? 'Paused' : 'Queued',
            );

            expect(screen.getByRole('group', { name: 'Retry stage' })).toBeInTheDocument();
            expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
        },
    );

    it.each([
        ['running', 'Running'],
        ['paused', 'Paused'],
    ] as const)('sends an actual retry POST for a %s run', async (status, statusText) => {
        api.retryAuthorMetadataRun.mockResolvedValue({ reopened: 2 });
        currentWillReturn(makeRun({ status, last_error_class: null }));
        renderScreen(<AuthorNormalization />);
        await screen.findByText(statusText);

        fireEvent.click(screen.getByRole('button', { name: 'Extraction' }));
        fireEvent.click(screen.getByRole('button', { name: 'archive_unreadable' }));
        fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

        await waitFor(() =>
            expect(api.retryAuthorMetadataRun).toHaveBeenCalledWith(7, {
                stage: 'extraction',
                error_class: 'archive_unreadable',
            }),
        );
    });

    it('offers the retry choice for a completed run with no run-level error class', async () => {
        trackedWillReturn(makeRun({ status: 'completed', last_error_class: null }));
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Completed');

        expect(screen.getByRole('group', { name: 'Retry stage' })).toBeInTheDocument();
    });

    it('lists exactly the closed class list of the chosen stage', async () => {
        trackedWillReturn(makeRun({ status: 'completed', last_error_class: null }));
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Completed');

        expect(classButtons('Extraction')).toEqual([...AUTHOR_METADATA_EXTRACTION_RETRY_CLASSES]);
        expect(classButtons('Local normalization')).toEqual([
            ...AUTHOR_METADATA_LOCAL_RETRY_CLASSES,
        ]);
    });

    it('lets the admin choose a class different from the run error class', async () => {
        api.retryAuthorMetadataRun.mockResolvedValue({ reopened: 1 });
        trackedWillReturn(makeRun({ status: 'completed', last_error_class: 'invalid_fb2' }));
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Completed');

        fireEvent.click(screen.getByRole('button', { name: 'Extraction' }));
        fireEvent.click(screen.getByRole('button', { name: 'extraction_failed' }));
        fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

        await waitFor(() =>
            expect(api.retryAuthorMetadataRun).toHaveBeenCalledWith(7, {
                stage: 'extraction',
                error_class: 'extraction_failed',
            }),
        );
    });

    it('offers no retry for a failed-systemic run (the server refuses it)', async () => {
        currentWillReturn(makeRun({ status: 'failed_systemic', last_error_class: 'invalid_fb2' }));
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Failed (systemic)');

        expect(screen.queryByRole('group', { name: 'Retry stage' })).toBeNull();
    });
});

describe('initial status and superseded responses', () => {
    it('keeps Start disabled until the initial status is known', async () => {
        let release!: (value: { run: AuthorMetadataRun | null }) => void;
        const gate = new Promise<{ run: AuthorMetadataRun | null }>((resolve) => {
            release = resolve;
        });
        api.getCurrentAuthorMetadataRun.mockReturnValue(gate);
        renderScreen(<AuthorNormalization />);

        expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
        expect(screen.queryByText('No author normalization run yet. Start one below.')).toBeNull();

        await act(async () => {
            release({ run: null });
        });
        expect(
            await screen.findByText('No author normalization run yet. Start one below.'),
        ).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    });

    it('shows a load failure instead of the empty state and recovers on refresh', async () => {
        api.getCurrentAuthorMetadataRun.mockRejectedValueOnce(new Error('network down'));
        renderScreen(<AuthorNormalization />);

        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent('Failed to load the current run.');
        expect(screen.queryByText('No author normalization run yet. Start one below.')).toBeNull();
        expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();

        api.getCurrentAuthorMetadataRun.mockResolvedValueOnce({ run: null });
        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

        expect(
            await screen.findByText('No author normalization run yet. Start one below.'),
        ).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    });

    it('discards a status response superseded by a newer action response', async () => {
        const running = makeRun();
        let resolveStale!: (value: { run: AuthorMetadataRun | null }) => void;
        const staleGate = new Promise<{ run: AuthorMetadataRun | null }>((resolve) => {
            resolveStale = resolve;
        });
        api.getCurrentAuthorMetadataRun
            .mockResolvedValueOnce({ run: running })
            .mockImplementationOnce(() => staleGate);
        api.pauseAuthorMetadataRun.mockResolvedValue({ run: makeRun({ status: 'paused' }) });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        // The refresh GET is in flight when the pause response lands.
        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));
        fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
        expect(await screen.findByText('Paused')).toBeInTheDocument();

        // The older GET answers last; it must not resurrect the running state.
        await act(async () => {
            resolveStale({ run: running });
        });
        expect(screen.getByText('Paused')).toBeInTheDocument();
        expect(screen.queryByText('Running')).toBeNull();
        expect(screen.getByRole('button', { name: 'Pause' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Resume' })).toBeEnabled();
    });
});

describe('unsafe book IDs', () => {
    it('rejects an ID beyond the safe integer range instead of rounding it', async () => {
        renderScreen(<AuthorNormalization />);
        await screen.findByText('No author normalization run yet. Start one below.');

        fireEvent.change(screen.getByLabelText('Book IDs'), {
            target: { value: '9007199254740993' },
        });
        fireEvent.click(screen.getByRole('button', { name: 'Start' }));

        expect(api.startAuthorMetadataRun).not.toHaveBeenCalled();
        expect(
            await screen.findByText('Book IDs must be whole numbers up to 9007199254740991.'),
        ).toBeInTheDocument();
    });
});

describe('inherited error-map keys', () => {
    it.each(['constructor', '__proto__', 'toString', 'hasOwnProperty'])(
        'renders the generic failure for the inherited key %s, never a translation key',
        async (key) => {
            api.startAuthorMetadataRun.mockRejectedValue(
                new ApiError('private transport message', 500, { body: { error: key } }),
            );
            renderScreen(<AuthorNormalization />);
            await screen.findByText('No author normalization run yet. Start one below.');

            fireEvent.change(screen.getByLabelText('Book IDs'), { target: { value: '1' } });
            fireEvent.click(screen.getByRole('button', { name: 'Start' }));

            const alert = await screen.findByRole('alert');
            expect(alert).toHaveTextContent('Action failed.');
            expect(alert.textContent).not.toContain('authorNormalization.errors');
        },
    );

    it('keeps inherited keys off the translation path under a real translator', async () => {
        const ru = ruTranslation as Record<string, string>;
        const instance = i18next.createInstance();
        await instance.init({
            lng: 'ru',
            fallbackLng: 'en',
            resources: { ru: { translation: ruTranslation }, en: { translation: enTranslation } },
            keySeparator: false,
            interpolation: { escapeValue: false },
        });
        const fallback = i18nHolder.t;
        i18nHolder.t = (key: string, opts?: unknown) =>
            typeof opts === 'string'
                ? instance.t(key, { defaultValue: opts })
                : instance.t(key, (opts ?? {}) as Record<string, unknown>);
        try {
            api.startAuthorMetadataRun.mockRejectedValue(
                new ApiError('private transport message', 500, { body: { error: 'constructor' } }),
            );
            renderScreen(<AuthorNormalization />);
            await screen.findByText(ru['authorNormalization.emptyState']);

            fireEvent.change(screen.getByLabelText(ru['authorNormalization.form.bookIds']), {
                target: { value: '1' },
            });
            fireEvent.click(screen.getByRole('button', { name: ru['authorNormalization.start'] }));

            const alert = await screen.findByRole('alert');
            expect(alert).toHaveTextContent(ru['authorNormalization.actionError']);
            expect(alert.textContent).not.toContain('authorNormalization.errors');
        } finally {
            i18nHolder.t = fallback;
        }
    });

    it('a real Russian i18next instance translates every documented error code', async () => {
        const instance = i18next.createInstance();
        await instance.init({
            lng: 'ru',
            fallbackLng: 'en',
            resources: { ru: { translation: ruTranslation }, en: { translation: enTranslation } },
            keySeparator: false,
            interpolation: { escapeValue: false },
        });
        const en = enTranslation as Record<string, string>;
        const keys = Object.keys(en).filter((key) => key.startsWith('authorNormalization.errors.'));
        expect(keys).toHaveLength(18);
        for (const key of keys) {
            const code = key.replace('authorNormalization.errors.', '');
            const result = instance.t(key);
            expect(result).toMatch(/[А-Яа-яЁё]/);
            expect(result).not.toContain(code);
        }
    });
});

describe('received run ids outside the safe range', () => {
    it('refuses run actions on a rounded id instead of acting on it', async () => {
        // JSON.parse already rounded the wire value; the dashboard must notice.
        currentWillReturn(makeRun({ id: JSON.parse('9007199254740993') as number }));
        api.pauseAuthorMetadataRun.mockResolvedValue({
            run: makeRun({ id: JSON.parse('9007199254740993') as number, status: 'paused' }),
        });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        expect(
            await screen.findByText(
                'The run data received from the server is invalid; run actions are disabled.',
            ),
        ).toBeInTheDocument();
        const pause = screen.getByRole('button', { name: 'Pause' });
        expect(pause).toBeDisabled();
        fireEvent.click(pause);
        expect(api.pauseAuthorMetadataRun).not.toHaveBeenCalled();
        expect(screen.getByRole('button', { name: 'Resume' })).toBeDisabled();
        // Reading the durable status stays available.
        expect(screen.getByRole('button', { name: 'Refresh status' })).toBeEnabled();
    });

    it('does not request the report for an unusable id', async () => {
        currentWillReturn({
            ...completedWithBacklog,
            id: JSON.parse('9007199254740993') as number,
        });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Completed');

        await act(async () => {
            await Promise.resolve();
        });
        expect(api.getAuthorMetadataRunReport).not.toHaveBeenCalled();
    });
});

describe('unknown server status', () => {
    it('treats an unrecognized status as unknown: no mutations, no start', async () => {
        currentWillReturn(
            makeRun({
                status: 'unexpected_status' as unknown as AuthorMetadataRun['status'],
                last_error_class: 'normalizer_failed',
            }),
        );
        renderScreen(<AuthorNormalization />);

        expect(await screen.findByText('Unknown')).toBeInTheDocument();
        expect(
            screen.getByText(
                'The run status received from the server is unknown; actions are disabled until a valid status arrives.',
            ),
        ).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Pause' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Resume' })).toBeDisabled();
        expect(screen.queryByRole('button', { name: 'Retry local normalization' })).toBeNull();
        expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    });
});

describe('failing refresh after a loaded run', () => {
    it('keeps mutation controls disabled until a refresh succeeds again', async () => {
        api.getCurrentAuthorMetadataRun
            .mockResolvedValueOnce({ run: makeRun() })
            .mockRejectedValueOnce(new Error('network down'));
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');
        expect(screen.getByRole('button', { name: 'Pause' })).toBeEnabled();

        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

        expect(await screen.findByRole('alert')).toHaveTextContent(
            'Failed to load the current run.',
        );
        expect(screen.getByRole('button', { name: 'Pause' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Resume' })).toBeDisabled();
    });
});

describe('superseded GET ordering', () => {
    const deferred = () => {
        let resolve!: (value: { run: AuthorMetadataRun | null }) => void;
        let reject!: (reason?: unknown) => void;
        const promise = new Promise<{ run: AuthorMetadataRun | null }>((yes, no) => {
            resolve = yes;
            reject = no;
        });
        return { promise, resolve, reject };
    };

    it('an older GET success cannot overwrite a newer GET success', async () => {
        const older = deferred();
        const newer = deferred();
        api.getCurrentAuthorMetadataRun
            .mockResolvedValueOnce({ run: makeRun() })
            .mockReturnValueOnce(older.promise)
            .mockReturnValueOnce(newer.promise);
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));
        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));
        await act(async () => {
            newer.resolve({ run: makeRun({ status: 'paused' }) });
        });
        await screen.findByText('Paused');

        await act(async () => {
            older.resolve({ run: makeRun() });
        });
        expect(screen.getByText('Paused')).toBeInTheDocument();
    });

    it('an older GET rejection cannot overwrite a newer GET success', async () => {
        const older = deferred();
        const newer = deferred();
        api.getCurrentAuthorMetadataRun
            .mockResolvedValueOnce({ run: makeRun() })
            .mockReturnValueOnce(older.promise)
            .mockReturnValueOnce(newer.promise);
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));
        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));
        await act(async () => {
            newer.resolve({ run: makeRun({ status: 'paused' }) });
        });
        await screen.findByText('Paused');

        await act(async () => {
            older.reject(new Error('private transport detail'));
        });
        expect(screen.getByText('Paused')).toBeInTheDocument();
        expect(screen.queryByRole('alert')).toBeNull();
    });
});

describe('garbage error bodies', () => {
    it.each([
        undefined,
        null,
        '',
        'garbage',
        [],
        {},
        { error: null },
        { error: 5 },
        { error: {} },
        { message: 'private source' },
        { error: '' },
    ])('renders the generic failure for body %s and never the transport message', async (body) => {
        api.startAuthorMetadataRun.mockRejectedValue(
            new ApiError('private transport message', 500, { body }),
        );
        renderScreen(<AuthorNormalization />);
        await screen.findByText('No author normalization run yet. Start one below.');

        fireEvent.change(screen.getByLabelText('Book IDs'), { target: { value: '1' } });
        fireEvent.click(screen.getByRole('button', { name: 'Start' }));

        expect(await screen.findByRole('alert')).toHaveTextContent('Action failed.');
        expect(screen.queryByText('private transport message')).toBeNull();
    });
});

describe('retry payload pairs', () => {
    it.each([
        ['extraction', 'Extraction', 'lease_expired'],
        ['local', 'Local normalization', 'transient_database'],
    ] as const)('sends the chosen pair %s/%s', async (stage, stageLabel, errorClass) => {
        api.retryAuthorMetadataRun.mockResolvedValue({ reopened: 1 });
        trackedWillReturn(makeRun({ status: 'completed', last_error_class: null }));
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Completed');

        fireEvent.click(screen.getByRole('button', { name: stageLabel }));
        fireEvent.click(screen.getByRole('button', { name: errorClass }));
        fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

        await waitFor(() =>
            expect(api.retryAuthorMetadataRun).toHaveBeenCalledWith(7, {
                stage,
                error_class: errorClass,
            }),
        );
    });
});

describe('tracked run continuity', () => {
    it('follows the tracked run through GET /runs/:id once the active slot empties', async () => {
        const running = makeRun();
        api.getCurrentAuthorMetadataRun
            .mockResolvedValueOnce({ run: running })
            .mockResolvedValue({ run: null });
        api.getAuthorMetadataRun.mockResolvedValue({ run: completedWithBacklog });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

        await waitFor(() => expect(api.getAuthorMetadataRun).toHaveBeenCalledWith(7));
        expect(await screen.findByText('Completed')).toBeInTheDocument();
        expect(window.localStorage.getItem(TRACKED_RUN_KEY)).toBe('7');
    });

    it('tracks the id of a run it started and follows that run afterwards', async () => {
        api.startAuthorMetadataRun.mockResolvedValue({ run: makeRun({ id: 8 }) });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('No author normalization run yet. Start one below.');

        fireEvent.change(screen.getByLabelText('Book IDs'), { target: { value: '1' } });
        fireEvent.click(screen.getByRole('button', { name: 'Start' }));

        await screen.findByText('Running');
        expect(window.localStorage.getItem(TRACKED_RUN_KEY)).toBe('8');

        api.getCurrentAuthorMetadataRun.mockResolvedValue({ run: null });
        api.getAuthorMetadataRun.mockResolvedValue({
            run: makeRun({ id: 8, status: 'completed' }),
        });
        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

        await waitFor(() => expect(api.getAuthorMetadataRun).toHaveBeenCalledWith(8));
        expect(await screen.findByText('Completed')).toBeInTheDocument();
    });

    it('a newer active run replaces the tracked id', async () => {
        window.localStorage.setItem(TRACKED_RUN_KEY, '7');
        api.getCurrentAuthorMetadataRun
            .mockResolvedValueOnce({ run: makeRun({ id: 9 }) })
            .mockResolvedValue({ run: null });
        api.getAuthorMetadataRun.mockResolvedValue({
            run: makeRun({ id: 9, status: 'completed' }),
        });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');
        expect(window.localStorage.getItem(TRACKED_RUN_KEY)).toBe('9');

        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

        await waitFor(() => expect(api.getAuthorMetadataRun).toHaveBeenCalledWith(9));
        expect(await screen.findByText('Completed')).toBeInTheDocument();
    });

    it('falls back to the empty state when the tracked id no longer exists', async () => {
        window.localStorage.setItem(TRACKED_RUN_KEY, '42');
        api.getCurrentAuthorMetadataRun.mockResolvedValue({ run: null });
        api.getAuthorMetadataRun.mockRejectedValue(
            new ApiError('run_not_found', 404, { body: { error: 'run_not_found' } }),
        );
        renderScreen(<AuthorNormalization />);

        expect(
            await screen.findByText('No author normalization run yet. Start one below.'),
        ).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    });

    /**
     * Replaces the real window.localStorage with one whose chosen method
     * genuinely throws — the setupTests polyfill is a plain object, so a
     * Storage.prototype spy would never intercept it.
     */
    const installThrowingStorage = (method: 'getItem' | 'setItem') => {
        const store = window.localStorage;
        const boom = () => {
            throw new Error('storage unavailable');
        };
        Object.defineProperty(window, 'localStorage', {
            configurable: true,
            value: {
                getItem: method === 'getItem' ? boom : store.getItem.bind(store),
                setItem: method === 'setItem' ? boom : store.setItem.bind(store),
                removeItem: store.removeItem.bind(store),
                clear: store.clear.bind(store),
                key: store.key.bind(store),
                get length() {
                    return store.length;
                },
            } as Storage,
        });
        return () => {
            Object.defineProperty(window, 'localStorage', { configurable: true, value: store });
        };
    };

    it('keeps the known run through active-slot completion when the storage write fails', async () => {
        const restore = installThrowingStorage('setItem');
        try {
            expect(() => window.localStorage.setItem('probe', '1')).toThrow();
            api.getCurrentAuthorMetadataRun
                .mockResolvedValueOnce({ run: makeRun() })
                .mockResolvedValue({ run: null });
            api.getAuthorMetadataRun.mockResolvedValue({ run: completedWithBacklog });
            renderScreen(<AuthorNormalization />);
            await screen.findByText('Running');

            fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

            await waitFor(() => expect(api.getAuthorMetadataRun).toHaveBeenCalledWith(7));
            expect(await screen.findByText('Completed')).toBeInTheDocument();
        } finally {
            restore();
        }
    });

    it('keeps the known run when the storage read fails', async () => {
        api.getCurrentAuthorMetadataRun
            .mockResolvedValueOnce({ run: makeRun() })
            .mockResolvedValue({ run: null });
        api.getAuthorMetadataRun.mockResolvedValue({ run: completedWithBacklog });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        const restore = installThrowingStorage('getItem');
        try {
            expect(() => window.localStorage.getItem('probe')).toThrow();
            fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

            await waitFor(() => expect(api.getAuthorMetadataRun).toHaveBeenCalledWith(7));
            expect(await screen.findByText('Completed')).toBeInTheDocument();
        } finally {
            restore();
        }
    });

    it('remembers a completed run loaded by id before storage later becomes unavailable', async () => {
        window.localStorage.setItem(TRACKED_RUN_KEY, '7');
        api.getCurrentAuthorMetadataRun.mockResolvedValue({ run: null });
        api.getAuthorMetadataRun.mockResolvedValue({ run: completedWithBacklog });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Completed');

        const restore = installThrowingStorage('getItem');
        try {
            expect(() => window.localStorage.getItem('probe')).toThrow();
            fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

            // The memory tracker now holds the id the reload used.
            await waitFor(() => expect(api.getAuthorMetadataRun).toHaveBeenCalledTimes(2));
            expect(await screen.findByText('Completed')).toBeInTheDocument();
        } finally {
            restore();
        }
    });

    it('never looks up a rounded unsafe active-run id when its active slot later empties', async () => {
        // JSON parsing rounds 9007199254740993 to 9007199254740992.
        const unsafeId = JSON.parse('9007199254740993') as number;
        api.getCurrentAuthorMetadataRun
            .mockResolvedValueOnce({ run: makeRun({ id: unsafeId }) })
            .mockResolvedValue({ run: null });
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        // The existing invalid-data alert covers the unsafe run while shown.
        expect(
            await screen.findByText(
                'The run data received from the server is invalid; run actions are disabled.',
            ),
        ).toBeInTheDocument();

        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

        expect(
            await screen.findByText('No author normalization run yet. Start one below.'),
        ).toBeInTheDocument();
        expect(api.getAuthorMetadataRun).not.toHaveBeenCalled();
        expect(window.localStorage.getItem(TRACKED_RUN_KEY)).toBeNull();
    });

    it.each([
        [
            'a server error',
            new ApiError('internal_error', 500, { body: { error: 'internal_error' } }),
        ],
        ['a network failure', new Error('network down')],
    ])(
        'reports a retryable load error, not an empty state, when the tracked lookup fails with %s',
        async (_label, failure) => {
            window.localStorage.setItem(TRACKED_RUN_KEY, '7');
            api.getCurrentAuthorMetadataRun.mockResolvedValue({ run: null });
            api.getAuthorMetadataRun.mockRejectedValue(failure);
            renderScreen(<AuthorNormalization />);

            const alert = await screen.findByRole('alert');
            expect(alert).toHaveTextContent('Failed to load the current run.');
            expect(
                screen.queryByText('No author normalization run yet. Start one below.'),
            ).toBeNull();
            expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
        },
    );

    it('keeps the visible run on screen while the tracked lookup fails', async () => {
        api.getCurrentAuthorMetadataRun
            .mockResolvedValueOnce({ run: makeRun() })
            .mockResolvedValue({ run: null });
        api.getAuthorMetadataRun.mockRejectedValue(
            new ApiError('internal_error', 500, { body: { error: 'internal_error' } }),
        );
        renderScreen(<AuthorNormalization />);
        await screen.findByText('Running');

        fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));

        expect(await screen.findByRole('alert')).toHaveTextContent(
            'Failed to load the current run.',
        );
        expect(screen.getByText('Running')).toBeInTheDocument();
        expect(screen.queryByText('No author normalization run yet. Start one below.')).toBeNull();
    });
});

describe('latest run', () => {
    it('shows the latest run when the active slot is empty, even completed', async () => {
        api.getLatestAuthorMetadataRun.mockResolvedValue({ run: completedWithBacklog });
        renderScreen(<AuthorNormalization />);

        const run = await screen.findByRole('region', { name: /Last run/ });
        expect(within(run).getByText('Completed')).toBeInTheDocument();
        expect(api.getAuthorMetadataRun).not.toHaveBeenCalled();
    });

    it('tracks the id the latest endpoint named', async () => {
        api.getLatestAuthorMetadataRun.mockResolvedValue({ run: completedWithBacklog });
        renderScreen(<AuthorNormalization />);

        await screen.findByText('Completed');
        expect(window.localStorage.getItem(TRACKED_RUN_KEY)).toBe('7');
    });

    it('does not ask the latest endpoint while a run is active', async () => {
        currentWillReturn(makeRun());
        renderScreen(<AuthorNormalization />);

        await screen.findByRole('region', { name: /Current run/ });
        expect(api.getLatestAuthorMetadataRun).not.toHaveBeenCalled();
    });

    it('falls back to the tracked id when the latest endpoint fails', async () => {
        api.getLatestAuthorMetadataRun.mockRejectedValue(
            new ApiError('internal_error', 500, { body: { error: 'internal_error' } }),
        );
        window.localStorage.setItem(TRACKED_RUN_KEY, '9');
        api.getAuthorMetadataRun.mockResolvedValue({
            run: makeRun({ id: 9, status: 'completed', completed_at: minutesBefore(5) }),
        });
        renderScreen(<AuthorNormalization />);

        const run = await screen.findByRole('region', { name: /Last run/ });
        expect(within(run).getByText('Completed')).toBeInTheDocument();
        expect(api.getAuthorMetadataRun).toHaveBeenCalledWith(9);
    });

    it('keeps the empty state when latest and tracked are both absent', async () => {
        renderScreen(<AuthorNormalization />);

        expect(
            await screen.findByText('No author normalization run yet. Start one below.'),
        ).toBeInTheDocument();
        expect(api.getAuthorMetadataRun).not.toHaveBeenCalled();
        expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    });

    it('reports a load error when the latest endpoint fails and nothing is tracked', async () => {
        api.getLatestAuthorMetadataRun.mockRejectedValue(new Error('network down'));
        renderScreen(<AuthorNormalization />);

        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent('Failed to load the current run.');
        expect(screen.queryByText('No author normalization run yet. Start one below.')).toBeNull();
        expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    });
});

describe('extraction throughput formatting', () => {
    const withRate = (rate: number) =>
        makeRun({
            stages: {
                ...makeRun().stages,
                extraction: { ...makeRun().stages.extraction, items_per_minute: rate },
            },
        });

    it('renders at most one fraction digit, never the raw float', async () => {
        currentWillReturn(withRate(7507.850998570327));
        renderScreen(<AuthorNormalization />);

        const run = await screen.findByRole('region', { name: /Current run/ });
        const extraction = within(run).getByRole('region', { name: 'Extraction' });
        expect(within(extraction).getByText('7,507.9')).toBeInTheDocument();
        expect(within(extraction).queryByText('7507.850998570327')).toBeNull();
    });

    it('keeps zero as zero', async () => {
        currentWillReturn(withRate(0));
        renderScreen(<AuthorNormalization />);

        const run = await screen.findByRole('region', { name: /Current run/ });
        const extraction = within(run).getByRole('region', { name: 'Extraction' });
        expect(within(extraction).getByText('0')).toBeInTheDocument();
    });

    it('follows the Russian locale', async () => {
        i18nHolder.language = 'ru';
        try {
            currentWillReturn(withRate(7507.850998570327));
            renderScreen(<AuthorNormalization />);

            const run = await screen.findByRole('region', { name: /Current run/ });
            const extraction = within(run).getByRole('region', { name: 'Extraction' });
            // The exact group separator is the locale's own business; what is
            // pinned is that the figure went through ru formatting. The raw
            // textContent comparison keeps the locale's own non-breaking
            // space, which the query normalizer would fold away.
            const expected = new Intl.NumberFormat('ru', { maximumFractionDigits: 1 }).format(
                7507.850998570327,
            );
            expect(
                within(extraction).getByText((_, element) => element?.textContent === expected),
            ).toBeInTheDocument();
            expect(expected).toMatch(/507/);
        } finally {
            i18nHolder.language = 'en';
        }
    });
});

describe('formatItemsPerMinute', () => {
    it('keeps at most one fraction digit and zero as zero', () => {
        expect(formatItemsPerMinute(7507.850998570327, 'en')).toBe('7,507.9');
        expect(formatItemsPerMinute(12.5, 'en')).toBe('12.5');
        expect(formatItemsPerMinute(0, 'en')).toBe('0');
        expect(formatItemsPerMinute(0, 'ru')).toBe('0');
        expect(formatItemsPerMinute(7507.850998570327, 'ru')).toBe(
            new Intl.NumberFormat('ru', { maximumFractionDigits: 1 }).format(7507.850998570327),
        );
    });
});
