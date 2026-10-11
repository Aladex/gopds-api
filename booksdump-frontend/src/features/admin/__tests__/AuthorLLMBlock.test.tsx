import { readFileSync } from 'node:fs';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';

import AuthorLLMBlock from '@/features/admin/AuthorLLMBlock';
import * as adminApi from '@/api/admin';
import type {
    AuthorLLMParticipant,
    AuthorLLMRun,
    AuthorLLMStatus,
    AuthorLLMStep,
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
        getAuthorLLMStatus: vi.fn(),
        startAuthorLLMRun: vi.fn(),
        pauseAuthorLLMRun: vi.fn(),
        resumeAuthorLLMRun: vi.fn(),
        closeAuthorLLMRun: vi.fn(),
        resumeAuthorLLMParticipant: vi.fn(),
        resumeAuthorLLMEndpoint: vi.fn(),
        getAuthorLLMEvalReport: vi.fn(),
    };
});

const api = vi.mocked(adminApi);

const n = (value: number) => value.toLocaleString();

const step = (key: AuthorLLMStep['key'], overrides: Partial<AuthorLLMStep> = {}): AuthorLLMStep => {
    const eval_ = key.startsWith('eval');
    // Swaps are the one step not built yet.
    const built = key !== 'swap';
    return {
        key,
        kind: eval_ ? 'eval' : key === 'swap' ? 'swap' : 'resolve',
        mode: key.endsWith('pilot') ? 'pilot' : 'full',
        available: built,
        enabled: false,
        done: false,
        blocked: built ? 'prerequisite' : 'not_available',
        ...overrides,
    };
};

const freshSteps = (): AuthorLLMStep[] => [
    step('eval_pilot', { enabled: true, blocked: undefined }),
    step('eval_full'),
    step('resolve_pilot'),
    step('resolve_full'),
    step('swap'),
];

const participants = (): AuthorLLMParticipant[] => [
    { slot: 'production_a', model: 'gpt-6-luna' },
    { slot: 'production_b', model: 'deepseek-v4-pro' },
    { slot: 'judge_a', model: 'gpt-6-sol' },
    { slot: 'judge_b', model: 'kimi-k3' },
];

const status = (overrides: Partial<AuthorLLMStatus> = {}): AuthorLLMStatus => ({
    enabled: true,
    run: null,
    steps: freshSteps(),
    participants: participants(),
    budget_tokens: 900_000_000,
    spent_tokens: 0,
    ...overrides,
});

const llmRun = (overrides: Partial<AuthorLLMRun> = {}): AuthorLLMRun => ({
    id: 41,
    kind: 'eval',
    mode: 'pilot',
    status: 'running',
    stage: 'judge',
    current_config: true,
    progress: { done: 8200, total: 13400 },
    estimate_tokens: 205_000_000,
    limit_tokens: 307_500_000,
    spent_tokens: 70_000_000,
    ...overrides,
});

const show = (st: AuthorLLMStatus) => api.getAuthorLLMStatus.mockResolvedValue({ status: st });

beforeEach(() => {
    vi.clearAllMocks();
    api.startAuthorLLMRun.mockResolvedValue({
        run: llmRun({ stage: 'freeze', progress: undefined }),
    });
    api.pauseAuthorLLMRun.mockResolvedValue({ run: llmRun({ status: 'paused' }) });
    api.resumeAuthorLLMRun.mockResolvedValue({ run: llmRun() });
    api.closeAuthorLLMRun.mockResolvedValue({ run: llmRun({ status: 'closed' }) });
    api.resumeAuthorLLMParticipant.mockResolvedValue(undefined);
    api.resumeAuthorLLMEndpoint.mockResolvedValue(undefined);
});

describe('AuthorLLMBlock', () => {
    it('is one line without buttons when the LLM is not configured', async () => {
        show(status({ enabled: false, reason: 'llm_not_configured', participants: null }));
        render(<AuthorLLMBlock />);
        expect(await screen.findByText('LLM is not configured')).toBeInTheDocument();
        expect(screen.queryAllByRole('button')).toHaveLength(0);
    });

    it('shows the whole chain with only the next step enabled', async () => {
        show(status());
        render(<AuthorLLMBlock />);
        const check = await screen.findByRole('button', { name: /Check quality/ });
        expect(check).toBeEnabled();
        expect(screen.getByRole('button', { name: /Check everything/ })).toBeDisabled();
        expect(screen.getByRole('button', { name: /Resolve: pilot 500/ })).toBeDisabled();
        expect(screen.getByRole('button', { name: /Resolve everything/ })).toBeDisabled();
        expect(screen.getByRole('button', { name: /Swaps/ })).toBeDisabled();
        expect(screen.getByText('Swaps come in the next version.')).toBeInTheDocument();

        fireEvent.click(check);
        await waitFor(() => expect(api.startAuthorLLMRun).toHaveBeenCalledWith('eval', 'pilot'));
        await waitFor(() => expect(api.getAuthorLLMStatus).toHaveBeenCalledTimes(2));
    });

    it('marks done steps and opens the next one', async () => {
        show(
            status({
                steps: [
                    step('eval_pilot', { enabled: true, done: true, blocked: undefined }),
                    step('eval_full', { enabled: true, blocked: undefined }),
                    step('resolve_pilot'),
                    step('resolve_full'),
                    step('swap'),
                ],
            }),
        );
        render(<AuthorLLMBlock />);
        const full = await screen.findByRole('button', { name: /Check everything/ });
        expect(full).toBeEnabled();
        expect(screen.getByRole('button', { name: /✓ Check quality/ })).toBeInTheDocument();
        fireEvent.click(full);
        await waitFor(() => expect(api.startAuthorLLMRun).toHaveBeenCalledWith('eval', 'full'));
    });

    it('shows a running run: stage, progress, tokens, and pauses it', async () => {
        show(status({ run: llmRun(), steps: freshSteps().map((s) => ({ ...s, enabled: false })) }));
        render(<AuthorLLMBlock />);
        expect(
            await screen.findByText(`Quality check: judges · ${n(8200)} of ${n(13400)}`),
        ).toBeInTheDocument();
        expect(
            screen.getByText(
                `Tokens: ${n(70_000_000)} of ~${n(205_000_000)} (paused at ${n(307_500_000)})`,
            ),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
        await waitFor(() => expect(api.pauseAuthorLLMRun).toHaveBeenCalledWith(41));
    });

    it('resumes a paused run and says why it paused', async () => {
        show(status({ run: llmRun({ status: 'paused', paused_reason: 'budget' }) }));
        render(<AuthorLLMBlock />);
        expect(
            await screen.findByText('Paused: the run reached its token limit'),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Resume' }));
        await waitFor(() => expect(api.resumeAuthorLLMRun).toHaveBeenCalledWith(41));
    });

    it('shows the cost when a price is configured', async () => {
        show(status({ run: llmRun({ cost: 12.5 }), price_unit: 'USD' }));
        render(<AuthorLLMBlock />);
        expect(await screen.findByText(/≈ 12\.50 USD/)).toBeInTheDocument();
    });

    it('sums up a completed check and downloads its report', async () => {
        show(
            status({
                run: llmRun({
                    status: 'completed',
                    stage: 'done',
                    progress: undefined,
                    report: {
                        sha256: 'ab',
                        strata_total: 16,
                        strata_passed: 9,
                        excerpt_helps: 3,
                        registrable: true,
                    },
                }),
            }),
        );
        const blob = new Blob(['{}'], { type: 'application/json' });
        api.getAuthorLLMEvalReport.mockResolvedValue(blob);
        const createURL = vi.fn(() => 'blob:report');
        const revokeURL = vi.fn();
        Object.assign(window.URL, { createObjectURL: createURL, revokeObjectURL: revokeURL });
        const clicks: string[] = [];
        const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
            this: HTMLAnchorElement,
        ) {
            clicks.push(this.download);
        });

        render(<AuthorLLMBlock />);
        expect(
            await screen.findByText(
                'Quality check: ✓ 9 of 16 groups passed · the excerpt helps in 3',
            ),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Report' }));
        await waitFor(() => expect(api.getAuthorLLMEvalReport).toHaveBeenCalledWith(41));
        await waitFor(() => expect(clicks).toEqual(['author-llm-eval-41.json']));
        expect(createURL).toHaveBeenCalledWith(blob);
        click.mockRestore();
    });

    it('shows how many repeated answers changed, per participant', async () => {
        show(
            status({
                run: llmRun({
                    status: 'completed',
                    progress: undefined,
                    report: {
                        sha256: 'ab',
                        strata_total: 1,
                        strata_passed: 0,
                        excerpt_helps: 0,
                        registrable: true,
                        repeats: [
                            { participant: 'production_a', changed: 4, total: 200 },
                            { participant: 'production_b', changed: 3, total: 200 },
                            { participant: 'judge_b', changed: 0, total: 200 },
                        ],
                    },
                }),
            }),
        );
        render(<AuthorLLMBlock />);
        expect(
            await screen.findByText(`gpt-6-luna · repeats: ${n(2)}% changed`),
        ).toBeInTheDocument();
        expect(
            screen.getByText(`deepseek-v4-pro · repeats: ${n(1.5)}% changed`),
        ).toBeInTheDocument();
        expect(screen.getByText(`kimi-k3 · repeats: ${n(0)}% changed`)).toBeInTheDocument();
        expect(ruTranslation['authorLLM.repeatChanged']).toBe(
            '{{participant}} · повторы: {{share}}% изменились',
        );
        expect(enTranslation['authorLLM.repeatChanged']).toBe(
            '{{participant}} · repeats: {{share}}% changed',
        );
    });

    it('sums up a completed resolve run and downloads its report', async () => {
        show(
            status({
                run: llmRun({
                    kind: 'resolve',
                    mode: 'pilot',
                    status: 'completed',
                    stage: 'done',
                    progress: undefined,
                    resolve: { sha256: 'cd', applied: 431, total: 500 },
                }),
            }),
        );
        const blob = new Blob(['{}'], { type: 'application/json' });
        api.getAuthorLLMEvalReport.mockResolvedValue(blob);
        Object.assign(window.URL, { createObjectURL: vi.fn(() => 'blob:r'), revokeObjectURL: vi.fn() });
        const clicks: string[] = [];
        const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
            this: HTMLAnchorElement,
        ) {
            clicks.push(this.download);
        });
        render(<AuthorLLMBlock />);
        expect(
            await screen.findByText(`Resolve pilot: ✓ ${n(431)} of ${n(500)} applied`),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Report' }));
        await waitFor(() => expect(clicks).toEqual(['author-llm-resolve-41.json']));
        click.mockRestore();
    });

    it('shows a running resolve run with its progress', async () => {
        show(
            status({
                run: llmRun({ kind: 'resolve', mode: 'full', stage: 'resolve', progress: { done: 10, total: 1000 } }),
            }),
        );
        render(<AuthorLLMBlock />);
        expect(
            await screen.findByText(`Resolve everything: resolving · ${n(10)} of ${n(1000)}`),
        ).toBeInTheDocument();
    });

    it('says when a completed check cannot back a registration', async () => {
        show(
            status({
                run: llmRun({
                    status: 'completed',
                    progress: undefined,
                    report: {
                        sha256: 'ab',
                        strata_total: 1,
                        strata_passed: 0,
                        excerpt_helps: 0,
                        registrable: false,
                    },
                }),
            }),
        );
        render(<AuthorLLMBlock />);
        expect(
            await screen.findByText('Not enough evidence to start resolving.'),
        ).toBeInTheDocument();
    });

    it('shows participant and endpoint pauses with their action', async () => {
        const list = participants();
        list[1] = { ...list[1], paused_reason: 'model_mismatch' };
        show(status({ participants: list, endpoint_paused_reason: 'quota_exhausted' }));
        render(<AuthorLLMBlock />);
        expect(
            await screen.findByText('LLM: the provider ran out of quota or money'),
        ).toBeInTheDocument();
        expect(
            screen.getByText(
                'deepseek-v4-pro: the model answers differently than at the check — auto-acceptance stopped',
            ),
        ).toBeInTheDocument();
        const resumes = screen.getAllByRole('button', { name: 'Continue' });
        expect(resumes).toHaveLength(2);
        fireEvent.click(resumes[0]);
        await waitFor(() => expect(api.resumeAuthorLLMEndpoint).toHaveBeenCalled());
        fireEvent.click(resumes[1]);
        await waitFor(() =>
            expect(api.resumeAuthorLLMParticipant).toHaveBeenCalledWith('production_b'),
        );
    });

    it('says why the server refused a start', async () => {
        show(status());
        api.startAuthorLLMRun.mockRejectedValue(
            new ApiError('conflict', 409, { body: { error: 'prerequisite_incomplete' } }),
        );
        render(<AuthorLLMBlock />);
        fireEvent.click(await screen.findByRole('button', { name: /Check quality/ }));
        expect(await screen.findByText('Finish the previous step first.')).toBeInTheDocument();
    });

    it('re-reads while a run is active and stops once it is not', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            show(status({ run: llmRun() }));
            render(<AuthorLLMBlock />);
            await screen.findByRole('button', { name: 'Pause' });
            expect(api.getAuthorLLMStatus).toHaveBeenCalledTimes(1);
            show(status({ run: llmRun({ status: 'completed', progress: undefined }) }));
            await act(async () => {
                await vi.advanceTimersByTimeAsync(5000);
            });
            expect(api.getAuthorLLMStatus).toHaveBeenCalledTimes(2);
            await act(async () => {
                await vi.advanceTimersByTimeAsync(15000);
            });
            expect(api.getAuthorLLMStatus).toHaveBeenCalledTimes(2);
        } finally {
            vi.useRealTimers();
        }
    });

    it('offers only closing for a run of an older configuration', async () => {
        show(
            status({
                run: llmRun({ status: 'paused', paused_reason: 'admin', current_config: false }),
            }),
        );
        render(<AuthorLLMBlock />);
        expect(
            await screen.findByText(
                'This run belongs to an earlier configuration and cannot go on; close it to start a new one.',
            ),
        ).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Resume' })).toBeNull();
        expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull();
        fireEvent.click(screen.getByRole('button', { name: 'Close run' }));
        await waitFor(() => expect(api.closeAuthorLLMRun).toHaveBeenCalledWith(41));
        expect(api.resumeAuthorLLMRun).not.toHaveBeenCalled();
    });

    it('offers closing for a still-running run of an older configuration', async () => {
        show(status({ run: llmRun({ current_config: false }) }));
        render(<AuthorLLMBlock />);
        expect(await screen.findByRole('button', { name: 'Close run' })).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull();
    });

    it('offers closing next to resuming for a paused run, not for a running one', async () => {
        show(status({ run: llmRun({ status: 'paused', paused_reason: 'admin' }) }));
        const { unmount } = render(<AuthorLLMBlock />);
        expect(await screen.findByRole('button', { name: 'Resume' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Close run' })).toBeInTheDocument();
        unmount();

        show(status({ run: llmRun() }));
        render(<AuthorLLMBlock />);
        expect(await screen.findByRole('button', { name: 'Pause' })).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Close run' })).toBeNull();
    });

    it('says a run was closed', async () => {
        show(status({ run: llmRun({ status: 'closed', progress: undefined }) }));
        render(<AuthorLLMBlock />);
        expect(await screen.findByText('Quality check: closed')).toBeInTheDocument();
    });

    it('keeps both locales complete', () => {
        const en = enTranslation as Record<string, string>;
        const ru = ruTranslation as Record<string, string>;
        const source = readFileSync('src/features/admin/AuthorLLMBlock.tsx', 'utf8');
        const used = [...source.matchAll(/'(authorLLM\.[a-zA-Z_.]+)'/g)].map((m) => m[1]);
        expect(used.length).toBeGreaterThan(20);
        for (const key of used) {
            expect(en[key], `en ${key}`).toBeTruthy();
            expect(ru[key], `ru ${key}`).toBeTruthy();
            // A value made of placeholders only reads the same in both.
            if (/[A-Za-z]/.test(en[key].replace(/\{\{\w+\}\}/g, ''))) {
                expect(ru[key], `ru ${key}`).not.toBe(en[key]);
            }
        }
        expect(ru['authorLLM.notConfigured']).toBe('LLM не настроена');
        expect(ru['authorLLM.step.eval_pilot']).toBe('Проверить качество');
        expect(ru['authorLLM.step.resolve_full']).toBe('Разобрать всё');
        expect(ru['authorLLM.run.resolve_pilot']).toBe('Разбор: пилот');
        expect(ru['authorLLM.notAvailable']).toBe('Перестановки появятся в следующей версии.');
    });
});
