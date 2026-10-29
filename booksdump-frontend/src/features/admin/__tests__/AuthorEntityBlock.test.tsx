import { readFileSync } from 'node:fs';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';

import AuthorEntityBlock from '@/features/admin/AuthorEntityBlock';
import * as adminApi from '@/api/admin';
import type {
    AuthorAutopilotStatus,
    AuthorMerges,
    AuthorMergeRegistration,
    AuthorMergeVeto,
    AuthorEntityReport,
    AuthorEntityRun,
    WikidataExtract,
} from '@/api/admin';
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
        getAuthorEntityStatus: vi.fn(),
        startAuthorEntityBuild: vi.fn(),
        startWikidataFetch: vi.fn(),
        setAuthorAutopilot: vi.fn(),
        getAuthorMerges: vi.fn(),
        revokeMergeDecision: vi.fn(),
        retireMergeVeto: vi.fn(),
        reanchorMergeVeto: vi.fn(),
        registerMergeKind: vi.fn(),
        withdrawMergeRegistration: vi.fn(),
        blockMergeScript: vi.fn(),
        applyMergeEval: vi.fn(),
    };
});

const api = vi.mocked(adminApi);

const n = (value: number) => value.toLocaleString();

const entityRun = (overrides: Partial<AuthorEntityRun> = {}): AuthorEntityRun => ({
    id: 7,
    status: 'running',
    stage: 'apply',
    units_total: 200,
    units_done: 50,
    units_skipped: 0,
    changes: 0,
    created_at: new Date().toISOString(),
    ...overrides,
});

const report = (): AuthorEntityReport => ({
    credits: 632742,
    books: 517439,
    fingerprints: 166103,
    entities: 159604,
    initials_entities: 13935,
    sizes: [
        { label: '1', entities: 91303, books: 91303 },
        { label: '100+', entities: 468, books: 87617 },
    ],
    top: [{ id: '01a163ad-4511-71e5-91b3-7c1e4de8eb47', books: 1296 }],
    typo_merges: 198,
    no_entity: {},
    buckets: {
        buckets: 100261,
        books: 394222,
        with_full_names: 12507,
        with_full_names_books: 98626,
        moved: 25016,
        moved_by: { title: 10497, series: 10835, coauthor: 7494 },
        conflicting: 298,
        samizdat_books: 4088,
        samizdat_kept: 89,
        remaining: 369206,
        emptied: 1182,
        entities: 99079,
    },
    ids: { inherited: 0, new: 159604, merged: 0, orphaned: 0 },
    legacy: {
        authors: 144101,
        entities_per_author: [
            { label: '1', entities: 131394, books: 400206 },
            { label: '2', entities: 10628, books: 165876 },
            { label: '3+', entities: 2079, books: 61983 },
        ],
        shared_entities: 1973,
    },
    unselected_credits: 45273,
    units_skipped: 0,
    changes: 509129,
    timings: { load_ms: 2420, plan_ms: 6620, apply_ms: 8288 },
});

const extract = (overrides: Partial<WikidataExtract> = {}): WikidataExtract => ({
    id: 3,
    content_id: 'wd-0123456789abcdef',
    status: 'published',
    source: 'https://qlever.dev/api/wikidata',
    people: 755521,
    names: 4361015,
    works: 71801,
    started_at: new Date().toISOString(),
    chunks_done: 28,
    chunks_total: 28,
    ...overrides,
});

const wikidataReport = (): AuthorEntityReport['wikidata'] => ({
    extract: 'wd-0123456789abcdef',
    named: 31204,
    excluded: { pseudonym: 2410, initials: 388, short_name: 1702, namesake: 61 },
    groups: 2610,
    merges: { same_name: 190, cross_script: 2401 },
    script_conflicts: 17,
    shared_book: 12,
    references: 26890,
    references_withheld: 690,
});

const DAY_MS = 24 * 60 * 60 * 1000;

const time = (value: string) => new Date(value).toLocaleString();

const autopilot = (overrides: Partial<AuthorAutopilotStatus> = {}): AuthorAutopilotStatus => ({
    enabled: true,
    resolve_inputs: 0,
    failures: 0,
    updated_at: new Date().toISOString(),
    quiet_seconds: 1800,
    waiting_jobs: 0,
    build_pending: false,
    skipped_units: 0,
    ...overrides,
});

const showAutopilot = (a: AuthorAutopilotStatus) =>
    api.getAuthorEntityStatus.mockResolvedValue({
        status: { run: null, report: null, autopilot: a },
    });

const noMerges = (): AuthorMerges => ({
    decisions: [],
    decisions_total: 0,
    vetoes: [],
    unanchored: false,
    registrations: [],
});

const registration = (
    overrides: Partial<AuthorMergeRegistration> = {},
): AuthorMergeRegistration => ({
    id: 4,
    config_version: 'llm1-merge',
    kind: 'swap',
    evidence_report_id: 31,
    gate: 'judge_consensus',
    registered_at: new Date().toISOString(),
    blocked_scripts: [],
    run_id: 33,
    ...overrides,
});

const veto = (overrides: Partial<AuthorMergeVeto> = {}): AuthorMergeVeto => ({
    id: 3,
    revoke_decision_id: 10,
    item_kind: 'entity',
    created_at: new Date().toISOString(),
    state: 'unanchored',
    held_books: 1,
    lost_books: 2,
    gone_books: 0,
    widened_fingerprints: 1,
    side_a: [{ id: 'entity-a2', display_name: 'Стивен Кинг' }],
    side_b: [{ id: 'entity-b', display_name: 'Кинг Стивен' }],
    ...overrides,
});

const oneDecision = (): AuthorMerges => ({
    ...noMerges(),
    decisions: [
        {
            id: 9,
            kind: 'swap',
            script: 'Cyrl',
            item_kind: 'entity',
            outcome: 'same',
            created_at: new Date().toISOString(),
            state: 'applied',
            side_a: { display_name: 'Стивен Кинг', books: 2 },
            side_b: { display_name: 'Кинг Стивен', books: 1 },
        },
    ],
    decisions_total: 1,
});

const showMerges = (merges: AuthorMerges) => {
    api.getAuthorEntityStatus.mockResolvedValue({ status: { run: null, report: null } });
    api.getAuthorMerges.mockResolvedValue({ merges });
};

beforeEach(() => {
    vi.clearAllMocks();
    api.getAuthorMerges.mockResolvedValue({ merges: noMerges() });
    api.startWikidataFetch.mockResolvedValue({
        extract: extract({ status: 'fetching', chunks_done: 0 }),
    });
    api.startAuthorEntityBuild.mockResolvedValue({
        run: entityRun({ status: 'pending', stage: undefined }),
    });
});

afterEach(cleanup);

describe('AuthorEntityBlock', () => {
    it('shows the newest report: entities, buckets, evidence, the legacy comparison', async () => {
        api.getAuthorEntityStatus.mockResolvedValue({
            status: { run: entityRun({ status: 'completed', stage: 'report' }), report: report() },
        });
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(
                `Entities: ${n(159604)} for ${n(517439)} books (${n(632742)} credits)`,
            ),
        ).toBeInTheDocument();
        expect(
            screen.getByText(
                `Moved to the full name by evidence: ${n(25016)} books (title ${n(10497)}, series ${n(10835)}, co-author ${n(7494)}); conflicting ${n(298)}, samizdat kept ${n(89)}`,
            ),
        ).toBeInTheDocument();
        expect(
            screen.getByText(
                `Against the old catalog: ${n(144101)} authors, split into two entities ${n(10628)}, into three or more ${n(2079)}; entities gathering several old authors: ${n(1973)}`,
            ),
        ).toBeInTheDocument();
        expect(
            screen.getByText(`Pass: ${n(509129)} rows written, ${n(0)} refused, ${n(17)} s`),
        ).toBeInTheDocument();
        expect(screen.getByText('01a163ad-4511-71e5-91b3-7c1e4de8eb47')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Build author entities' })).toBeEnabled();
    });

    it('shows a running pass with its progress and does not start another', async () => {
        api.getAuthorEntityStatus.mockResolvedValue({ status: { run: entityRun(), report: null } });
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(`Pass 7: writing · ${n(50)} of ${n(200)}`),
        ).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Build author entities' })).toBeDisabled();
    });

    it('starts a pass and reads the state again', async () => {
        api.getAuthorEntityStatus.mockResolvedValue({ status: { run: null, report: null } });
        render(<AuthorEntityBlock />);
        fireEvent.click(await screen.findByRole('button', { name: 'Build author entities' }));
        await waitFor(() => expect(api.startAuthorEntityBuild).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(2));
    });

    it('says when a pass is already running', async () => {
        api.getAuthorEntityStatus.mockResolvedValue({ status: { run: null, report: null } });
        api.startAuthorEntityBuild.mockRejectedValue(
            new ApiError('conflict', 409, { body: { error: 'active_run_exists' } }),
        );
        render(<AuthorEntityBlock />);
        fireEvent.click(await screen.findByRole('button', { name: 'Build author entities' }));
        expect(await screen.findByText('A pass is already running.')).toBeInTheDocument();
    });

    it('shows the published Wikidata extract and what its evidence merged', async () => {
        api.getAuthorEntityStatus.mockResolvedValue({
            status: {
                run: entityRun({ status: 'completed', stage: 'report' }),
                report: { ...report(), wikidata: wikidataReport() },
                wikidata: { latest: extract(), published: extract() },
            },
        });
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(
                `Extract wd-0123456789abcdef: ${n(755521)} writers, ${n(4361015)} names, ${n(71801)} notable works`,
            ),
        ).toBeInTheDocument();
        expect(
            screen.getByText(
                `Wikidata wd-0123456789abcdef: ${n(2591)} merged — the same name ${n(190)}, another script ${n(2401)}; entities named by one writer's main label ${n(31204)}, writers with two or more ${n(2610)}, entities given their writer ${n(26890)} (short names beside a full one left without ${n(690)})`,
            ),
        ).toBeInTheDocument();
        expect(
            screen.getByText(
                `Kept apart: the writer has pseudonyms ${n(2410)} · initials ${n(388)} · a short name beside its full name ${n(1702)} · a namesake tag ${n(61)} · two names in one script ${n(17)} · on one book ${n(12)}`,
            ),
        ).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Fetch Wikidata' })).toBeEnabled();
    });

    it('shows a running fetch with its parts and does not start another', async () => {
        api.getAuthorEntityStatus.mockResolvedValue({
            status: {
                run: null,
                report: null,
                wikidata: {
                    latest: extract({ status: 'fetching', chunks_done: 7 }),
                    published: null,
                },
            },
        });
        render(<AuthorEntityBlock />);
        expect(await screen.findByText(`Fetch 3: ${n(7)} of ${n(28)} parts`)).toBeInTheDocument();
        expect(
            screen.getByText('No extract yet: the build goes without Wikidata.'),
        ).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Fetch Wikidata' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Build author entities' })).toBeEnabled();
    });

    it('continues a failed fetch and reads the state again', async () => {
        api.getAuthorEntityStatus.mockResolvedValue({
            status: {
                run: null,
                report: null,
                wikidata: {
                    latest: extract({
                        status: 'failed',
                        chunks_done: 5,
                        last_error: 'fetching chunk label_en: 503',
                    }),
                    published: null,
                },
            },
        });
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(
                `The fetch stopped: fetching chunk label_en: 503. The ${n(5)} parts fetched are kept.`,
            ),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Continue the fetch' }));
        await waitFor(() => expect(api.startWikidataFetch).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(2));
    });

    it('says when a fetch is already running', async () => {
        api.getAuthorEntityStatus.mockResolvedValue({
            status: { run: null, report: null, wikidata: { latest: null, published: null } },
        });
        api.startWikidataFetch.mockRejectedValue(
            new ApiError('conflict', 409, { body: { error: 'active_run_exists' } }),
        );
        render(<AuthorEntityBlock />);
        fireEvent.click(await screen.findByRole('button', { name: 'Fetch Wikidata' }));
        expect(await screen.findByText('A fetch is already running.')).toBeInTheDocument();
    });

    it('says how old the published extract is', async () => {
        const fetched = new Date(Date.now() - 3 * DAY_MS - 60_000).toISOString();
        api.getAuthorEntityStatus.mockResolvedValue({
            status: {
                run: null,
                report: null,
                wikidata: { latest: null, published: extract({ finished_at: fetched }) },
            },
        });
        render(<AuthorEntityBlock />);
        expect(await screen.findByText(`Fetched ${time(fetched)}, 3 d ago`)).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Fetch Wikidata' })).toBeEnabled();
    });

    it('shows the autopilot on and what its last check found', async () => {
        const until = new Date(Date.now() + 20 * 60_000).toISOString();
        const cases: [Partial<AuthorAutopilotStatus>, string][] = [
            [{}, 'Not checked yet.'],
            [
                { state: 'not_quiet', quiet_until: until },
                `Waiting for the scan to be quiet, until ${time(until)}.`,
            ],
            [
                { state: 'normalizing', waiting_jobs: 1250 },
                `Local normalization is at work (${n(1250)} names waiting).`,
            ],
            [{ state: 'llm_run_active', state_run_id: 57 }, 'Waiting for LLM run 57 to end.'],
            [
                { state: 'entity_build_active', state_run_id: 9 },
                'Waiting for entity pass 9 to end.',
            ],
            [
                { state: 'resolve_started', state_run_id: 31, resolve_inputs: 40 },
                `Started resolve run 31 for ${n(40)} names.`,
            ],
            [{ state: 'build_started', state_run_id: 12 }, 'Started entity pass 12.'],
            [{ state: 'idle' }, 'Nothing to do.'],
        ];
        for (const [overrides, line] of cases) {
            showAutopilot(autopilot(overrides));
            render(<AuthorEntityBlock />);
            expect(await screen.findByText(line)).toBeInTheDocument();
            expect(screen.getByRole('switch', { name: 'Autopilot after a scan' })).toBeChecked();
            cleanup();
        }
    });

    it('says how the resolve check came out, a pass due and the last run it started', async () => {
        const at = new Date(Date.now() - 60_000).toISOString();
        showAutopilot(
            autopilot({
                state: 'idle',
                resolve_note: 'no_resolve_pilot',
                build_pending: true,
                last_action: 'build',
                last_action_run_id: 12,
                last_action_at: at,
            }),
        );
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(
                'Resolve: the resolve pilot has not run under this configuration.',
            ),
        ).toBeInTheDocument();
        expect(
            screen.getByText('The selections changed since the last pass: a pass is due.'),
        ).toBeInTheDocument();
        expect(screen.getByText(`Last started: entity pass 12, ${time(at)}.`)).toBeInTheDocument();
        cleanup();

        showAutopilot(
            autopilot({
                state: 'resolve_started',
                state_run_id: 31,
                resolve_note: 'started',
                build_pending: true,
                last_action: 'resolve',
                last_action_run_id: 31,
                last_action_at: at,
            }),
        );
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(`Last started: resolve run 31, ${time(at)}.`),
        ).toBeInTheDocument();
        expect(screen.queryByText(/^Resolve:/)).toBeNull();
        cleanup();

        showAutopilot(autopilot({ state: 'build_started', state_run_id: 13, build_pending: true }));
        render(<AuthorEntityBlock />);
        await screen.findByText('Started entity pass 13.');
        expect(screen.queryByText(/a pass is due/)).toBeNull();
    });

    it('says a pass skipped units and when they are retried', async () => {
        const retry = new Date(Date.now() + 300_000).toISOString();
        showAutopilot(autopilot({ state: 'idle', skipped_units: 2, skipped_retry_at: retry }));
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(
                `The last pass skipped ${n(2)} units whose rows were held; another pass is due ${time(retry)}.`,
            ),
        ).toBeInTheDocument();
        cleanup();

        showAutopilot(
            autopilot({
                state: 'entity_build_active',
                state_run_id: 5,
                skipped_units: 2,
                skipped_retry_at: retry,
            }),
        );
        render(<AuthorEntityBlock />);
        await screen.findByText('Waiting for entity pass 5 to end.');
        expect(screen.queryByText(/skipped/)).toBeNull();
    });

    it('shows a failed check with its next attempt', async () => {
        const next = new Date(Date.now() + 120_000).toISOString();
        showAutopilot(
            autopilot({
                state: 'idle',
                failures: 2,
                last_error_class: 'sqlstate_40p01',
                next_attempt_at: next,
            }),
        );
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(
                `The last check failed 2 times in a row (sqlstate_40p01); next attempt ${time(next)}.`,
            ),
        ).toBeInTheDocument();
    });

    it('re-reads every 15 s while the autopilot is on, so what it starts shows up', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            const next = async (ms: number) => {
                await act(async () => {
                    await vi.advanceTimersByTimeAsync(ms);
                });
            };
            showAutopilot(autopilot({ state: 'idle' }));
            render(<AuthorEntityBlock />);
            await screen.findByText('Nothing to do.');

            showAutopilot(autopilot({ state: 'normalizing', waiting_jobs: 3 }));
            await next(15000);
            expect(
                screen.getByText(`Local normalization is at work (${n(3)} names waiting).`),
            ).toBeInTheDocument();

            showAutopilot(
                autopilot({ state: 'resolve_started', state_run_id: 31, resolve_inputs: 2 }),
            );
            await next(15000);
            expect(
                screen.getByText(`Started resolve run 31 for ${n(2)} names.`),
            ).toBeInTheDocument();

            showAutopilot(autopilot({ state: 'build_started', state_run_id: 12 }));
            await next(15000);
            expect(screen.getByText('Started entity pass 12.')).toBeInTheDocument();

            showAutopilot(autopilot({ state: 'idle' }));
            await next(10000);
            expect(screen.getByText('Started entity pass 12.')).toBeInTheDocument();
            await next(5000);
            expect(screen.getByText('Nothing to do.')).toBeInTheDocument();
        } finally {
            vi.useRealTimers();
        }
    });

    it('does not re-read while the autopilot is off and nothing runs', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            showAutopilot(autopilot({ enabled: false, state: 'not_quiet' }));
            render(<AuthorEntityBlock />);
            await screen.findByText(
                'Off: new books wait for the buttons; a run it started goes on.',
            );
            expect(screen.queryByText(/Waiting for the scan/)).toBeNull();
            expect(
                screen.getByRole('switch', { name: 'Autopilot after a scan' }),
            ).not.toBeChecked();
            await act(async () => {
                await vi.advanceTimersByTimeAsync(60000);
            });
            expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(1);
        } finally {
            vi.useRealTimers();
        }
    });

    it('switches the autopilot off and reads the state again', async () => {
        showAutopilot(autopilot());
        api.setAuthorAutopilot.mockResolvedValue({ autopilot: autopilot({ enabled: false }) });
        render(<AuthorEntityBlock />);
        fireEvent.click(await screen.findByRole('switch', { name: 'Autopilot after a scan' }));
        await waitFor(() => expect(api.setAuthorAutopilot).toHaveBeenCalledWith(false));
        await waitFor(() => expect(api.getAuthorEntityStatus).toHaveBeenCalledTimes(2));
    });

    it('says there is no merge decision yet', async () => {
        showMerges(noMerges());
        render(<AuthorEntityBlock />);
        expect(await screen.findByText('No merge decision yet.')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Undo merge' })).not.toBeInTheDocument();
    });

    it('lists a decision and undoes it once confirmed, then reads again', async () => {
        showMerges(oneDecision());
        api.revokeMergeDecision.mockResolvedValue({ revoke_id: 10 });
        const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(
                `9: Стивен Кинг (${n(2)}) and Кинг Стивен (${n(1)}) · swapped name parts · merged`,
            ),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Undo merge' }));
        expect(confirm).toHaveBeenCalledTimes(1);
        expect(api.revokeMergeDecision).not.toHaveBeenCalled();
        confirm.mockReturnValue(true);
        fireEvent.click(screen.getByRole('button', { name: 'Undo merge' }));
        await waitFor(() => expect(api.revokeMergeDecision).toHaveBeenCalledWith(9));
        await waitFor(() => expect(api.getAuthorMerges).toHaveBeenCalledTimes(2));
        confirm.mockRestore();
    });

    it('shows an unanchored veto, retires it once confirmed and re-anchors it', async () => {
        showMerges({ ...noMerges(), vetoes: [veto()], unanchored: true });
        api.retireMergeVeto.mockResolvedValue({ veto_id: 3 });
        api.reanchorMergeVeto.mockResolvedValue({ veto_id: 4 });
        const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(
                'A veto lost its books (re-extracted with other data): no question goes to the model until it is re-anchored or retired.',
            ),
        ).toBeInTheDocument();
        expect(
            screen.getByText(
                `Veto 3: lost its books · books held ${n(1)}, lost ${n(2)}, deleted ${n(0)}; fingerprints added ${n(1)}`,
            ),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Retire veto' }));
        await waitFor(() => expect(api.retireMergeVeto).toHaveBeenCalledWith(3));
        fireEvent.click(await screen.findByRole('button', { name: 'Re-anchor veto' }));
        fireEvent.click(await screen.findByRole('button', { name: 'Re-anchor on these two' }));
        await waitFor(() =>
            expect(api.reanchorMergeVeto).toHaveBeenCalledWith(3, 'entity-a2', 'entity-b'),
        );
        confirm.mockRestore();
    });

    it('says why a re-anchor was refused', async () => {
        showMerges({ ...noMerges(), vetoes: [veto()] });
        api.reanchorMergeVeto.mockRejectedValue(
            new ApiError('unprocessable', 422, { body: { error: 'veto_sides_invalid' } }),
        );
        render(<AuthorEntityBlock />);
        fireEvent.click(await screen.findByRole('button', { name: 'Re-anchor veto' }));
        fireEvent.click(await screen.findByRole('button', { name: 'Re-anchor on these two' }));
        expect(
            await screen.findByText('The chosen entities are not the two sides of the veto.'),
        ).toBeInTheDocument();
    });

    it('says why a decision is out of date in words, not codes', async () => {
        const merges = oneDecision();
        merges.decisions[0] = { ...merges.decisions[0], state: 'lapsed', reason: 'withdrawn' };
        showMerges(merges);
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(
                `9: Стивен Кинг (${n(2)}) and Кинг Стивен (${n(1)}) · swapped name parts · out of date (the kind is withdrawn)`,
            ),
        ).toBeInTheDocument();
    });

    it('lists a registration, applies its check and says what the apply did', async () => {
        showMerges({ ...noMerges(), registrations: [registration({ blocked_scripts: ['Latn'] })] });
        api.applyMergeEval.mockResolvedValue({
            apply: {
                registration_id: 4,
                kind: 'swap',
                run_id: 33,
                report_id: 31,
                items: 1081,
                accepted: 900,
                inserted: 890,
                lapsed_before_apply: 10,
                already_decided: 0,
                build_run_id: 40,
            },
        });
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText('swapped name parts: check run 33, report 31 · blocked: Latn'),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Apply the check' }));
        await waitFor(() => expect(api.applyMergeEval).toHaveBeenCalledWith(4));
        expect(
            await screen.findByText(
                `swapped name parts: ${n(890)} merges added of ${n(900)} accepted (${n(1081)} asked); out of date ${n(10)}, already there ${n(0)}`,
            ),
        ).toBeInTheDocument();
        await waitFor(() => expect(api.getAuthorMerges).toHaveBeenCalledTimes(2));
    });

    it('blocks a script and withdraws a kind once confirmed', async () => {
        showMerges({ ...noMerges(), registrations: [registration()] });
        api.blockMergeScript.mockResolvedValue({ registration_id: 4 });
        api.withdrawMergeRegistration.mockResolvedValue({ registration_id: 4 });
        const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
        render(<AuthorEntityBlock />);
        fireEvent.click(await screen.findByRole('button', { name: 'Withdraw' }));
        expect(api.withdrawMergeRegistration).not.toHaveBeenCalled();
        confirm.mockReturnValue(true);
        fireEvent.click(screen.getByRole('button', { name: 'Block script' }));
        await waitFor(() => expect(api.blockMergeScript).toHaveBeenCalledWith(4, 'Cyrl'));
        fireEvent.click(await screen.findByRole('button', { name: 'Withdraw' }));
        await waitFor(() => expect(api.withdrawMergeRegistration).toHaveBeenCalledWith(4));
        confirm.mockRestore();
    });

    it('offers nothing on a withdrawn registration', async () => {
        showMerges({
            ...noMerges(),
            registrations: [registration({ withdrawn_at: new Date().toISOString() })],
        });
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText('swapped name parts: check run 33, report 31 · withdrawn'),
        ).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Apply the check' })).not.toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Withdraw' })).not.toBeInTheDocument();
    });

    it('registers a kind from a run and says why one was refused', async () => {
        showMerges(noMerges());
        api.registerMergeKind.mockRejectedValue(
            new ApiError('conflict', 409, { body: { error: 'merge_kind_not_passed' } }),
        );
        render(<AuthorEntityBlock />);
        const register = await screen.findByRole('button', { name: 'Register' });
        expect(register).toBeDisabled();
        fireEvent.change(screen.getByRole('textbox', { name: 'Check run' }), {
            target: { value: '33' },
        });
        fireEvent.click(register);
        await waitFor(() => expect(api.registerMergeKind).toHaveBeenCalledWith(33, 'swap'));
        expect(await screen.findByText('The report did not pass this kind.')).toBeInTheDocument();
    });

    it('keeps both locales complete', () => {
        const en = enTranslation as Record<string, string>;
        const ru = ruTranslation as Record<string, string>;
        const source = readFileSync('src/features/admin/AuthorEntityBlock.tsx', 'utf8');
        const used = [...source.matchAll(/'(authorEntity\.[a-zA-Z_.]+)'/g)].map((m) => m[1]);
        expect(used.length).toBeGreaterThan(15);
        for (const key of used) {
            expect(en[key], `en ${key}`).toBeTruthy();
            expect(ru[key], `ru ${key}`).toBeTruthy();
            if (/[A-Za-z]/.test(en[key].replace(/\{\{\w+\}\}/g, ''))) {
                expect(ru[key], `ru ${key}`).not.toBe(en[key]);
            }
        }
        expect(ru['authorEntity.build']).toBe('Собрать сущности авторов');
        expect(ru['authorEntity.wikidata.fetch']).toBe('Загрузить Wikidata');
        expect(ru['authorEntity.autopilot.title']).toBe('Автопилот после сканирования');
        for (const key of Object.keys(en).filter((k) => k.startsWith('authorEntity.autopilot.'))) {
            expect(used, `${key} is unused`).toContain(key);
        }
    });
});
