import { readFileSync } from 'node:fs';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';

import AuthorEntityBlock from '@/features/admin/AuthorEntityBlock';
import * as adminApi from '@/api/admin';
import type { AuthorEntityReport, AuthorEntityRun } from '@/api/admin';
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

beforeEach(() => {
    vi.clearAllMocks();
    api.startAuthorEntityBuild.mockResolvedValue({ run: entityRun({ status: 'pending', stage: undefined }) });
});

afterEach(cleanup);

describe('AuthorEntityBlock', () => {
    it('shows the newest report: entities, buckets, evidence, the legacy comparison', async () => {
        api.getAuthorEntityStatus.mockResolvedValue({
            status: { run: entityRun({ status: 'completed', stage: 'report' }), report: report() },
        });
        render(<AuthorEntityBlock />);
        expect(
            await screen.findByText(`Entities: ${n(159604)} for ${n(517439)} books (${n(632742)} credits)`),
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
        expect(screen.getByText(`Pass: ${n(509129)} rows written, ${n(0)} refused, ${n(17)} s`)).toBeInTheDocument();
        expect(screen.getByText('01a163ad-4511-71e5-91b3-7c1e4de8eb47')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Build author entities' })).toBeEnabled();
    });

    it('shows a running pass with its progress and does not start another', async () => {
        api.getAuthorEntityStatus.mockResolvedValue({ status: { run: entityRun(), report: null } });
        render(<AuthorEntityBlock />);
        expect(await screen.findByText(`Pass 7: writing · ${n(50)} of ${n(200)}`)).toBeInTheDocument();
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
    });
});
