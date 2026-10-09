import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { AlertCircle, RefreshCw } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Badge } from '@/shared/ui/badge';
import { Button } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import { Field } from '@/shared/ui/field';
import { Input } from '@/shared/ui/input';
import { Progress } from '@/shared/ui/progress';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/ui/tabs';
import { Textarea } from '@/shared/ui/textarea';
import { formatDate } from '@/shared/lib/formatDate';
import * as adminApi from '@/api/admin';
import type {
    AuthorMetadataReport,
    AuthorMetadataRun,
    AuthorMetadataRunMode,
    AuthorMetadataRunStart,
    AuthorMetadataRunStatus,
} from '@/api/admin';
import {
    AUTHOR_METADATA_EXTRACTION_RETRY_CLASSES,
    AUTHOR_METADATA_LOCAL_RETRY_CLASSES,
    closedErrorCode,
    isExtractionRetryClass,
    isLocalRetryClass,
} from '@/api/admin';
import { isApiError } from '@/api/errors';
import AuthorReviewQueue from '@/features/admin/AuthorReviewQueue';

/** The contract's ceiling on a smoke run's book_ids selector. */
export const MAX_BOOK_IDS = 10000;

/** How often the durable status is re-fetched while a run is in flight. */
const POLL_INTERVAL_MS = 15000;

/**
 * The last run this browser followed. The current endpoint answers only from
 * the active slot, so once a run completes this id is what keeps it visible
 * through GET /runs/:id until a newer run starts.
 */
export const TRACKED_RUN_KEY = 'authorNormalization.trackedRunId';

const readTrackedRunId = (): number | null => {
    try {
        const raw = window.localStorage.getItem(TRACKED_RUN_KEY);
        if (raw === null) {
            return null;
        }
        const id = Number(raw);
        return Number.isSafeInteger(id) && id > 0 ? id : null;
    } catch {
        return null;
    }
};

/**
 * Remembers the tracked id in component memory first and in browser storage
 * second. Storage can be unavailable (private mode, quota, a throwing
 * implementation); the in-memory id is what keeps the run reachable for as
 * long as the screen is mounted.
 */
const useTrackedRunId = () => useRef<number | null>(null);

const ACTIVE_STATUSES: ReadonlySet<AuthorMetadataRunStatus> = new Set([
    'pending',
    'running',
    'paused',
]);

const TERMINAL_STATUSES: ReadonlySet<AuthorMetadataRunStatus> = new Set([
    'completed',
    'failed_systemic',
]);

const MODES: AuthorMetadataRunMode[] = ['smoke', 'pilot_archive', 'full'];

/**
 * The closed last_error_class vocabulary (contract, phase 20 integration fix
 * round 1): null, or one of the four run error classes, with the fixed
 * sentinel "other" for out-of-set stored values. A Map so only its own
 * entries are found; anything else renders the same generic localized
 * "other" label with no raw suffix — an out-of-set value has no guaranteed
 * diagnostic meaning or privacy properties, so it is never echoed.
 */
const LAST_ERROR_CLASS_FALLBACKS = new Map([
    ['archive_unreadable', 'Unreadable archive'],
    ['database_invariant', 'Database invariant broken'],
    ['version_mismatch', 'Version mismatch'],
    ['extractor_misconfigured', 'Extractor misconfigured'],
    ['other', 'Other error'],
]);

/** Fallbacks for the dynamic status/mode keys; the locales carry the real strings. */
const STATUS_FALLBACKS: Record<AuthorMetadataRunStatus, string> = {
    pending: 'Queued',
    running: 'Running',
    paused: 'Paused',
    completed: 'Completed',
    failed_systemic: 'Failed (systemic)',
};

const MODE_FALLBACKS: Record<AuthorMetadataRunMode, string> = {
    smoke: 'Smoke',
    pilot_archive: 'Pilot archive',
    full: 'Full catalog',
};

/** The closed status enum; anything else is an unknown status, not a state to act on. */
const KNOWN_STATUSES: ReadonlySet<AuthorMetadataRunStatus> = new Set([
    'pending',
    'running',
    'paused',
    'completed',
    'failed_systemic',
]);

/**
 * Every closed error code the runs contract publishes, with the English the
 * locales replace. A Map so that only its own entries are ever found: an
 * error body of "constructor" or "__proto__" must not inherit Object
 * properties past the unknown-code fallback. Anything not listed here
 * renders as the generic failure — never the server's string.
 */
const ERROR_FALLBACKS: ReadonlyMap<string, string> = new Map([
    ['invalid_request', 'The request body is malformed.'],
    ['invalid_mode', 'Unknown run mode.'],
    [
        'invalid_selector',
        'The mode and its selector do not match, or the selector selects no books.',
    ],
    ['too_many_book_ids', 'At most 10000 book IDs are allowed.'],
    ['invalid_book_id', 'Book IDs must be positive numbers.'],
    ['duplicate_book_id', 'Book IDs must not repeat.'],
    ['invalid_archive', 'The archive name is invalid.'],
    ['invalid_id', 'The run ID is invalid.'],
    ['invalid_stage', 'The retry stage is invalid.'],
    ['invalid_error_class', 'This error class cannot be retried for that stage.'],
    ['run_not_found', 'The run was not found.'],
    ['active_run_exists', 'A run is already active.'],
    ['full_run_not_approved', 'The full run is not approved.'],
    ['invalid_transition', 'This action is not allowed in the current state.'],
    ['not_a_completed_pilot', 'Only a completed pilot run can be approved.'],
    ['already_approved', 'The full run is already approved.'],
    ['internal_error', 'The server failed to handle the request.'],
    ['run_service_unavailable', 'The run service is not available.'],
]);

export type BookIdsParseError = 'empty' | 'bad' | 'unsafe' | 'duplicate' | 'too_many';

/**
 * Parses the book-ids selector: any mix of commas, spaces and newlines, each
 * token a positive safe integer, no repeats, at most MAX_BOOK_IDS of them.
 * Every rejection names a rule the server would also enforce, so the client
 * blocks what the server would bounce — and never rounds an operator's ID
 * into a different one.
 */
export function parseBookIds(text: string): { ids: number[]; error?: BookIdsParseError } {
    const tokens = text.split(/[\s,;]+/).filter((token) => token !== '');
    if (tokens.length === 0) {
        return { ids: [], error: 'empty' };
    }
    const ids: number[] = [];
    for (const token of tokens) {
        if (!/^\d+$/.test(token)) {
            return { ids: [], error: 'bad' };
        }
        const id = Number(token);
        if (!Number.isSafeInteger(id)) {
            return { ids: [], error: 'unsafe' };
        }
        if (id <= 0) {
            return { ids: [], error: 'bad' };
        }
        ids.push(id);
    }
    if (new Set(ids).size !== ids.length) {
        return { ids: [], error: 'duplicate' };
    }
    if (ids.length > MAX_BOOK_IDS) {
        return { ids: [], error: 'too_many' };
    }
    return { ids };
}

export interface RunControls {
    pause: boolean;
    resume: boolean;
    retry: boolean;
    /** The stages whose closed retry list accepts the run's error class. */
    retryStages: ('extraction' | 'local')[];
}

/**
 * Which controls the server status permits; nothing else may enable them.
 *
 * Retry follows the server's own state machine (database.retryRunStatuses):
 * pending, running, paused and an unapproved completed run accept retries —
 * an active run keeps going, a completed one goes back to running. The
 * server answers 409 invalid_transition for failed_systemic and for an
 * approved pilot (the approval pins it), so no retry is offered there. The
 * stage and error class are the admin's explicit choice from the closed
 * per-stage lists; the run's last_error_class is a diagnostic, not a
 * prerequisite.
 */
export function controlsFor(run: AuthorMetadataRun): RunControls {
    const retryable =
        run.status === 'pending' ||
        run.status === 'running' ||
        run.status === 'paused' ||
        (run.status === 'completed' && !run.approved_for_full);
    return {
        pause: run.status === 'running',
        resume: run.status === 'paused',
        retry: retryable,
        retryStages: retryable ? ['extraction', 'local'] : [],
    };
}

/**
 * A full run may start only behind a completed, approved pilot. A missing or
 * version-mismatched approval arrives from the server as approved_for_full
 * false (or a 409), so this flag is the whole gate on the client.
 */
export function fullStartAllowed(run: AuthorMetadataRun | null): boolean {
    return (
        run !== null &&
        run.mode === 'pilot_archive' &&
        run.status === 'completed' &&
        run.approved_for_full
    );
}

/**
 * A run is actionable only through an id that JSON parsing has not rounded.
 * The contract allows int64 ids while JavaScript only represents integers
 * exactly up to 2^53-1, so an id outside that range is refused — with every
 * action disabled — rather than sent to a possibly different run.
 */
export function hasUsableRunId(run: AuthorMetadataRun | null): boolean {
    return run !== null && Number.isSafeInteger(run.id) && run.id > 0;
}

/** One stage's percent, guarding the empty stage; never a run-wide figure. */
export function percent(done: number, total: number): number {
    if (total <= 0) {
        return 0;
    }
    return Math.round((done / total) * 100);
}

/** A labelled figure. Values sit in their own node so tests and readers see one number each. */
const Stat: React.FC<{ label: React.ReactNode; value: React.ReactNode }> = ({ label, value }) => (
    <div className="flex items-baseline justify-between gap-4 text-sm">
        <span className="text-muted-foreground">{label}</span>
        <span className="tabular-nums">{value}</span>
    </div>
);

/** One stage/queue block: a named region with its own heading and bar. */
const StageBlock: React.FC<{ id: string; title: string; children: React.ReactNode }> = ({
    id,
    title,
    children,
}) => (
    <section
        aria-labelledby={id}
        className="flex flex-col gap-2 rounded-lg border border-border p-3"
    >
        <h4 id={id} className="text-sm font-medium">
            {title}
        </h4>
        {children}
    </section>
);

/** Labelled bar plus its own count and percent — this stage's, nobody else's. */
const StageProgress: React.FC<{ title: string; done: number; total: number }> = ({
    title,
    done,
    total,
}) => {
    const value = percent(done, total);
    return (
        <div className="flex flex-col gap-1">
            <div className="flex items-center justify-between text-sm">
                <span className="tabular-nums">
                    {done} / {total}
                </span>
                <span className="font-semibold tabular-nums">{value}%</span>
            </div>
            <Progress value={value} aria-label={title} />
        </div>
    );
};

const AuthorNormalization: React.FC = () => {
    const { t } = useTranslation();
    const [searchParams, setSearchParams] = useSearchParams();
    /**
     * The tab lives in the address (?tab=review): the detail screen's Back
     * button returns here straight into the queue tab, and a reload keeps it.
     */
    const activeTab = searchParams.get('tab') === 'review' ? 'review' : 'dashboard';
    const changeTab = (value: string) => {
        const next = new URLSearchParams(searchParams);
        if (value === 'review') {
            next.set('tab', 'review');
        } else {
            next.delete('tab');
        }
        setSearchParams(next);
    };
    const [run, setRun] = useState<AuthorMetadataRun | null>(null);
    const [report, setReport] = useState<AuthorMetadataReport | null>(null);
    const [actionError, setActionError] = useState<string | null>(null);
    const [notice, setNotice] = useState<string | null>(null);
    const [mode, setMode] = useState<AuthorMetadataRunMode>('smoke');
    const [bookIdsText, setBookIdsText] = useState('');
    const [archiveName, setArchiveName] = useState('');
    const [formError, setFormError] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);
    /** The admin's explicit retry choice; never derived from the run. */
    const [retryStage, setRetryStage] = useState<'extraction' | 'local' | null>(null);
    const [retryClass, setRetryClass] = useState<string | null>(null);
    /**
     * Loading, a confirmed answer (including a confirmed null run), or a
     * failed load. Only a confirmed answer may enable the start form: a
     * failed GET has not established that no active run exists.
     */
    const [loadPhase, setLoadPhase] = useState<'loading' | 'ready' | 'failed'>('loading');
    /**
     * Status responses are stamped with a generation. A mutation response
     * bumps it, so an older GET that resolves afterwards cannot resurrect a
     * state the action already replaced.
     */
    const fetchGeneration = useRef(0);
    const trackedRunIdRef = useTrackedRunId();

    /** Memory first, storage as a best-effort bonus for the next reload. */
    /**
     * Only ids JSON parsing has not rounded may enter the tracker; the same
     * validation every other id consumer applies. An unsafe id is dropped
     * rather than tracked, so it can never be sent to GET /runs/:id — the
     * displayed run keeps its invalid-data alert either way.
     */
    const rememberTrackedRunId = useCallback((id: number) => {
        if (Number.isSafeInteger(id) && id > 0) {
            trackedRunIdRef.current = id;
            try {
                window.localStorage.setItem(TRACKED_RUN_KEY, String(id));
            } catch {
                // Storage refused; the in-memory id still tracks the run.
            }
            return;
        }
        trackedRunIdRef.current = null;
        try {
            window.localStorage.removeItem(TRACKED_RUN_KEY);
        } catch {
            // Storage refused; there is nothing safe left to track anyway.
        }
    }, []);

    /** The durable status is the source of truth; polling only supplements it. */
    const fetchCurrent = useCallback(async () => {
        const generation = ++fetchGeneration.current;
        try {
            const data = await adminApi.getCurrentAuthorMetadataRun();
            if (generation !== fetchGeneration.current) {
                return;
            }
            if (data.run !== null) {
                // Every run the active slot names becomes the tracked one.
                rememberTrackedRunId(data.run.id);
                setRun(data.run);
                setLoadPhase('ready');
                return;
            }
            // The slot is empty: the latest run of any status — completed
            // included — is what stays on the dashboard, straight from the
            // database with no client-side memory involved.
            let latestFailed = false;
            try {
                const latest = await adminApi.getLatestAuthorMetadataRun();
                if (generation !== fetchGeneration.current) {
                    return;
                }
                if (latest.run !== null) {
                    rememberTrackedRunId(latest.run.id);
                    setRun(latest.run);
                    setLoadPhase('ready');
                    return;
                }
            } catch {
                if (generation !== fetchGeneration.current) {
                    return;
                }
                latestFailed = true;
                // The tracked id below is the secondary fallback; whether it
                // exists decides between a run and a load error.
            }
            const tracked = trackedRunIdRef.current ?? readTrackedRunId();
            if (tracked === null) {
                if (latestFailed) {
                    // The latest endpoint failed and nothing is tracked: no
                    // confirmed answer exists, so this is not "no run yet".
                    setActionError(
                        t('authorNormalization.loadError', 'Failed to load the current run.'),
                    );
                    setLoadPhase('failed');
                    return;
                }
                setRun(null);
                setLoadPhase('ready');
                return;
            }
            if (!Number.isSafeInteger(tracked) || tracked <= 0) {
                // Unreachable with the adoption validation; refused again so
                // a rounded id is never interpolated into a route.
                setRun(null);
                setLoadPhase('ready');
                return;
            }
            try {
                const byId = await adminApi.getAuthorMetadataRun(tracked);
                if (generation !== fetchGeneration.current) {
                    return;
                }
                // A run loaded through the fallback is as tracked as any
                // other: storage failing later must not lose it either.
                rememberTrackedRunId(byId.run.id);
                setRun(byId.run);
                setLoadPhase('ready');
            } catch (error) {
                if (generation !== fetchGeneration.current) {
                    return;
                }
                if (closedErrorCode(error) === 'run_not_found') {
                    // A confirmed missing run is the true empty state.
                    setRun(null);
                    setLoadPhase('ready');
                    return;
                }
                // A server or network failure is not "no run": keep whatever
                // is on screen and surface a retryable load error.
                setActionError(
                    t('authorNormalization.loadError', 'Failed to load the current run.'),
                );
                setLoadPhase('failed');
            }
        } catch {
            if (generation !== fetchGeneration.current) {
                return;
            }
            setActionError(t('authorNormalization.loadError', 'Failed to load the current run.'));
            setLoadPhase('failed');
        }
    }, [rememberTrackedRunId, t]);

    useEffect(() => {
        fetchCurrent();
    }, [fetchCurrent]);

    /** Adopts a mutation's run and supersedes any status GET still in flight. */
    const applyRun = useCallback(
        (next: AuthorMetadataRun) => {
            fetchGeneration.current += 1;
            // Starting (or adopting) a run makes it the tracked one.
            rememberTrackedRunId(next.id);
            setRun(next);
            setLoadPhase('ready');
        },
        [rememberTrackedRunId],
    );

    const runID = run?.id ?? null;
    const runStatus = run?.status ?? null;
    useEffect(() => {
        setRetryStage(null);
        setRetryClass(null);
    }, [runID]);
    useEffect(() => {
        setReport(null);
        if (
            runID === null ||
            // An id outside the safe range was rounded by JSON parsing;
            // requesting its report could read a different run.
            !Number.isSafeInteger(runID) ||
            runID <= 0 ||
            runStatus === null ||
            !TERMINAL_STATUSES.has(runStatus)
        ) {
            return;
        }
        let cancelled = false;
        // The report is supplementary: if it cannot be loaded the dashboard
        // still shows the durable run status above.
        adminApi
            .getAuthorMetadataRunReport(runID)
            .then((data) => {
                if (!cancelled) {
                    setReport(data.report);
                }
            })
            .catch(() => {});
        return () => {
            cancelled = true;
        };
    }, [runID, runStatus]);

    // An unrecognized status is unknown, not inactive: it keeps blocking the
    // start form and keeps the poll running until a valid snapshot arrives.
    const statusKnown = run === null || KNOWN_STATUSES.has(run.status);
    const activeRun = run !== null && (!statusKnown || ACTIVE_STATUSES.has(run.status));
    const idUsable = hasUsableRunId(run);

    useEffect(() => {
        if (!activeRun) {
            return;
        }
        const timer = setInterval(fetchCurrent, POLL_INTERVAL_MS);
        return () => clearInterval(timer);
    }, [activeRun, fetchCurrent]);

    /** Renders a closed error code as its localized text, or a generic failure. */
    const describeError = useCallback(
        (error: unknown): string => {
            const body = isApiError(error) ? error.body : undefined;
            const code =
                body && typeof body === 'object'
                    ? (body as Record<string, unknown>).error
                    : undefined;
            if (typeof code === 'string') {
                const fallback = ERROR_FALLBACKS.get(code);
                if (fallback !== undefined) {
                    return t(`authorNormalization.errors.${code}`, fallback);
                }
            }
            // Everything else — undocumented code, inherited-object key,
            // garbage body — is the generic message; the server's string
            // never reaches a reader.
            return t('authorNormalization.actionError', 'Action failed.');
        },
        [t],
    );

    const bookIdsErrorText = useCallback(
        (error: BookIdsParseError): string => {
            switch (error) {
                case 'empty':
                    return t(
                        'authorNormalization.form.errors.emptyBookIds',
                        'Enter at least one book ID.',
                    );
                case 'bad':
                    return t(
                        'authorNormalization.form.errors.badBookIds',
                        'Book IDs must be positive numbers.',
                    );
                case 'unsafe':
                    return t(
                        'authorNormalization.form.errors.unsafeBookIds',
                        'Book IDs must be whole numbers up to 9007199254740991.',
                    );
                case 'duplicate':
                    return t(
                        'authorNormalization.form.errors.duplicateBookIds',
                        'Book IDs must not repeat.',
                    );
                case 'too_many':
                    return t(
                        'authorNormalization.form.errors.tooManyBookIds',
                        'At most 10000 book IDs are allowed.',
                    );
            }
        },
        [t],
    );

    const handleStart = () => {
        setFormError(null);
        setActionError(null);
        setNotice(null);
        let payload: AuthorMetadataRunStart;
        if (mode === 'smoke') {
            const parsed = parseBookIds(bookIdsText);
            if (parsed.error) {
                setFormError(bookIdsErrorText(parsed.error));
                return;
            }
            payload = { mode, book_ids: parsed.ids };
        } else if (mode === 'pilot_archive') {
            const name = archiveName.trim();
            if (name === '') {
                setFormError(
                    t('authorNormalization.form.errors.archiveRequired', 'Enter the archive name.'),
                );
                return;
            }
            payload = { mode, archive: name };
        } else {
            payload = { mode };
        }
        setBusy(true);
        adminApi
            .startAuthorMetadataRun(payload)
            .then((data) => {
                applyRun(data.run);
                setBookIdsText('');
                setArchiveName('');
            })
            .catch((error) => setActionError(describeError(error)))
            .finally(() => setBusy(false));
    };

    /** One in-flight action at a time: they all mutate the same durable run. */
    const withBusy = (action: () => Promise<unknown>) => {
        setActionError(null);
        setNotice(null);
        setBusy(true);
        action()
            .catch((error) => setActionError(describeError(error)))
            .finally(() => setBusy(false));
    };

    /** A closed-vocabulary label that keeps the diagnostic code visible. */
    const labelForLastErrorClass = useCallback(
        (code: string): string => {
            const fallback = LAST_ERROR_CLASS_FALLBACKS.get(code);
            return fallback === undefined
                ? t('authorNormalization.errorClass.other', 'Other error')
                : `${t(`authorNormalization.errorClass.${code}`, fallback)} (${code})`;
        },
        [t],
    );

    const controls = run !== null ? controlsFor(run) : null;
    const fullAllowed = fullStartAllowed(run);
    const startDisabled =
        busy || loadPhase !== 'ready' || activeRun || (mode === 'full' && !fullAllowed);
    // Run actions need a confirmed, current snapshot whose id survived JSON
    // parsing; a failing refresh or an unusable id leaves them all disabled.
    const actionsBlocked = busy || !idUsable || loadPhase !== 'ready';

    return (
        <div className="flex flex-col gap-4">
            <h2 className="text-lg font-medium">
                {t('authorNormalization.title', 'Author normalization')}
            </h2>
            <Tabs value={activeTab} onValueChange={changeTab}>
                <TabsList>
                    <TabsTrigger value="dashboard">
                        {t('authorNormalization.dashboardTab', 'Dashboard')}
                    </TabsTrigger>
                    <TabsTrigger value="review">
                        {t('authorReview.tab', 'Review queue')}
                    </TabsTrigger>
                </TabsList>
                <TabsContent value="dashboard" className="flex flex-col gap-4">
                    <div className="flex justify-end">
                        <Button
                            variant="outline"
                            size="sm"
                            onClick={() => {
                                setActionError(null);
                                fetchCurrent();
                            }}
                        >
                            <RefreshCw className="size-4" />
                            {t('authorNormalization.refresh', 'Refresh status')}
                        </Button>
                    </div>

                    {loadPhase === 'loading' && (
                        <p className="text-sm text-muted-foreground">
                            {t('loading', 'Loading...')}
                        </p>
                    )}
                    {loadPhase === 'ready' && run === null && (
                        <p className="text-sm text-muted-foreground">
                            {t(
                                'authorNormalization.emptyState',
                                'No author normalization run yet. Start one below.',
                            )}
                        </p>
                    )}
                    {actionError && (
                        <Alert variant="destructive">
                            <AlertCircle className="size-4" />
                            <AlertDescription>{actionError}</AlertDescription>
                        </Alert>
                    )}
                    {run !== null && !idUsable && (
                        <Alert variant="destructive">
                            <AlertCircle className="size-4" />
                            <AlertDescription>
                                {t(
                                    'authorNormalization.invalidRunData',
                                    'The run data received from the server is invalid; run actions are disabled.',
                                )}
                            </AlertDescription>
                        </Alert>
                    )}
                    {run !== null && !statusKnown && (
                        <Alert variant="destructive">
                            <AlertCircle className="size-4" />
                            <AlertDescription>
                                {t(
                                    'authorNormalization.unknownStatus',
                                    'The run status received from the server is unknown; actions are disabled until a valid status arrives.',
                                )}
                            </AlertDescription>
                        </Alert>
                    )}
                    {notice && <p className="text-sm text-muted-foreground">{notice}</p>}

                    {run !== null && (
                        <Card>
                            <CardContent className="flex flex-col gap-3">
                                <section
                                    aria-labelledby="author-norm-run-heading"
                                    className="flex flex-col gap-3"
                                >
                                    <div className="flex flex-wrap items-center gap-2">
                                        <h3
                                            id="author-norm-run-heading"
                                            className="text-base font-medium"
                                        >
                                            {/* A run out of the active slot is the last run, not the current one. */}
                                            {statusKnown && !ACTIVE_STATUSES.has(run.status)
                                                ? t(
                                                      'authorNormalization.latestRunTitle',
                                                      'Last run',
                                                  )
                                                : t(
                                                      'authorNormalization.runTitle',
                                                      'Current run',
                                                  )}{' '}
                                            #{run.id}
                                        </h3>
                                        <Badge>
                                            {t(
                                                `authorNormalization.mode.${run.mode}`,
                                                MODE_FALLBACKS[run.mode],
                                            )}
                                        </Badge>
                                        <Badge
                                            variant={
                                                run.status === 'failed_systemic'
                                                    ? 'destructive'
                                                    : 'secondary'
                                            }
                                        >
                                            {t(
                                                `authorNormalization.status.${run.status}`,
                                                KNOWN_STATUSES.has(run.status)
                                                    ? STATUS_FALLBACKS[run.status]
                                                    : t(
                                                          'authorNormalization.status.unknown',
                                                          'Unknown',
                                                      ),
                                            )}
                                        </Badge>
                                    </div>

                                    <p className="flex flex-wrap gap-x-4 text-sm text-muted-foreground">
                                        <span>
                                            {t('authorNormalization.extractorVersion', 'Extractor')}
                                            :{' '}
                                            <span className="text-foreground">
                                                {run.extractor_version}
                                            </span>
                                        </span>
                                        <span>
                                            {t(
                                                'authorNormalization.normalizerVersion',
                                                'Normalizer',
                                            )}
                                            :{' '}
                                            <span className="text-foreground">
                                                {run.normalizer_version}
                                            </span>
                                        </span>
                                    </p>

                                    <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-muted-foreground">
                                        {run.started_at && (
                                            <span>
                                                {t('authorNormalization.startedAt', 'Started')}:{' '}
                                                {formatDate(run.started_at)}
                                            </span>
                                        )}
                                        {run.extraction_completed_at && (
                                            <span>
                                                {t(
                                                    'authorNormalization.extractionCompletedAt',
                                                    'Extraction completed',
                                                )}
                                                : {formatDate(run.extraction_completed_at)}
                                            </span>
                                        )}
                                        {run.completed_at && (
                                            <span>
                                                {t(
                                                    'authorNormalization.completedAt',
                                                    'Completed at',
                                                )}
                                                : {formatDate(run.completed_at)}
                                            </span>
                                        )}
                                    </div>

                                    {run.last_error_class !== null && (
                                        <Alert variant="destructive">
                                            <AlertCircle className="size-4" />
                                            <AlertDescription>
                                                <span>
                                                    {t(
                                                        'authorNormalization.lastErrorClass',
                                                        'Last error class',
                                                    )}
                                                </span>
                                                :{' '}
                                                <span>
                                                    {labelForLastErrorClass(run.last_error_class)}
                                                </span>
                                            </AlertDescription>
                                        </Alert>
                                    )}

                                    <div className="grid gap-3 md:grid-cols-2">
                                        <StageBlock
                                            id="author-norm-extraction"
                                            title={t(
                                                'authorNormalization.stage.extraction',
                                                'Extraction',
                                            )}
                                        >
                                            <StageProgress
                                                title={t(
                                                    'authorNormalization.stage.extraction',
                                                    'Extraction',
                                                )}
                                                done={run.stages.extraction.done}
                                                total={run.stages.extraction.total}
                                            />
                                            <Stat
                                                label={t('authorNormalization.pending', 'Pending')}
                                                value={run.stages.extraction.pending}
                                            />
                                            <Stat
                                                label={t('authorNormalization.leased', 'Leased')}
                                                value={run.stages.extraction.leased}
                                            />
                                            <Stat
                                                label={t(
                                                    'authorNormalization.oldestPending',
                                                    'Oldest pending',
                                                )}
                                                value={`${run.stages.extraction.oldest_pending_age_s} ${t(
                                                    'authorNormalization.seconds',
                                                    's',
                                                )}`}
                                            />
                                            <Stat
                                                label={t(
                                                    'authorNormalization.extraction.itemsPerMinute',
                                                    'Items/min',
                                                )}
                                                value={run.stages.extraction.items_per_minute}
                                            />
                                            {run.stages.extraction.current_archive !== null && (
                                                <p className="text-sm break-words">
                                                    <span className="text-muted-foreground">
                                                        {t(
                                                            'authorNormalization.extraction.currentArchive',
                                                            'Current archive',
                                                        )}
                                                        :{' '}
                                                    </span>
                                                    <span>
                                                        {run.stages.extraction.current_archive}
                                                    </span>
                                                </p>
                                            )}
                                            <div className="flex flex-col gap-0.5 border-t border-border pt-2">
                                                {Object.entries(run.stages.extraction.by_status)
                                                    .filter(([, count]) => count > 0)
                                                    .map(([status, count]) => (
                                                        <Stat
                                                            key={status}
                                                            label={status}
                                                            value={count}
                                                        />
                                                    ))}
                                            </div>
                                        </StageBlock>

                                        <StageBlock
                                            id="author-norm-local"
                                            title={t(
                                                'authorNormalization.stage.local',
                                                'Local normalization',
                                            )}
                                        >
                                            <StageProgress
                                                title={t(
                                                    'authorNormalization.stage.local',
                                                    'Local normalization',
                                                )}
                                                done={run.stages.local.done}
                                                total={run.stages.local.total}
                                            />
                                            <Stat
                                                label={t('authorNormalization.pending', 'Pending')}
                                                value={run.stages.local.pending}
                                            />
                                            <Stat
                                                label={t('authorNormalization.leased', 'Leased')}
                                                value={run.stages.local.leased}
                                            />
                                            <Stat
                                                label={t(
                                                    'authorNormalization.local.failed',
                                                    'Failed',
                                                )}
                                                value={run.stages.local.failed}
                                            />
                                            <Stat
                                                label={t(
                                                    'authorNormalization.oldestPending',
                                                    'Oldest pending',
                                                )}
                                                value={`${run.stages.local.oldest_pending_age_s} ${t(
                                                    'authorNormalization.seconds',
                                                    's',
                                                )}`}
                                            />
                                        </StageBlock>

                                        <StageBlock
                                            id="author-norm-review"
                                            title={t(
                                                'authorNormalization.stage.review',
                                                'Manual review',
                                            )}
                                        >
                                            <StageProgress
                                                title={t(
                                                    'authorNormalization.stage.review',
                                                    'Manual review',
                                                )}
                                                done={run.stages.review.closed}
                                                total={
                                                    run.stages.review.open +
                                                    run.stages.review.closed
                                                }
                                            />
                                            <Stat
                                                label={t('authorNormalization.review.open', 'Open')}
                                                value={run.stages.review.open}
                                            />
                                            <Stat
                                                label={t(
                                                    'authorNormalization.review.closed',
                                                    'Closed',
                                                )}
                                                value={run.stages.review.closed}
                                            />
                                        </StageBlock>

                                        <StageBlock
                                            id="author-norm-credits"
                                            title={t(
                                                'authorNormalization.stage.credits',
                                                'Credits',
                                            )}
                                        >
                                            <Stat
                                                label={t(
                                                    'authorNormalization.credits.selected',
                                                    'Selected',
                                                )}
                                                value={run.credits.selected}
                                            />
                                            <Stat
                                                label={t(
                                                    'authorNormalization.credits.invalid',
                                                    'Invalid',
                                                )}
                                                value={run.credits.invalid}
                                            />
                                            <Stat
                                                label={t(
                                                    'authorNormalization.credits.review',
                                                    'Awaiting review',
                                                )}
                                                value={run.credits.review}
                                            />
                                            <Stat
                                                label={t(
                                                    'authorNormalization.credits.pending',
                                                    'Pending',
                                                )}
                                                value={run.credits.pending}
                                            />
                                            <div className="flex flex-col gap-0.5 border-t border-border pt-2">
                                                {Object.entries(run.credits.unresolved)
                                                    .filter(([, count]) => count > 0)
                                                    .map(([reason, count]) => (
                                                        <Stat
                                                            key={reason}
                                                            label={reason}
                                                            value={count}
                                                        />
                                                    ))}
                                            </div>
                                        </StageBlock>
                                    </div>

                                    {run.status === 'completed' && run.stages.review.open > 0 && (
                                        <p className="text-sm text-muted-foreground">
                                            {t('authorNormalization.reviewBacklog', {
                                                count: run.stages.review.open,
                                            })}
                                        </p>
                                    )}

                                    {controls !== null && (
                                        <div className="flex flex-wrap gap-2">
                                            <Button
                                                variant="outline"
                                                size="sm"
                                                disabled={actionsBlocked || !controls.pause}
                                                onClick={() =>
                                                    withBusy(() =>
                                                        adminApi
                                                            .pauseAuthorMetadataRun(run.id)
                                                            .then((data) => applyRun(data.run)),
                                                    )
                                                }
                                            >
                                                {t('authorNormalization.pause', 'Pause')}
                                            </Button>
                                            <Button
                                                variant="outline"
                                                size="sm"
                                                disabled={actionsBlocked || !controls.resume}
                                                onClick={() =>
                                                    withBusy(() =>
                                                        adminApi
                                                            .resumeAuthorMetadataRun(run.id)
                                                            .then((data) => applyRun(data.run)),
                                                    )
                                                }
                                            >
                                                {t('authorNormalization.resume', 'Resume')}
                                            </Button>
                                            {controls.retry && (
                                                <div className="flex flex-col gap-2">
                                                    <span className="text-xs text-muted-foreground">
                                                        {t(
                                                            'authorNormalization.retryHint',
                                                            'Choose the stage and the error class to reopen.',
                                                        )}
                                                    </span>
                                                    <div
                                                        role="group"
                                                        aria-label={t(
                                                            'authorNormalization.retryStage',
                                                            'Retry stage',
                                                        )}
                                                        className="flex flex-wrap gap-2"
                                                    >
                                                        {(['extraction', 'local'] as const).map(
                                                            (stage) => (
                                                                <Button
                                                                    key={stage}
                                                                    variant={
                                                                        retryStage === stage
                                                                            ? 'default'
                                                                            : 'outline'
                                                                    }
                                                                    size="sm"
                                                                    aria-pressed={
                                                                        retryStage === stage
                                                                    }
                                                                    onClick={() => {
                                                                        setRetryStage(stage);
                                                                        setRetryClass(null);
                                                                    }}
                                                                >
                                                                    {stage === 'extraction'
                                                                        ? t(
                                                                              'authorNormalization.stage.extraction',
                                                                              'Extraction',
                                                                          )
                                                                        : t(
                                                                              'authorNormalization.stage.local',
                                                                              'Local normalization',
                                                                          )}
                                                                </Button>
                                                            ),
                                                        )}
                                                    </div>
                                                    {retryStage !== null && (
                                                        <div
                                                            role="group"
                                                            aria-label={t(
                                                                'authorNormalization.retryClass',
                                                                'Retry error class',
                                                            )}
                                                            className="flex flex-wrap gap-2"
                                                        >
                                                            {(retryStage === 'extraction'
                                                                ? AUTHOR_METADATA_EXTRACTION_RETRY_CLASSES
                                                                : AUTHOR_METADATA_LOCAL_RETRY_CLASSES
                                                            ).map((errorClass) => (
                                                                <Button
                                                                    key={errorClass}
                                                                    variant={
                                                                        retryClass === errorClass
                                                                            ? 'default'
                                                                            : 'outline'
                                                                    }
                                                                    size="sm"
                                                                    aria-pressed={
                                                                        retryClass === errorClass
                                                                    }
                                                                    onClick={() =>
                                                                        setRetryClass(errorClass)
                                                                    }
                                                                >
                                                                    {errorClass}
                                                                </Button>
                                                            ))}
                                                        </div>
                                                    )}
                                                    <div>
                                                        <Button
                                                            size="sm"
                                                            disabled={
                                                                actionsBlocked ||
                                                                retryStage === null ||
                                                                retryClass === null
                                                            }
                                                            onClick={() => {
                                                                if (
                                                                    retryStage === null ||
                                                                    retryClass === null
                                                                ) {
                                                                    return;
                                                                }
                                                                // The class groups are the
                                                                // closed per-stage lists; the
                                                                // guards admit the pair.
                                                                const payload =
                                                                    retryStage === 'extraction' &&
                                                                    isExtractionRetryClass(
                                                                        retryClass,
                                                                    )
                                                                        ? {
                                                                              stage: retryStage,
                                                                              error_class:
                                                                                  retryClass,
                                                                          }
                                                                        : retryStage === 'local' &&
                                                                            isLocalRetryClass(
                                                                                retryClass,
                                                                            )
                                                                          ? {
                                                                                stage: retryStage,
                                                                                error_class:
                                                                                    retryClass,
                                                                            }
                                                                          : null;
                                                                if (payload === null) {
                                                                    return;
                                                                }
                                                                withBusy(() =>
                                                                    adminApi
                                                                        .retryAuthorMetadataRun(
                                                                            run.id,
                                                                            payload,
                                                                        )
                                                                        .then((data) => {
                                                                            // reopened is the
                                                                            // exact count; zero is
                                                                            // an honest answer —
                                                                            // nothing matched, or
                                                                            // the matching rows
                                                                            // spent their budget.
                                                                            setNotice(
                                                                                data.reopened > 0
                                                                                    ? t(
                                                                                          'authorNormalization.reopened',
                                                                                          {
                                                                                              count: data.reopened,
                                                                                          },
                                                                                      )
                                                                                    : t(
                                                                                          'authorNormalization.reopenedNone',
                                                                                          'Nothing was reopened: either no failed row matches this stage and class, or the matching rows have already spent their attempt budget.',
                                                                                      ),
                                                                            );
                                                                            return fetchCurrent();
                                                                        }),
                                                                );
                                                            }}
                                                        >
                                                            {t(
                                                                'authorNormalization.retry',
                                                                'Retry',
                                                            )}
                                                        </Button>
                                                    </div>
                                                </div>
                                            )}
                                            {run.mode === 'pilot_archive' &&
                                                run.status === 'completed' &&
                                                !run.approved_for_full && (
                                                    <Button
                                                        size="sm"
                                                        disabled={actionsBlocked}
                                                        onClick={() =>
                                                            withBusy(() =>
                                                                adminApi
                                                                    .approveAuthorMetadataFullRun(
                                                                        run.id,
                                                                    )
                                                                    .then((data) =>
                                                                        applyRun(data.run),
                                                                    ),
                                                            )
                                                        }
                                                    >
                                                        {t(
                                                            'authorNormalization.approveFull',
                                                            'Approve full run',
                                                        )}
                                                    </Button>
                                                )}
                                            {run.mode === 'pilot_archive' &&
                                                run.status === 'completed' &&
                                                run.approved_for_full && (
                                                    <p className="self-center text-sm text-muted-foreground">
                                                        {t(
                                                            'authorNormalization.approvedFull',
                                                            'Full run approved',
                                                        )}
                                                    </p>
                                                )}
                                        </div>
                                    )}
                                </section>
                            </CardContent>
                        </Card>
                    )}

                    {report !== null && (
                        <Card>
                            <CardContent className="flex flex-col gap-3">
                                <div className="flex flex-wrap items-center gap-2">
                                    <h3 className="text-base font-medium">
                                        {t('authorNormalization.report.title', 'Report')}
                                    </h3>
                                    <Badge variant={report.ready ? 'default' : 'secondary'}>
                                        {report.ready
                                            ? t('authorNormalization.report.ready', 'Ready')
                                            : t('authorNormalization.report.notReady', 'Not ready')}
                                    </Badge>
                                </div>
                                {!report.ready && report.not_ready_reasons.length > 0 && (
                                    <div className="flex flex-col gap-1">
                                        <span className="text-sm text-muted-foreground">
                                            {t(
                                                'authorNormalization.report.notReadyReasons',
                                                'Not ready because',
                                            )}
                                            :
                                        </span>
                                        <ul className="ml-4 list-disc text-sm">
                                            {report.not_ready_reasons.map((reason) => (
                                                <li key={reason}>{reason}</li>
                                            ))}
                                        </ul>
                                    </div>
                                )}
                                <div className="grid gap-1 sm:grid-cols-2">
                                    <Stat
                                        label={t('authorNormalization.report.duration', 'Duration')}
                                        value={report.duration_s}
                                    />
                                    <Stat
                                        label={t(
                                            'authorNormalization.report.dbGrowth',
                                            'Database growth, bytes',
                                        )}
                                        value={report.db_growth_bytes}
                                    />
                                </div>
                                <div className="grid gap-3 sm:grid-cols-2">
                                    <div className="flex flex-col gap-1">
                                        <h4 className="text-sm font-medium">
                                            {t(
                                                'authorNormalization.report.byClass',
                                                'By decision class',
                                            )}
                                        </h4>
                                        {Object.entries(report.by_class).map(([cls, count]) => (
                                            <Stat key={cls} label={cls} value={count} />
                                        ))}
                                    </div>
                                    <div className="flex flex-col gap-1">
                                        <h4 className="text-sm font-medium">
                                            {t('authorNormalization.report.byScript', 'By script')}
                                        </h4>
                                        {Object.entries(report.by_script).map(([script, count]) => (
                                            <Stat key={script} label={script} value={count} />
                                        ))}
                                    </div>
                                </div>
                            </CardContent>
                        </Card>
                    )}

                    <Card>
                        <CardContent className="flex flex-col gap-3">
                            <h3 className="text-base font-medium">
                                {t('authorNormalization.startTitle', 'Start a run')}
                            </h3>
                            <div
                                role="group"
                                aria-label={t('authorNormalization.startMode', 'Mode')}
                                className="flex flex-wrap gap-2"
                            >
                                {MODES.map((candidate) => (
                                    <Button
                                        key={candidate}
                                        variant={mode === candidate ? 'default' : 'outline'}
                                        size="sm"
                                        aria-pressed={mode === candidate}
                                        onClick={() => {
                                            setMode(candidate);
                                            setFormError(null);
                                        }}
                                    >
                                        {t(
                                            `authorNormalization.mode.${candidate}`,
                                            MODE_FALLBACKS[candidate],
                                        )}
                                    </Button>
                                ))}
                            </div>

                            {mode === 'smoke' && (
                                <Field
                                    id="author-norm-book-ids"
                                    label={t('authorNormalization.form.bookIds', 'Book IDs')}
                                    hint={t(
                                        'authorNormalization.form.bookIdsHint',
                                        'Comma or whitespace separated, up to 10000, unique, positive.',
                                    )}
                                    error={formError ?? undefined}
                                >
                                    <Textarea
                                        id="author-norm-book-ids"
                                        value={bookIdsText}
                                        onChange={(event) => setBookIdsText(event.target.value)}
                                    />
                                </Field>
                            )}
                            {mode === 'pilot_archive' && (
                                <Field
                                    id="author-norm-archive"
                                    label={t('authorNormalization.form.archive', 'Archive name')}
                                    error={formError ?? undefined}
                                >
                                    <Input
                                        id="author-norm-archive"
                                        value={archiveName}
                                        onChange={(event) => setArchiveName(event.target.value)}
                                    />
                                </Field>
                            )}
                            {mode === 'full' && (
                                <p className="text-sm text-muted-foreground">
                                    {fullAllowed
                                        ? t(
                                              'authorNormalization.form.fullReady',
                                              'The approved completed pilot allows a full run.',
                                          )
                                        : t(
                                              'authorNormalization.form.fullHint',
                                              'A full run needs an approved completed pilot run.',
                                          )}
                                </p>
                            )}
                            {run !== null && ACTIVE_STATUSES.has(run.status) && (
                                <p className="text-sm text-muted-foreground">
                                    {t(
                                        'authorNormalization.form.activeRunHint',
                                        'A run is already active.',
                                    )}
                                </p>
                            )}

                            <div>
                                <Button onClick={handleStart} disabled={startDisabled}>
                                    {t('authorNormalization.start', 'Start')}
                                </Button>
                            </div>
                        </CardContent>
                    </Card>
                </TabsContent>
                <TabsContent value="review">
                    <AuthorReviewQueue />
                </TabsContent>
            </Tabs>
        </div>
    );
};

export default AuthorNormalization;
