import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import { Progress } from '@/shared/ui/progress';
import * as adminApi from '@/api/admin';
import type { AuthorMetadataReport, AuthorMetadataRun } from '@/api/admin';
import { closedErrorCode } from '@/api/admin';

/**
 * The author metadata card of the scanning section: one pass over the
 * catalogue, with no modes, versions or report. The one button follows the
 * server's gate for a full run: without an approved pilot of the current
 * versions it first checks one archive (the smallest non-empty one), and once
 * that check is complete and clean the same button approves it and walks the
 * whole catalogue. A few books that can never be read (at most 1% of the run)
 * do not block it; more than that leaves the button disabled with its reason,
 * and "Retry errors" is the way forward. When the server refuses a
 * full run because the approved check was made by older versions, the same
 * button starts a new check.
 *
 * Every control acts only on data it can trust — a run id JSON parsing has
 * not rounded, a known status, the report of that very run — and only once the
 * state it acts on is confirmed: after an action the controls stay disabled
 * until the following read succeeds, and a failed read withdraws them until
 * "Refresh" or the next poll reads the state again.
 */

/** How often the card re-reads an active run. */
const POLL_INTERVAL_MS = 15000;

const ACTIVE = new Set(['pending', 'running', 'paused']);

const KNOWN_STATUSES: ReadonlySet<string> = new Set([...ACTIVE, 'completed', 'failed_systemic']);

const KNOWN_MODES: ReadonlySet<string> = new Set(['smoke', 'pilot_archive', 'full']);

/** Only a run whose id JSON parsing has not rounded, with a known status and mode, may be acted on. */
export const isUsableRun = (run: AuthorMetadataRun): boolean =>
    Number.isSafeInteger(run.id) &&
    run.id > 0 &&
    KNOWN_STATUSES.has(run.status) &&
    KNOWN_MODES.has(run.mode);

/** The extraction statuses that count as a book the pass could not read. */
const EXTRACTION_FAILURES = [
    'entry_missing',
    'invalid_fb2',
    'unsupported_encoding',
    'metadata_parse_failed',
] as const;

/** Fallbacks for the closed reasons a check is not clean, and for error codes. */
const NOT_READY_FALLBACKS: ReadonlyMap<string, string> = new Map([
    ['run_not_completed', 'the check has not finished'],
    ['extraction_pending', 'some books are still waiting'],
    ['credits_pending', 'some authors are still being processed'],
    ['errors', 'some books could not be read'],
    ['other', 'the check is not ready'],
]);

/** A reason outside the closed set is shown as a generic one, never verbatim. */
const closedReason = (reason: string): string =>
    NOT_READY_FALLBACKS.has(reason) ? reason : 'other';

interface ArchiveInfo {
    name: string;
    books_count: number;
}

/** The archive a check runs on: the smallest non-empty one, ties by name. */
export function pilotArchive(archives: ArchiveInfo[]): string | null {
    const candidates = archives
        .filter((a) => a.books_count > 0)
        .sort((a, b) => a.books_count - b.books_count || a.name.localeCompare(b.name));
    return candidates.length > 0 ? candidates[0].name : null;
}

/** Books the run could not read, in either stage. */
export function runErrors(run: AuthorMetadataRun): number {
    const byStatus = run.stages.extraction.by_status;
    return (
        EXTRACTION_FAILURES.reduce((sum, status) => sum + (byStatus[status] ?? 0), 0) +
        run.stages.local.failed
    );
}

/**
 * Whether the run's failed books are few enough not to block it: at most 1%
 * of its books, rounded down. A broken FB2 never reads, so demanding zero would
 * block the catalogue forever; a systematic failure (a whole charset refused)
 * is well above this and still stops the operator.
 */
export function errorsTolerable(run: AuthorMetadataRun): boolean {
    return runErrors(run) <= Math.floor(run.stages.extraction.total / 100);
}

export type CardState =
    | { kind: 'idle' }
    | { kind: 'active'; run: AuthorMetadataRun }
    | { kind: 'checkReady'; run: AuthorMetadataRun; report: AuthorMetadataReport }
    | { kind: 'checkNotReady'; run: AuthorMetadataRun; reasons: string[] }
    | { kind: 'approved'; run: AuthorMetadataRun }
    | { kind: 'done'; run: AuthorMetadataRun }
    | { kind: 'notDone'; run: AuthorMetadataRun; reasons: string[] };

/**
 * What the card shows for the newest run and its report. A report that is not
 * that run's own counts as missing; outdatedPilot names an approved pilot the
 * server refused a full run on, which needs a new check.
 */
export function cardState(
    run: AuthorMetadataRun | null,
    runReport: AuthorMetadataReport | null,
    outdatedPilot: number | null = null,
): CardState {
    if (run === null) {
        return { kind: 'idle' };
    }
    const report = runReport !== null && runReport.id === run.id ? runReport : null;
    if (ACTIVE.has(run.status)) {
        return { kind: 'active', run };
    }
    if (run.status !== 'completed') {
        // Ended failed_systemic: start over with a check.
        return { kind: 'idle' };
    }
    const reasons =
        report === null
            ? ['run_not_completed']
            : [...new Set(report.not_ready_reasons.map(closedReason))];
    if (!errorsTolerable(run) && !reasons.includes('errors')) {
        reasons.push('errors');
    }
    const clean = report !== null && report.ready && errorsTolerable(run);
    if (run.mode === 'full') {
        return clean ? { kind: 'done', run } : { kind: 'notDone', run, reasons };
    }
    if (run.mode === 'pilot_archive') {
        if (run.approved_for_full) {
            return run.id === outdatedPilot ? { kind: 'idle' } : { kind: 'approved', run };
        }
        return clean && report !== null
            ? { kind: 'checkReady', run, report }
            : { kind: 'checkNotReady', run, reasons };
    }
    // A smoke run: start over with a check.
    return { kind: 'idle' };
}

const formatBytes = (bytes: number): string => {
    if (bytes < 1024 * 1024) {
        return `${Math.max(0, Math.round(bytes / 1024))} KB`;
    }
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
};

const AuthorMetadataCard: React.FC = () => {
    const { t } = useTranslation();
    const [run, setRun] = useState<AuthorMetadataRun | null>(null);
    const [report, setReport] = useState<AuthorMetadataReport | null>(null);
    const [phase, setPhase] = useState<'loading' | 'ready' | 'failed'>('loading');
    const [busy, setBusy] = useState(false);
    const [errorText, setErrorText] = useState<string | null>(null);
    const [loadErrorText, setLoadErrorText] = useState<string | null>(null);
    const [notice, setNotice] = useState<string | null>(null);
    const [outdatedPilot, setOutdatedPilot] = useState<number | null>(null);
    const generation = useRef(0);

    const describeError = useCallback(
        (error: unknown): string => {
            const code = closedErrorCode(error);
            if (code === 'active_run_exists') {
                return t('authorMetadataCard.activeRunExists', 'A pass is already running.');
            }
            return t('authorMetadataCard.actionFailed', 'The action failed.');
        },
        [t],
    );

    /** Reads the newest run and, for a finished usable one, its report. */
    const load = useCallback(async () => {
        const current = ++generation.current;
        try {
            const { run: latest } = await adminApi.getLatestAuthorMetadataRun();
            let latestReport: AuthorMetadataReport | null = null;
            if (latest !== null && isUsableRun(latest) && !ACTIVE.has(latest.status)) {
                latestReport = (await adminApi.getAuthorMetadataRunReport(latest.id)).report;
            }
            if (current !== generation.current) {
                return;
            }
            setRun(latest);
            setReport(latestReport);
            setLoadErrorText(null);
            setPhase('ready');
        } catch {
            if (current !== generation.current) {
                return;
            }
            setLoadErrorText(
                t('authorMetadataCard.loadFailed', 'Could not load the author metadata status.'),
            );
            setPhase('failed');
        }
    }, [t]);

    useEffect(() => {
        load();
    }, [load]);

    const invalid = run !== null && !isUsableRun(run);
    const state = invalid ? ({ kind: 'idle' } as const) : cardState(run, report, outdatedPilot);
    const active = state.kind === 'active';
    useEffect(() => {
        if (!active) {
            return undefined;
        }
        const timer = window.setInterval(load, POLL_INTERVAL_MS);
        return () => window.clearInterval(timer);
    }, [active, load]);

    /**
     * Runs one action, then reads the state again; the controls stay disabled
     * until that read has settled, so nothing acts twice on a state the card
     * has not confirmed.
     */
    const act = async (
        action: () => Promise<string | null>,
        approvedPilot: number | null = null,
    ) => {
        setBusy(true);
        setErrorText(null);
        setNotice(null);
        try {
            setNotice(await action());
        } catch (error) {
            if (approvedPilot !== null && closedErrorCode(error) === 'full_run_not_approved') {
                // The approval is for older versions: the next press starts a new check.
                setOutdatedPilot(approvedPilot);
                setNotice(
                    t(
                        'authorMetadataCard.outdatedCheck',
                        'The check was made by an older version. The button starts a new check.',
                    ),
                );
            } else {
                setErrorText(describeError(error));
            }
        } finally {
            await load();
            setBusy(false);
        }
    };

    const startCheck = () =>
        act(async () => {
            const data = await adminApi.listScannedArchives<{ scanned_archives?: ArchiveInfo[] }>();
            const archive = pilotArchive(data?.scanned_archives ?? []);
            if (archive === null) {
                return t(
                    'authorMetadataCard.noArchive',
                    'There is no scanned archive with books yet.',
                );
            }
            await adminApi.startAuthorMetadataRun({ mode: 'pilot_archive', archive });
            return null;
        });

    const startFull = (pilot: AuthorMetadataRun, approve: boolean) =>
        act(async () => {
            if (approve) {
                await adminApi.approveAuthorMetadataFullRun(pilot.id);
            }
            await adminApi.startAuthorMetadataRun({ mode: 'full' });
            return null;
        }, pilot.id);

    const pauseOrResume = (current: AuthorMetadataRun) =>
        act(async () => {
            if (current.status === 'paused') {
                await adminApi.resumeAuthorMetadataRun(current.id);
            } else {
                await adminApi.pauseAuthorMetadataRun(current.id);
            }
            return null;
        });

    /** Retries every retryable class of both stages on the newest run. */
    const retryErrors = (current: AuthorMetadataRun) =>
        act(async () => {
            let reopened = 0;
            for (const errorClass of adminApi.AUTHOR_METADATA_EXTRACTION_RETRY_CLASSES) {
                reopened += (
                    await adminApi.retryAuthorMetadataRun(current.id, {
                        stage: 'extraction',
                        error_class: errorClass,
                    })
                ).reopened;
            }
            for (const errorClass of adminApi.AUTHOR_METADATA_LOCAL_RETRY_CLASSES) {
                reopened += (
                    await adminApi.retryAuthorMetadataRun(current.id, {
                        stage: 'local',
                        error_class: errorClass,
                    })
                ).reopened;
            }
            return t('authorMetadataCard.retried', {
                defaultValue: 'Books queued again: {{count}}.',
                count: reopened,
            });
        });

    const reasonText = (reasons: string[]) =>
        reasons
            .map((reason) =>
                t(
                    `authorMetadataCard.reason.${closedReason(reason)}`,
                    NOT_READY_FALLBACKS.get(closedReason(reason)) ?? '',
                ),
            )
            .join(', ');

    const shown = state.kind === 'idle' ? null : state.run;
    const controls = phase === 'ready' && !invalid;
    const extraction = shown?.stages.extraction;

    return (
        <Card>
            <CardContent className="flex flex-col gap-3">
                <h3 className="text-base font-medium">
                    {t('authorMetadataCard.title', 'Author metadata')}
                </h3>

                {phase === 'loading' && (
                    <p className="text-sm text-muted-foreground">{t('loading', 'Loading...')}</p>
                )}
                {loadErrorText !== null && (
                    <Alert variant="destructive">
                        <AlertDescription>{loadErrorText}</AlertDescription>
                    </Alert>
                )}
                {errorText !== null && (
                    <Alert variant="destructive">
                        <AlertDescription>{errorText}</AlertDescription>
                    </Alert>
                )}
                {phase === 'ready' && invalid && (
                    <p role="note" className="text-sm text-muted-foreground">
                        {t(
                            'authorMetadataCard.invalidData',
                            'The run data from the server is invalid; actions are disabled.',
                        )}
                    </p>
                )}
                {notice !== null && (
                    <p role="status" className="text-sm text-muted-foreground">
                        {notice}
                    </p>
                )}

                {controls && shown !== null && extraction !== undefined && (
                    <div className="flex flex-col gap-2">
                        <p className="text-sm">
                            {shown.mode === 'pilot_archive'
                                ? t('authorMetadataCard.checking', 'Check on one archive')
                                : t('authorMetadataCard.catalogue', 'The whole catalogue')}
                            {extraction.current_archive !== null && active && (
                                <span className="text-muted-foreground break-all">
                                    {' '}
                                    · {extraction.current_archive}
                                </span>
                            )}
                        </p>
                        <p className="text-sm tabular-nums">
                            {t('authorMetadataCard.processed', {
                                defaultValue: 'Processed {{done}} of {{total}} books',
                                done: extraction.done,
                                total: extraction.total,
                            })}
                        </p>
                        <Progress
                            value={
                                extraction.total > 0
                                    ? Math.round((extraction.done * 100) / extraction.total)
                                    : 0
                            }
                        />
                        <p className="text-sm tabular-nums text-muted-foreground">
                            {t('authorMetadataCard.errors', {
                                defaultValue: 'Errors: {{count}}',
                                count: runErrors(shown),
                            })}
                        </p>
                    </div>
                )}

                {controls && state.kind === 'done' && (
                    <p className="text-sm font-medium">{t('authorMetadataCard.done', 'Done')}</p>
                )}
                {controls && state.kind === 'checkReady' && (
                    <p className="text-sm text-muted-foreground">
                        {runErrors(state.run) === 0
                            ? t('authorMetadataCard.checkReady', {
                                  defaultValue:
                                      'The check is clean: {{done}} books, no errors, {{growth}} added. Walk the whole catalogue now.',
                                  done: state.run.stages.extraction.done,
                                  growth: formatBytes(state.report.db_growth_bytes),
                              })
                            : t('authorMetadataCard.checkReadyWithErrors', {
                                  defaultValue:
                                      'The check passed: {{done}} books, errors: {{errors}}, {{growth}} added. Walk the whole catalogue now.',
                                  done: state.run.stages.extraction.done,
                                  errors: runErrors(state.run),
                                  growth: formatBytes(state.report.db_growth_bytes),
                              })}
                    </p>
                )}
                {controls && (state.kind === 'checkNotReady' || state.kind === 'notDone') && (
                    <p className="text-sm text-muted-foreground">
                        {t('authorMetadataCard.notReady', {
                            defaultValue: 'Not finished: {{reasons}}.',
                            reasons: reasonText(state.reasons),
                        })}
                    </p>
                )}
                {controls && state.kind === 'idle' && (
                    <p className="text-sm text-muted-foreground">
                        {t(
                            'authorMetadataCard.idleHint',
                            'New books get their author metadata when they are scanned. The pass reads the books scanned before, after a check on the smallest archive.',
                        )}
                    </p>
                )}

                <div className="flex flex-wrap gap-2">
                    {controls && state.kind === 'active' ? (
                        <Button
                            variant="outline"
                            disabled={busy}
                            onClick={() => pauseOrResume(state.run)}
                        >
                            {state.run.status === 'paused'
                                ? t('authorMetadataCard.resume', 'Resume')
                                : t('authorMetadataCard.pause', 'Pause')}
                        </Button>
                    ) : controls && state.kind !== 'done' ? (
                        <Button
                            disabled={
                                busy || state.kind === 'checkNotReady' || state.kind === 'notDone'
                            }
                            onClick={() => {
                                if (state.kind === 'checkReady') {
                                    startFull(state.run, true);
                                } else if (state.kind === 'approved') {
                                    startFull(state.run, false);
                                } else {
                                    startCheck();
                                }
                            }}
                        >
                            {t('authorMetadataCard.walk', 'Walk the catalogue')}
                        </Button>
                    ) : null}
                    {controls && shown !== null && !active && runErrors(shown) > 0 && (
                        <Button
                            variant="outline"
                            disabled={busy}
                            onClick={() => retryErrors(shown)}
                        >
                            {t('authorMetadataCard.retryErrors', 'Retry errors')}
                        </Button>
                    )}
                    {phase !== 'loading' && (
                        <Button
                            variant="ghost"
                            disabled={busy}
                            onClick={() => {
                                setErrorText(null);
                                load();
                            }}
                        >
                            {t('authorMetadataCard.refresh', 'Refresh')}
                        </Button>
                    )}
                </div>
            </CardContent>
        </Card>
    );
};

export default AuthorMetadataCard;
