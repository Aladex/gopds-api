import type { AuthorEntityStatus, AuthorLLMStatus, AuthorMetadataRun } from '@/api/admin';
import { ACTIVE_METADATA, archiveFailures, isUsableRun, runErrors } from './metadataRun';

/*
 * The scan tab's pipeline: five stages in a chain plus the human review that
 * branches off it. derivePipeline turns what the tab already reads into each
 * stage's state and its one reason line; it is pure, so the whole table of
 * rules is tested without rendering anything.
 */

export type StageKey = 'arch' | 'books' | 'names' | 'model' | 'ent' | 'human';

/** The chain in flow order; the human review is not part of it. */
export const CHAIN: readonly StageKey[] = ['arch', 'books', 'names', 'model', 'ent'];

const STAGES: ReadonlySet<string> = new Set<StageKey>([...CHAIN, 'human']);

/**
 * ok: nothing to do. warning: worth a look. stuck: the flow stops here.
 * waiting: held by a stage before it (or, for the human review, by a person).
 */
export type NodeState = 'ok' | 'warning' | 'stuck' | 'waiting';

/** A reason line: a key under scanPanel.reason.* and its values; dates stay ISO strings. */
export interface Reason {
    key: string;
    values?: Record<string, string | number>;
}

export interface StageNode {
    key: StageKey;
    state: NodeState;
    /** The node's big number; null while its source has not answered. */
    figure: number | null;
    reason: Reason;
    /** 0–100 while something on the stage is running. */
    progress?: number;
}

export interface ScannedArchiveSummary {
    name: string;
    books_count: number;
    errors_count: number;
    scanned_at: string;
}

export interface PipelineInput {
    scanned: ScannedArchiveSummary[] | null;
    unscannedCount: number | null;
    scanStatus: {
        is_running: boolean;
        archives_processed: number;
        total_archives: number;
        last_error?: string;
    } | null;
    fixScan: { running: boolean; percent: number } | null;
    /** The latest author metadata run, as the card reads it. */
    metadataRun: AuthorMetadataRun | null;
    llmNames: AuthorLLMStatus | null;
    llmMerge: AuthorLLMStatus | null;
    entity: AuthorEntityStatus | null;
    mergesUnanchored: boolean | null;
}

export interface Pipeline {
    nodes: Record<StageKey, StageNode>;
    /** Stuck stages, then warnings, each in chain order; never waiting, never the human review. */
    problems: StageKey[];
    /** The first stuck stage in chain order. */
    stoppedAt: StageKey | null;
    /** The stage a page without a chosen one opens on. */
    defaultStage: StageKey;
}

/** A newest scan older than this means the monthly archive did not come. */
const STALE_SCAN_MS = 35 * 24 * 60 * 60 * 1000;

/** The share of the token budget spent that starts to warn. */
const BUDGET_WARNING = 0.95;

const LLM_ACTIVE: ReadonlySet<string> = new Set(['pending', 'running']);
const ENTITY_ACTIVE: ReadonlySet<string> = new Set(['pending', 'running']);

const loading = (key: StageKey): StageNode => ({
    key,
    state: 'ok',
    figure: null,
    reason: { key: 'loading' },
});

const node = (
    key: StageKey,
    state: NodeState,
    figure: number | null,
    reasonKey: string,
    values?: Record<string, string | number>,
    progress?: number,
): StageNode => ({
    key,
    state,
    figure,
    reason: values === undefined ? { key: reasonKey } : { key: reasonKey, values },
    ...(progress === undefined ? {} : { progress }),
});

const percentOf = (done: number, total: number): number =>
    total > 0 ? Math.round((done / total) * 100) : 0;

/** The newest archive with a readable scan date. */
function newestArchive(scanned: ScannedArchiveSummary[]): ScannedArchiveSummary | null {
    let newest: ScannedArchiveSummary | null = null;
    let newestAt = -Infinity;
    for (const archive of scanned) {
        const at = Date.parse(archive.scanned_at);
        if (!Number.isNaN(at) && at > newestAt) {
            newest = archive;
            newestAt = at;
        }
    }
    return newest;
}

function archNode(input: PipelineInput, now: Date): StageNode {
    const { scanned, scanStatus } = input;
    if (scanned === null) {
        return loading('arch');
    }
    const figure = scanned.length;
    if (scanStatus?.last_error && !scanStatus.is_running) {
        return node('arch', 'stuck', figure, 'arch.scanFailed');
    }
    if (scanStatus?.is_running) {
        const { archives_processed: done, total_archives: total } = scanStatus;
        return node('arch', 'ok', figure, 'arch.scanning', { done, total }, percentOf(done, total));
    }
    const newest = newestArchive(scanned);
    if (newest !== null && newest.errors_count > 0) {
        return node('arch', 'warning', figure, 'arch.unread', {
            count: newest.errors_count,
            archive: newest.name,
            date: newest.scanned_at,
        });
    }
    if ((input.unscannedCount ?? 0) > 0) {
        return node('arch', 'warning', figure, 'arch.unscanned', {
            count: input.unscannedCount ?? 0,
        });
    }
    if (newest === null) {
        return node('arch', 'ok', figure, 'arch.none');
    }
    if (now.getTime() - Date.parse(newest.scanned_at) > STALE_SCAN_MS) {
        return node('arch', 'warning', figure, 'arch.stale', { date: newest.scanned_at });
    }
    return node('arch', 'ok', figure, 'arch.last', {
        archive: newest.name,
        books: newest.books_count,
        date: newest.scanned_at,
    });
}

function booksNode(run: AuthorMetadataRun, fixScan: PipelineInput['fixScan']): StageNode {
    const { extraction } = run.stages;
    const figure = extraction.done;
    if (run.status === 'failed_systemic') {
        return node('books', 'stuck', figure, 'books.failed');
    }
    if (run.status === 'paused') {
        return node('books', 'stuck', figure, 'books.paused', {
            class: run.last_error_class ?? '',
        });
    }
    const lost = archiveFailures(run);
    if (lost > 0) {
        return node('books', 'warning', figure, 'books.archives', { count: lost });
    }
    if (fixScan?.running) {
        const percent = Math.round(fixScan.percent);
        return node('books', 'ok', figure, 'books.fixing', { percent }, percent);
    }
    if (ACTIVE_METADATA.has(run.status)) {
        return node(
            'books',
            'ok',
            figure,
            'books.reading',
            { done: extraction.done, total: extraction.total },
            percentOf(extraction.done, extraction.total),
        );
    }
    return node('books', 'ok', figure, 'books.read', { unread: runErrors(run) });
}

function namesNode(run: AuthorMetadataRun, modelStuck: boolean): StageNode {
    const { credits } = run;
    const figure = credits.selected;
    if (credits.pending > 0 && modelStuck) {
        return node('names', 'waiting', figure, 'names.waitModel', { pending: credits.pending });
    }
    if (credits.pending > 0) {
        return node('names', 'ok', figure, 'names.pending', { pending: credits.pending });
    }
    if (run.stages.local.failed > 0) {
        return node('names', 'warning', figure, 'names.localFailed', {
            count: run.stages.local.failed,
        });
    }
    return node('names', 'ok', figure, 'names.selected', { invalid: credits.invalid });
}

function modelNode(names: AuthorLLMStatus, merge: AuthorLLMStatus | null): StageNode {
    const statuses = merge === null ? [names] : [names, merge];
    const figure = names.run?.resolve?.applied ?? null;
    for (const status of statuses) {
        if (status.endpoint_paused_reason) {
            const values: Record<string, string> = { reason: status.endpoint_paused_reason };
            if (status.endpoint_paused_at) {
                values.since = status.endpoint_paused_at;
            }
            return node('model', 'stuck', figure, 'model.endpoint', values);
        }
    }
    for (const status of statuses) {
        const paused = status.participants?.find((p) => p.paused_reason);
        if (paused) {
            return node('model', 'stuck', figure, 'model.participant', {
                model: paused.model,
                reason: paused.paused_reason ?? '',
            });
        }
    }
    for (const status of statuses) {
        if (status.run?.status === 'paused') {
            return node('model', 'stuck', figure, 'model.runPaused', {
                reason: status.run.paused_reason ?? '',
            });
        }
    }
    for (const status of statuses) {
        if (status.run?.status === 'failed_systemic') {
            return node('model', 'stuck', figure, 'model.runFailed');
        }
    }
    for (const status of statuses) {
        const run = status.run;
        if (run !== null && LLM_ACTIVE.has(run.status)) {
            const done = run.progress?.done ?? 0;
            const total = run.progress?.total ?? 0;
            return node(
                'model',
                'ok',
                figure,
                'model.running',
                { done, total },
                percentOf(done, total),
            );
        }
    }
    if (!names.enabled) {
        return node('model', 'ok', figure, 'model.off');
    }
    const share = names.budget_tokens > 0 ? names.spent_tokens / names.budget_tokens : 0;
    const percent = Math.round(share * 100);
    if (share >= BUDGET_WARNING) {
        return node('model', 'warning', figure, 'model.budget', { percent });
    }
    const resolve = names.run?.resolve;
    if (names.run !== null && resolve) {
        return node('model', 'ok', figure, 'model.last', {
            id: names.run.id,
            applied: resolve.applied,
            total: resolve.total,
            percent,
        });
    }
    return node('model', 'ok', figure, 'model.budgetLeft', { percent });
}

function entNode(
    status: AuthorEntityStatus,
    modelStuck: boolean,
    pendingNames: number,
    unanchored: boolean,
): StageNode {
    const figure = status.report?.entities ?? null;
    const autopilot = status.autopilot ?? null;
    const run = status.run;
    if (autopilot !== null && autopilot.failures > 0) {
        return node('ent', 'stuck', figure, 'ent.autopilotFailing', {
            count: autopilot.failures,
            class: autopilot.last_error_class ?? '',
            next: autopilot.next_attempt_at ?? '',
        });
    }
    if (run?.status === 'failed') {
        return node('ent', 'stuck', figure, 'ent.passFailed', { id: run.id });
    }
    if (modelStuck && (pendingNames > 0 || autopilot?.build_pending === true)) {
        return node('ent', 'waiting', figure, 'ent.waitsFor', { stage: 'model' });
    }
    if (unanchored) {
        return node('ent', 'warning', figure, 'ent.unanchored');
    }
    if (autopilot !== null && autopilot.census_failures > 0) {
        return node('ent', 'warning', figure, 'ent.census', {
            count: autopilot.census_failures,
            retry: autopilot.census_retry_at ?? '',
        });
    }
    if (run !== null && ENTITY_ACTIVE.has(run.status)) {
        return node(
            'ent',
            'ok',
            figure,
            'ent.building',
            { done: run.units_done, total: run.units_total },
            percentOf(run.units_done, run.units_total),
        );
    }
    if (run?.status === 'completed') {
        return node('ent', 'ok', figure, 'ent.last', { id: run.id, date: run.finished_at ?? '' });
    }
    return node('ent', 'ok', figure, 'ent.none');
}

function humanNode(run: AuthorMetadataRun): StageNode {
    const open = run.stages.review.open;
    return open > 0
        ? node('human', 'waiting', open, 'human.open', { count: open })
        : node('human', 'ok', open, 'human.empty');
}

/** Each stage's state and reason, the problems in order, and where the flow stops. */
export function derivePipeline(input: PipelineInput, now: Date): Pipeline {
    const run =
        input.metadataRun !== null && isUsableRun(input.metadataRun) ? input.metadataRun : null;
    const model =
        input.llmNames === null ? loading('model') : modelNode(input.llmNames, input.llmMerge);
    const modelStuck = model.state === 'stuck';
    const nodes: Record<StageKey, StageNode> = {
        arch: archNode(input, now),
        books: run === null ? loading('books') : booksNode(run, input.fixScan),
        names: run === null ? loading('names') : namesNode(run, modelStuck),
        model,
        ent:
            input.entity === null
                ? loading('ent')
                : entNode(
                      input.entity,
                      modelStuck,
                      run?.credits.pending ?? 0,
                      input.mergesUnanchored === true,
                  ),
        human: run === null ? loading('human') : humanNode(run),
    };
    const problems = [
        ...CHAIN.filter((key) => nodes[key].state === 'stuck'),
        ...CHAIN.filter((key) => nodes[key].state === 'warning'),
    ];
    const stoppedAt = CHAIN.find((key) => nodes[key].state === 'stuck') ?? null;
    return { nodes, problems, stoppedAt, defaultStage: problems[0] ?? 'human' };
}

/**
 * The arrow from CHAIN[from] to the next stage: 'stuck' into the stage the
 * flow stops at, 'idle' for every arrow after it, 'flow' otherwise.
 */
export function arrowStyle(from: number, stoppedAt: StageKey | null): 'flow' | 'stuck' | 'idle' {
    const stop = stoppedAt === null ? -1 : CHAIN.indexOf(stoppedAt);
    if (stop < 0) {
        return 'flow';
    }
    if (from + 1 === stop) {
        return 'stuck';
    }
    return from >= stop ? 'idle' : 'flow';
}

/**
 * The stage the address names: ?stage=<key>, or the old tabs (?tab=authors
 * is the review queue, the archive tabs are the archives); null for none.
 */
export function stageFromParams(params: URLSearchParams): StageKey | null {
    const stage = params.get('stage');
    if (stage !== null && STAGES.has(stage)) {
        return stage as StageKey;
    }
    const tab = params.get('tab');
    if (tab === 'authors') {
        return 'human';
    }
    if (tab === 'scanned' || tab === 'unscanned') {
        return 'arch';
    }
    return null;
}
