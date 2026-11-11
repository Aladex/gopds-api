import React, { createContext, useContext, useState, useSyncExternalStore } from 'react';

import * as adminApi from '@/api/admin';
import type {
    AuthorEntityStatus,
    AuthorLLMStatus,
    AuthorMerges,
    AuthorMetadataProblemArchive,
    AuthorMetadataReport,
    AuthorMetadataRun,
} from '@/api/admin';
import { ACTIVE_METADATA, archiveFailures, isUsableRun } from './metadataRun';

/*
 * The scan tab's data sources. Each endpoint the tab polls is one store: one
 * request at a time, one timer, shared by every consumer under the same
 * provider, so the chain and a stage panel reading the same status never fetch
 * it twice. Outside a provider each consumer gets a private store with the
 * same rules, which is how the blocks behave on their own.
 *
 * The polling rules are the blocks' own, moved here unchanged: the metadata run
 * every 5 s while a pass is followed, an LLM status every 5 s while its run is
 * active, the entity status every 5 s while a pass or a Wikidata fetch runs and
 * every 15 s while the autopilot is on, the merges never. A store reads on its
 * first consumer's mount, after every reload, and stops its timer when the
 * last consumer leaves.
 */

/** The metadata run as the card reads it: the newest run, its report and its problem archives. */
export interface MetadataRunData {
    run: AuthorMetadataRun | null;
    report: AuthorMetadataReport | null;
    archives: AuthorMetadataProblemArchive[];
}

export interface SourceData {
    metadataRun: MetadataRunData;
    llmNames: AuthorLLMStatus;
    llmMerge: AuthorLLMStatus;
    entity: AuthorEntityStatus;
    merges: AuthorMerges;
}

export type SourceKey = keyof SourceData;

export interface Source<T> {
    /** The last answer; it survives a failed read. */
    data: T | null;
    /** Whether the last read failed. */
    failed: boolean;
    /** Reads again now; resolves once the answer is in (or failed). */
    reload: () => Promise<void>;
}

interface Definition<T> {
    load: () => Promise<T>;
    /** How long until the next read, or null for no timer. */
    pollMs: (data: T) => number | null;
}

/** How often anything that is running is read again. */
const ACTIVE_POLL_MS = 5000;
/** How often the entity status is read while only the autopilot is on. */
const IDLE_ENTITY_POLL_MS = 15000;

const LLM_ACTIVE: ReadonlySet<string> = new Set(['pending', 'running', 'paused']);
const ENTITY_ACTIVE: ReadonlySet<string> = new Set(['pending', 'running']);

/** Whether the card follows the run: an active pass, final figures being counted, a deletion running. */
export function followsMetadataRun({ run, report, archives }: MetadataRunData): boolean {
    if (archives.some((a) => a.deletion !== null)) {
        return true;
    }
    if (run === null || !isUsableRun(run)) {
        return false;
    }
    if (ACTIVE_METADATA.has(run.status)) {
        return true;
    }
    return (
        run.status === 'completed' &&
        report !== null &&
        report.id === run.id &&
        report.not_ready_reasons.includes('figures_pending')
    );
}

const llmDefinition = (base?: string): Definition<AuthorLLMStatus> => ({
    load: async () =>
        (
            await (base === undefined
                ? adminApi.getAuthorLLMStatus()
                : adminApi.getAuthorLLMStatus(base))
        ).status,
    pollMs: (status) =>
        status.run !== null && LLM_ACTIVE.has(status.run.status) ? ACTIVE_POLL_MS : null,
});

const DEFINITIONS: { [K in SourceKey]: () => Definition<SourceData[K]> } = {
    metadataRun: () => ({
        load: async () => {
            const { run } = await adminApi.getLatestAuthorMetadataRun();
            let report: AuthorMetadataReport | null = null;
            let archives: AuthorMetadataProblemArchive[] = [];
            if (run !== null && isUsableRun(run)) {
                if (!ACTIVE_METADATA.has(run.status)) {
                    report = (await adminApi.getAuthorMetadataRunReport(run.id)).report;
                }
                if (archiveFailures(run) > 0) {
                    archives = (await adminApi.getAuthorMetadataRunArchives(run.id)).archives;
                }
            }
            return { run, report, archives };
        },
        pollMs: (data) => (followsMetadataRun(data) ? ACTIVE_POLL_MS : null),
    }),
    llmNames: () => llmDefinition(),
    llmMerge: () => llmDefinition(adminApi.llmMergeBase),
    entity: () => ({
        load: async () => (await adminApi.getAuthorEntityStatus()).status,
        pollMs: (status) => {
            const active =
                (status.run !== null && ENTITY_ACTIVE.has(status.run.status)) ||
                status.wikidata?.latest?.status === 'fetching';
            if (active) {
                return ACTIVE_POLL_MS;
            }
            return status.autopilot?.enabled === true ? IDLE_ENTITY_POLL_MS : null;
        },
    }),
    merges: () => ({
        load: async () => (await adminApi.getAuthorMerges()).merges,
        pollMs: () => null,
    }),
};

interface Snapshot<T> {
    data: T | null;
    failed: boolean;
}

/** One endpoint's state, its in-flight read and its timer. */
class SourceStore<T> {
    private snapshot: Snapshot<T> = { data: null, failed: false };
    private readonly listeners = new Set<() => void>();
    /** Each read's number; only the newest read may write its answer. */
    private generation = 0;
    private timer: ReturnType<typeof setTimeout> | null = null;
    private readonly definition: Definition<T>;

    constructor(definition: Definition<T>) {
        this.definition = definition;
    }

    readonly subscribe = (listener: () => void): (() => void) => {
        this.listeners.add(listener);
        if (this.listeners.size === 1) {
            void this.reload();
        }
        return () => {
            this.listeners.delete(listener);
            if (this.listeners.size === 0) {
                this.clearTimer();
            }
        };
    };

    readonly getSnapshot = (): Snapshot<T> => this.snapshot;

    readonly reload = async (): Promise<void> => {
        const current = ++this.generation;
        this.clearTimer();
        let next: Snapshot<T>;
        try {
            next = { data: await this.definition.load(), failed: false };
        } catch {
            next = { data: this.snapshot.data, failed: true };
        }
        if (current !== this.generation) {
            return;
        }
        this.snapshot = next;
        this.listeners.forEach((listener) => listener());
        this.schedule();
    };

    private schedule() {
        if (this.listeners.size === 0 || this.snapshot.data === null) {
            return;
        }
        const ms = this.definition.pollMs(this.snapshot.data);
        if (ms !== null) {
            this.timer = setTimeout(() => void this.reload(), ms);
        }
    }

    private clearTimer() {
        if (this.timer !== null) {
            clearTimeout(this.timer);
            this.timer = null;
        }
    }
}

type Stores = { [K in SourceKey]?: SourceStore<SourceData[K]> };

/** The provider's stores, created on first use and kept while it lives. */
class StoreRegistry {
    private readonly stores: Stores = {};

    get<K extends SourceKey>(key: K): SourceStore<SourceData[K]> {
        const existing = this.stores[key] as SourceStore<SourceData[K]> | undefined;
        if (existing !== undefined) {
            return existing;
        }
        const created = new SourceStore<SourceData[K]>(DEFINITIONS[key]());
        (this.stores as Record<K, SourceStore<SourceData[K]>>)[key] = created;
        return created;
    }
}

const SourcesContext = createContext<StoreRegistry | null>(null);

/** Shares one store per endpoint among every consumer below it. */
export function ScanSourcesProvider({ children }: { children: React.ReactNode }) {
    const [registry] = useState(() => new StoreRegistry());
    return <SourcesContext.Provider value={registry}>{children}</SourcesContext.Provider>;
}

/** One endpoint's data: the provider's shared store, or a private one without a provider. */
export function useScanSource<K extends SourceKey>(key: K): Source<SourceData[K]> {
    const registry = useContext(SourcesContext);
    const [own] = useState(() =>
        registry === null ? new SourceStore<SourceData[K]>(DEFINITIONS[key]()) : null,
    );
    const store = registry?.get(key) ?? own;
    if (store === null) {
        // A provider that appeared above a mounted consumer: not a supported tree.
        throw new Error('useScanSource: no store');
    }
    const snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot);
    return { data: snapshot.data, failed: snapshot.failed, reload: store.reload };
}
