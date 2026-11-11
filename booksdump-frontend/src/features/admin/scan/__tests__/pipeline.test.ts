import {
    CHAIN,
    arrowStyle,
    derivePipeline,
    stageFromParams,
    type NodeState,
    type PipelineInput,
    type StageKey,
} from '@/features/admin/scan/pipeline';
import {
    DAY_MS,
    ago,
    autopilot,
    entityStatus,
    healthy,
    llmStatus,
    metadataRun,
    twoProblems,
} from './pipelineFixtures';

describe('derivePipeline', () => {
    it('healthy prod reads healthy', () => {
        const NOW = new Date();
        const p = derivePipeline(healthy(NOW), NOW);
        for (const key of CHAIN) {
            expect(p.nodes[key].state, key).toBe('ok');
        }
        expect(p.problems).toEqual([]);
        expect(p.stoppedAt).toBeNull();
        expect(p.defaultStage).toBe('human');
        expect(p.nodes.human).toMatchObject({ state: 'waiting', figure: 13_023 });
        expect(p.nodes.human.reason).toEqual({ key: 'human.open', values: { count: 13_023 } });
        expect(p.nodes.arch).toMatchObject({ figure: 4 });
        expect(p.nodes.arch.reason).toEqual({
            key: 'arch.last',
            values: { archive: 'fb2-824014-826219.zip', books: 2_034, date: ago(NOW, 8 * DAY_MS) },
        });
        expect(p.nodes.books).toMatchObject({ figure: 558_608 });
        expect(p.nodes.names).toMatchObject({ figure: 642_138 });
        expect(p.nodes.names.reason).toEqual({ key: 'names.selected', values: { invalid: 121 } });
        expect(p.nodes.model).toMatchObject({ figure: 49 });
        expect(p.nodes.model.reason).toEqual({
            key: 'model.last',
            values: { id: 138, applied: 49, total: 56, percent: 74 },
        });
        expect(p.nodes.ent).toMatchObject({ figure: 146_788 });
        expect(p.nodes.ent.reason).toEqual({
            key: 'ent.last',
            values: { id: 100, date: ago(NOW, 5 * DAY_MS) },
        });
    });

    it('old errors stay quiet', () => {
        const NOW = new Date();
        const p = derivePipeline(healthy(NOW), NOW);
        expect(p.nodes.arch.state).toBe('ok');
        expect(p.nodes.books.state).toBe('ok');
        expect(p.nodes.books.reason).toEqual({ key: 'books.read', values: { unread: 20 } });
    });

    it('two problems', () => {
        const NOW = new Date();
        const input = twoProblems(NOW);
        const p = derivePipeline(input, NOW);
        expect(p.nodes.arch.state).toBe('warning');
        expect(p.nodes.arch.reason).toEqual({
            key: 'arch.unread',
            values: { count: 37, archive: 'fb2-826220-828301.zip', date: ago(NOW, 1 * DAY_MS) },
        });
        expect(p.nodes.model.state).toBe('stuck');
        expect(p.nodes.model.reason).toEqual({
            key: 'model.endpoint',
            values: { reason: 'quota_exhausted', since: input.llmNames?.endpoint_paused_at },
        });
        expect(p.nodes.names.state).toBe('waiting');
        expect(p.nodes.names.reason).toEqual({
            key: 'names.waitModel',
            values: { pending: 2_316 },
        });
        expect(p.nodes.ent.state).toBe('waiting');
        expect(p.nodes.ent.reason).toEqual({ key: 'ent.waitsFor', values: { stage: 'model' } });
        expect(p.nodes.books.state).toBe('ok');
        expect(p.nodes.human).toMatchObject({ state: 'waiting', figure: 13_412 });
        expect(p.problems).toEqual(['model', 'arch']);
        expect(p.stoppedAt).toBe('model');
        expect(p.defaultStage).toBe('model');
    });

    it('missing inputs are loading, not ok-with-zero', () => {
        const NOW = new Date();
        const empty: PipelineInput = {
            scanned: null,
            unscannedCount: null,
            scanStatus: null,
            fixScan: null,
            metadataRun: null,
            llmNames: null,
            llmMerge: null,
            entity: null,
            mergesUnanchored: null,
        };
        const p = derivePipeline(empty, NOW);
        for (const key of [...CHAIN, 'human'] as StageKey[]) {
            expect(p.nodes[key], key).toMatchObject({
                state: 'ok',
                figure: null,
                reason: { key: 'loading' },
            });
        }
        expect(p.problems).toEqual([]);
        expect(p.defaultStage).toBe('human');
    });

    it('a stale scan warns after 35 days', () => {
        const NOW = new Date();
        const at = (days: number): PipelineInput => ({
            ...healthy(NOW),
            scanned: [
                {
                    name: 'a.zip',
                    books_count: 10,
                    errors_count: 0,
                    scanned_at: ago(NOW, days * DAY_MS),
                },
            ],
        });
        expect(derivePipeline(at(36), NOW).nodes.arch).toMatchObject({
            state: 'warning',
            reason: { key: 'arch.stale', values: { date: ago(NOW, 36 * DAY_MS) } },
        });
        expect(derivePipeline(at(34), NOW).nodes.arch.state).toBe('ok');
    });

    // One row per rule of the plan's table: the input it needs, and the
    // state and reason it must give the stage.
    const rules: {
        name: string;
        stage: StageKey;
        input: (now: Date) => PipelineInput;
        state: NodeState;
        key: string;
        values?: Record<string, string | number> | ((now: Date) => Record<string, string | number>);
        progress?: number;
    }[] = [
        {
            name: 'a failed scan session is stuck',
            stage: 'arch',
            input: (now) => ({
                ...healthy(now),
                scanStatus: {
                    is_running: false,
                    archives_processed: 1,
                    total_archives: 2,
                    last_error: 'boom',
                },
            }),
            state: 'stuck',
            key: 'arch.scanFailed',
        },
        {
            name: 'a running scan shows its progress',
            stage: 'arch',
            input: (now) => ({
                ...healthy(now),
                scanStatus: { is_running: true, archives_processed: 1, total_archives: 4 },
            }),
            state: 'ok',
            key: 'arch.scanning',
            values: { done: 1, total: 4 },
            progress: 25,
        },
        {
            name: 'unscanned archives warn',
            stage: 'arch',
            input: (now) => ({ ...healthy(now), unscannedCount: 2 }),
            state: 'warning',
            key: 'arch.unscanned',
            values: { count: 2 },
        },
        {
            name: 'a failed metadata pass is stuck',
            stage: 'books',
            input: (now) => ({
                ...healthy(now),
                metadataRun: metadataRun(now, { status: 'failed_systemic' }),
            }),
            state: 'stuck',
            key: 'books.failed',
        },
        {
            name: 'a paused metadata pass is stuck with its class',
            stage: 'books',
            input: (now) => ({
                ...healthy(now),
                metadataRun: metadataRun(now, {
                    status: 'paused',
                    last_error_class: 'archive_unreadable',
                }),
            }),
            state: 'stuck',
            key: 'books.paused',
            values: { class: 'archive_unreadable' },
        },
        {
            name: 'missing archives warn with their books',
            stage: 'books',
            input: (now) => {
                const run = metadataRun(now);
                run.stages.extraction.by_status.archive_missing = 5;
                run.stages.extraction.by_status.archive_unreadable = 2;
                return { ...healthy(now), metadataRun: run };
            },
            state: 'warning',
            key: 'books.archives',
            values: { count: 7 },
        },
        {
            name: 'a fix scan shows on the books node',
            stage: 'books',
            input: (now) => ({ ...healthy(now), fixScan: { running: true, percent: 42 } }),
            state: 'ok',
            key: 'books.fixing',
            values: { percent: 42 },
            progress: 42,
        },
        {
            name: 'an active metadata pass shows its progress',
            stage: 'books',
            input: (now) => {
                const run = metadataRun(now, { status: 'running' });
                run.stages.extraction.done = 279_304;
                return { ...healthy(now), metadataRun: run };
            },
            state: 'ok',
            key: 'books.reading',
            values: { done: 279_304, total: 558_608 },
            progress: 50,
        },
        {
            name: 'local failures warn on names',
            stage: 'names',
            input: (now) => {
                const run = metadataRun(now);
                run.stages.local.failed = 3;
                return { ...healthy(now), metadataRun: run };
            },
            state: 'warning',
            key: 'names.localFailed',
            values: { count: 3 },
        },
        {
            name: 'pending names with a working model are ok',
            stage: 'names',
            input: (now) => {
                const run = metadataRun(now);
                run.credits.pending = 12;
                return { ...healthy(now), metadataRun: run };
            },
            state: 'ok',
            key: 'names.pending',
            values: { pending: 12 },
        },
        {
            name: 'a paused participant is stuck',
            stage: 'model',
            input: (now) => ({
                ...healthy(now),
                llmNames: llmStatus({
                    participants: [
                        { slot: 'production_a', model: 'model-a' },
                        {
                            slot: 'production_b',
                            model: 'model-b',
                            paused_reason: 'model_mismatch',
                            paused_at: ago(now, 2 * 60 * 60 * 1000),
                        },
                    ],
                }),
            }),
            state: 'stuck',
            key: 'model.participant',
            values: { model: 'model-b', reason: 'model_mismatch' },
        },
        {
            name: 'a paused merge run is stuck',
            stage: 'model',
            input: (now) => ({
                ...healthy(now),
                llmMerge: llmStatus({
                    run: {
                        id: 521,
                        kind: 'merge',
                        mode: 'run',
                        status: 'paused',
                        paused_reason: 'budget',
                        current_config: true,
                        estimate_tokens: 1,
                        limit_tokens: 1,
                        spent_tokens: 1,
                    },
                }),
            }),
            state: 'stuck',
            key: 'model.runPaused',
            values: { reason: 'budget' },
        },
        {
            name: 'a failed LLM run is stuck',
            stage: 'model',
            input: (now) => ({
                ...healthy(now),
                llmNames: llmStatus({
                    run: {
                        id: 139,
                        kind: 'resolve',
                        mode: 'full',
                        status: 'failed_systemic',
                        current_config: true,
                        estimate_tokens: 1,
                        limit_tokens: 1,
                        spent_tokens: 1,
                    },
                }),
            }),
            state: 'stuck',
            key: 'model.runFailed',
        },
        {
            name: 'a running LLM run shows its progress',
            stage: 'model',
            input: (now) => ({
                ...healthy(now),
                llmNames: llmStatus({
                    run: {
                        id: 139,
                        kind: 'resolve',
                        mode: 'full',
                        status: 'running',
                        current_config: true,
                        progress: { done: 30, total: 120 },
                        estimate_tokens: 1,
                        limit_tokens: 1,
                        spent_tokens: 1,
                    },
                }),
            }),
            state: 'ok',
            key: 'model.running',
            values: { done: 30, total: 120 },
            progress: 25,
        },
        {
            name: 'a switched-off LLM layer is ok and says so',
            stage: 'model',
            input: (now) => ({
                ...healthy(now),
                llmNames: llmStatus({ enabled: false, run: null }),
            }),
            state: 'ok',
            key: 'model.off',
        },
        {
            name: 'budget at 95 % warns',
            stage: 'model',
            input: (now) => ({
                ...healthy(now),
                llmNames: llmStatus({ budget_tokens: 1000, spent_tokens: 950 }),
            }),
            state: 'warning',
            key: 'model.budget',
            values: { percent: 95 },
        },
        {
            name: 'no resolve run yet shows the budget only',
            stage: 'model',
            input: (now) => ({ ...healthy(now), llmNames: llmStatus({ run: null }) }),
            state: 'ok',
            key: 'model.budgetLeft',
            values: { percent: 74 },
        },
        {
            name: 'autopilot failures make entities stuck',
            stage: 'ent',
            input: (now) => ({
                ...healthy(now),
                entity: entityStatus(now, {
                    autopilot: autopilot(now, {
                        failures: 2,
                        last_error_class: 'build_failed',
                        next_attempt_at: ago(now, -10 * 60 * 1000),
                    }),
                }),
            }),
            state: 'stuck',
            key: 'ent.autopilotFailing',
            values: (now) => ({ count: 2, class: 'build_failed', next: ago(now, -10 * 60 * 1000) }),
        },
        {
            name: 'a failed entity pass is stuck',
            stage: 'ent',
            input: (now) => {
                const base = entityStatus(now);
                return {
                    ...healthy(now),
                    entity: { ...base, run: { ...base.run!, id: 101, status: 'failed' } },
                };
            },
            state: 'stuck',
            key: 'ent.passFailed',
            values: { id: 101 },
        },
        {
            name: 'unanchored veto warns',
            stage: 'ent',
            input: (now) => ({ ...healthy(now), mergesUnanchored: true }),
            state: 'warning',
            key: 'ent.unanchored',
        },
        {
            // The census streak counts only the newest passes, so a settled
            // census failure always comes with a failed latest run, and that
            // rule matches first (Task 3 decides whether census gets its own).
            name: 'a settled census failure reads as a failed pass',
            stage: 'ent',
            input: (now) => {
                const base = entityStatus(now);
                return {
                    ...healthy(now),
                    entity: {
                        ...base,
                        run: {
                            ...base.run!,
                            id: 101,
                            status: 'failed',
                            last_error_class: 'census_failed',
                            created_at: ago(now, 2 * 60 * 60 * 1000),
                            finished_at: ago(now, 60 * 60 * 1000),
                        },
                        autopilot: autopilot(now, {
                            state: 'build_backoff',
                            census_failures: 1,
                            census_retry_at: ago(now, -DAY_MS),
                        }),
                    },
                };
            },
            state: 'stuck',
            key: 'ent.passFailed',
            values: { id: 101 },
        },
        {
            // The only payload that reaches ent.census: the status reads the
            // latest run before the census streak, so a pass that fails its
            // census between the two reads is still running in the answer.
            name: 'census failures warn when the failed pass races the status read',
            stage: 'ent',
            input: (now) => {
                const base = entityStatus(now);
                return {
                    ...healthy(now),
                    entity: {
                        ...base,
                        run: {
                            ...base.run!,
                            id: 101,
                            status: 'running',
                            units_done: 0,
                            created_at: ago(now, 60 * 1000),
                            finished_at: undefined,
                        },
                        autopilot: autopilot(now, {
                            census_failures: 1,
                            census_retry_at: ago(now, -DAY_MS),
                        }),
                    },
                };
            },
            state: 'warning',
            key: 'ent.census',
            values: (now) => ({ count: 1, retry: ago(now, -DAY_MS) }),
        },
        {
            name: 'a running entity pass shows its progress',
            stage: 'ent',
            input: (now) => {
                const base = entityStatus(now);
                return {
                    ...healthy(now),
                    entity: {
                        ...base,
                        run: {
                            ...base.run!,
                            status: 'running',
                            units_done: 3,
                            units_total: 4,
                            finished_at: undefined,
                        },
                    },
                };
            },
            state: 'ok',
            key: 'ent.building',
            values: { done: 3, total: 4 },
            progress: 75,
        },
        {
            name: 'an empty queue is ok',
            stage: 'human',
            input: (now) => {
                const run = metadataRun(now);
                run.stages.review.open = 0;
                return { ...healthy(now), metadataRun: run };
            },
            state: 'ok',
            key: 'human.empty',
        },
    ];

    it.each(rules)('$name', ({ stage, input, state, key, values, progress }) => {
        const NOW = new Date();
        const node = derivePipeline(input(NOW), NOW).nodes[stage];
        expect(node.state).toBe(state);
        expect(node.reason.key).toBe(key);
        if (values !== undefined) {
            expect(node.reason.values).toEqual(typeof values === 'function' ? values(NOW) : values);
        }
        if (progress !== undefined) {
            expect(node.progress).toBe(progress);
        }
    });

    it('entities do not wait on a working model', () => {
        const NOW = new Date();
        const run = metadataRun(NOW);
        run.credits.pending = 40;
        const p = derivePipeline(
            {
                ...healthy(NOW),
                metadataRun: run,
                entity: entityStatus(NOW, { autopilot: autopilot(NOW, { build_pending: true }) }),
            },
            NOW,
        );
        expect(p.nodes.ent.state).toBe('ok');
        expect(p.nodes.ent.reason.key).toBe('ent.last');
    });

    it('pending names win over local failures while the model works', () => {
        const NOW = new Date();
        const run = metadataRun(NOW);
        run.credits.pending = 12;
        run.stages.local.failed = 3;
        const p = derivePipeline({ ...healthy(NOW), metadataRun: run }, NOW);
        expect(p.nodes.names.state).toBe('ok');
        expect(p.nodes.names.reason).toEqual({ key: 'names.pending', values: { pending: 12 } });
        expect(p.problems).toEqual([]);
        expect(p.defaultStage).toBe('human');
    });

    it('an unusable metadata run is not read', () => {
        const NOW = new Date();
        const p = derivePipeline(
            { ...healthy(NOW), metadataRun: metadataRun(NOW, { id: 0 }) },
            NOW,
        );
        for (const key of ['books', 'names', 'human'] as StageKey[]) {
            expect(p.nodes[key], key).toMatchObject({ figure: null, reason: { key: 'loading' } });
        }
    });

    it('a waiting stage is never a problem', () => {
        const NOW = new Date();
        const input = twoProblems(NOW);
        const p = derivePipeline({ ...input, scanned: healthy(NOW).scanned }, NOW);
        expect(p.problems).toEqual(['model']);
        expect(p.nodes.ent.state).toBe('waiting');
    });

    it('stuck stages come before warnings, each in chain order', () => {
        const NOW = new Date();
        const input = twoProblems(NOW);
        const p = derivePipeline(
            {
                ...input,
                mergesUnanchored: true,
                metadataRun: metadataRun(NOW, { status: 'failed_systemic' }),
            },
            NOW,
        );
        expect(p.problems).toEqual(['books', 'model', 'arch']);
        expect(p.stoppedAt).toBe('books');
    });
});

describe('arrowStyle', () => {
    it('marks the arrow into the stuck stage and idles the ones after it', () => {
        expect(CHAIN.slice(0, -1).map((_, i) => arrowStyle(i, 'model'))).toEqual([
            'flow',
            'flow',
            'stuck',
            'idle',
        ]);
        expect(CHAIN.slice(0, -1).map((_, i) => arrowStyle(i, 'books'))).toEqual([
            'stuck',
            'idle',
            'idle',
            'idle',
        ]);
        expect(CHAIN.slice(0, -1).map((_, i) => arrowStyle(i, null))).toEqual([
            'flow',
            'flow',
            'flow',
            'flow',
        ]);
    });
});

describe('stageFromParams', () => {
    it.each([
        ['stage=ent', 'ent'],
        ['stage=human', 'human'],
        ['tab=authors', 'human'],
        ['tab=scanned', 'arch'],
        ['tab=unscanned', 'arch'],
        ['stage=bogus', null],
        ['', null],
        ['stage=ent&tab=authors', 'ent'],
    ])('%s → %s', (query, expected) => {
        expect(stageFromParams(new URLSearchParams(query))).toBe(expected);
    });
});
