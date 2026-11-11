import React, { useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { Progress } from '@/shared/ui/progress';
import * as adminApi from '@/api/admin';
import type {
    AuthorMetadataProblemArchive,
    AuthorMetadataReport,
    AuthorMetadataRun,
} from '@/api/admin';
import { closedErrorCode } from '@/api/admin';
import { ACTIVE_METADATA, isUsableRun, runErrors } from '@/features/admin/scan/metadataRun';
import { useScanSource } from '@/features/admin/scan/ScanSources';
import Fold from '@/features/admin/scan/Fold';
import { formatDay } from '@/features/admin/scan/scanUi';

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
 *
 * A full run answers its start at once and is seeded by the server in the
 * background: the card shows the preparation progress until it runs. An
 * archive the pass found missing or unreadable does not stop it; the card
 * lists such archives with "Retry" (after the file came back) and "Delete
 * book records" (confirmed first). A run the volume paused says why.
 */

const ACTIVE = ACTIVE_METADATA;

/** A stable empty list, so a card without problem archives keeps one identity. */
const NO_ARCHIVES: AuthorMetadataProblemArchive[] = [];

// Re-exported: the card's tests and callers have always read them from here.
export { isUsableRun, runErrors };

/** Fallbacks for the closed reasons of a problem archive. */
const ARCHIVE_REASON_FALLBACKS: ReadonlyMap<string, string> = new Map([
    ['archive_missing', 'the archive is missing'],
    ['archive_unreadable', 'the archive does not open'],
]);

/** Fallbacks for the closed reasons a check is not clean, and for error codes. */
const NOT_READY_FALLBACKS: ReadonlyMap<string, string> = new Map([
    ['run_not_completed', 'the check has not finished'],
    ['extraction_pending', 'some books are still waiting'],
    ['credits_pending', 'some authors are still being processed'],
    ['figures_pending', 'the final figures are being counted'],
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
    | { kind: 'finalizing'; run: AuthorMetadataRun }
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
    if (report !== null && report.not_ready_reasons.includes('figures_pending')) {
        // The server is counting the run's final figures: nothing is decided
        // from the ones it had before, and the card reads again until they land.
        return { kind: 'finalizing', run };
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

interface AuthorMetadataCardProps {
    /**
     * card: the card on its own, with its title. panel: the body of the scan
     * tab's «Книги» panel, its pass folded under «Проход по каталогу».
     */
    variant?: 'card' | 'panel';
}

const AuthorMetadataCard: React.FC<AuthorMetadataCardProps> = ({ variant = 'card' }) => {
    const { t } = useTranslation();
    // The newest run, its report and its problem archives come from the
    // tab's shared source, which re-reads every 5 s while anything is on its
    // way: an active pass, a completed run's final figures, a deletion of an
    // archive's records.
    const source = useScanSource('metadataRun');
    const run = source.data?.run ?? null;
    const report = source.data?.report ?? null;
    const archives = source.data?.archives ?? NO_ARCHIVES;
    const load = source.reload;
    const phase: 'loading' | 'ready' | 'failed' = source.failed
        ? 'failed'
        : source.data !== null
          ? 'ready'
          : 'loading';
    const loadErrorText = source.failed
        ? t('authorMetadataCard.loadFailed', 'Could not load the author metadata status.')
        : null;
    const [busy, setBusy] = useState(false);
    const [errorText, setErrorText] = useState<string | null>(null);
    const [notice, setNotice] = useState<string | null>(null);
    const [outdatedPilot, setOutdatedPilot] = useState<number | null>(null);
    const [pendingDelete, setPendingDelete] = useState<AuthorMetadataProblemArchive | null>(null);

    const describeError = useCallback(
        (error: unknown): string => {
            const code = closedErrorCode(error);
            if (code === 'active_run_exists') {
                return t('authorMetadataCard.activeRunExists', 'A pass is already running.');
            }
            if (code === 'not_a_completed_pilot') {
                // The server accounts the check's credits when it approves:
                // some became pending since the figures the card shows.
                return t(
                    'authorMetadataCard.pilotNotSettled',
                    'Some authors of the check are being processed again. Approve it once they are done.',
                );
            }
            return t('authorMetadataCard.actionFailed', 'The action failed.');
        },
        [t],
    );

    const invalid = run !== null && !isUsableRun(run);
    const state = invalid ? ({ kind: 'idle' } as const) : cardState(run, report, outdatedPilot);
    const active = state.kind === 'active';

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

    const retryArchive = (current: AuthorMetadataRun, archive: string) =>
        act(async () => {
            const { reopened } = await adminApi.retryAuthorMetadataArchive(current.id, archive);
            return t('authorMetadataCard.retried', {
                defaultValue: 'Books queued again: {{count}}.',
                count: reopened,
            });
        });

    const confirmDelete = (current: AuthorMetadataRun) => {
        const target = pendingDelete;
        setPendingDelete(null);
        if (target === null) {
            return;
        }
        act(async () => {
            const { deletion } = await adminApi.deleteAuthorMetadataArchive(
                current.id,
                target.archive,
            );
            return t('authorMetadataCard.archiveDeleting', {
                defaultValue: 'Deleting the book records of {{archive}}: {{count}} books.',
                archive: deletion.archive,
                count: deletion.books_total,
            });
        });
    };

    const archiveReason = (reason: string) => {
        const closed = ARCHIVE_REASON_FALLBACKS.has(reason) ? reason : 'archive_unreadable';
        return t(
            `authorMetadataCard.archiveReason.${closed}`,
            ARCHIVE_REASON_FALLBACKS.get(closed) ?? '',
        );
    };

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
    // A pending run is being prepared: the server is still adding its books.
    const preparing = state.kind === 'active' && state.run.status === 'pending';
    const seeding = preparing ? state.run.seeding : null;

    const statusLines = (
        <>
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
        </>
    );
    const passLines = (
        <>
            {controls && preparing && (
                <div className="flex flex-col gap-2">
                    <p className="text-sm tabular-nums">
                        {seeding !== null
                            ? t('authorMetadataCard.preparing', {
                                  defaultValue: 'Preparing: {{seeded}} of {{target}} books',
                                  seeded: seeding.seeded,
                                  target: seeding.target,
                              })
                            : t('authorMetadataCard.preparingShort', 'Preparing…')}
                    </p>
                    <Progress
                        value={
                            seeding !== null && seeding.target > 0
                                ? Math.min(100, Math.round((seeding.seeded * 100) / seeding.target))
                                : 0
                        }
                    />
                </div>
            )}

            {controls && !preparing && shown !== null && extraction !== undefined && (
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
        </>
    );
    const volumeLine = (
        <>
            {controls &&
                shown !== null &&
                shown.status === 'paused' &&
                shown.last_error_class === 'archive_unreadable' && (
                    <p className="text-sm">
                        {t(
                            'authorMetadataCard.pausedVolume',
                            'Paused: the books volume could not be read. Continue once it is back.',
                        )}
                    </p>
                )}
        </>
    );
    const archiveList = (
        <>
            {controls && shown !== null && archives.length > 0 && (
                <div className="flex flex-col gap-2">
                    <h4 className="text-sm font-medium">
                        {t('authorMetadataCard.problemArchives', 'Problem archives')}
                    </h4>
                    <ul className="flex flex-col gap-3">
                        {archives.map((archive) => (
                            <li
                                key={`${archive.archive}:${archive.reason}`}
                                className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between"
                            >
                                <div className="min-w-0">
                                    <p className="text-sm font-medium break-all">
                                        {archive.archive}
                                    </p>
                                    <p className="text-xs tabular-nums text-muted-foreground">
                                        {t('authorMetadataCard.archiveLine', {
                                            defaultValue: '{{books}} books · {{reason}}',
                                            books: archive.books,
                                            reason: archiveReason(archive.reason),
                                        })}
                                    </p>
                                </div>
                                {archive.deletion !== null ? (
                                    <p className="text-sm tabular-nums sm:shrink-0">
                                        {t('authorMetadataCard.archiveDeletionProgress', {
                                            defaultValue: 'Deleting: {{deleted}} of {{total}}',
                                            deleted: archive.deletion.deleted,
                                            total: archive.deletion.total,
                                        })}
                                    </p>
                                ) : (
                                    <div className="flex flex-wrap gap-2 sm:shrink-0">
                                        <Button
                                            size="sm"
                                            variant="outline"
                                            disabled={busy}
                                            aria-label={t('authorMetadataCard.retryArchiveLabel', {
                                                defaultValue: 'Retry {{archive}}',
                                                archive: archive.archive,
                                            })}
                                            onClick={() => retryArchive(shown, archive.archive)}
                                        >
                                            {t('authorMetadataCard.retryArchive', 'Retry')}
                                        </Button>
                                        <Button
                                            size="sm"
                                            variant="destructive"
                                            disabled={busy}
                                            aria-label={t('authorMetadataCard.deleteArchiveLabel', {
                                                defaultValue: 'Delete book records of {{archive}}',
                                                archive: archive.archive,
                                            })}
                                            onClick={() => setPendingDelete(archive)}
                                        >
                                            {t(
                                                'authorMetadataCard.deleteArchive',
                                                'Delete book records',
                                            )}
                                        </Button>
                                    </div>
                                )}
                            </li>
                        ))}
                    </ul>
                </div>
            )}
        </>
    );
    const outcomeLines = (
        <>
            {controls && state.kind === 'done' && (
                <p className="text-sm font-medium">{t('authorMetadataCard.done', 'Done')}</p>
            )}
            {controls && state.kind === 'finalizing' && (
                <p role="status" className="text-sm text-muted-foreground">
                    {t('authorMetadataCard.finalizing', 'Counting the final figures…')}
                </p>
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
        </>
    );
    const buttons = (
        <div className="flex flex-wrap gap-2">
            {controls && preparing ? null : controls && state.kind === 'active' ? (
                <Button variant="outline" disabled={busy} onClick={() => pauseOrResume(state.run)}>
                    {state.run.status === 'paused'
                        ? t('authorMetadataCard.resume', 'Resume')
                        : t('authorMetadataCard.pause', 'Pause')}
                </Button>
            ) : controls && state.kind !== 'done' && state.kind !== 'finalizing' ? (
                <Button
                    disabled={busy || state.kind === 'checkNotReady' || state.kind === 'notDone'}
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
            {controls &&
                shown !== null &&
                !active &&
                state.kind !== 'finalizing' &&
                runErrors(shown) > 0 && (
                    <Button variant="outline" disabled={busy} onClick={() => retryErrors(shown)}>
                        {t('authorMetadataCard.retryErrors', 'Retry errors')}
                    </Button>
                )}
            {phase !== 'loading' && (variant === 'card' || phase === 'failed') && (
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
    );
    const deleteDialog = (
        <Dialog
            open={pendingDelete !== null}
            onOpenChange={(open) => !open && setPendingDelete(null)}
        >
            <DialogContent closeLabel={t('close')}>
                <DialogHeader>
                    <DialogTitle>
                        {t('authorMetadataCard.deleteArchiveTitle', 'Delete the book records?')}
                    </DialogTitle>
                    <DialogDescription>
                        {t('authorMetadataCard.deleteArchiveBody', {
                            defaultValue:
                                'The catalogue forgets the {{books}} books of {{archive}} with their author metadata. The archive file is not touched; scanning it again brings the books back.',
                            books: pendingDelete?.books ?? 0,
                            archive: pendingDelete?.archive ?? '',
                        })}
                    </DialogDescription>
                </DialogHeader>
                <DialogFooter>
                    <Button variant="ghost" onClick={() => setPendingDelete(null)}>
                        {t('cancel')}
                    </Button>
                    <Button
                        variant="destructive"
                        disabled={busy || shown === null}
                        onClick={() => shown !== null && confirmDelete(shown)}
                    >
                        {t('authorMetadataCard.deleteArchiveConfirm', 'Delete records')}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );

    if (variant === 'panel') {
        // The stage panel: what needs a hand stays on top, the pass itself
        // (a migration-era tool) is folded unless one is on its way.
        const following = state.kind === 'active' || state.kind === 'finalizing';
        return (
            <div className="flex min-w-0 flex-col gap-3">
                {statusLines}
                {volumeLine}
                {archiveList}
                <Fold
                    label={
                        <>
                            {t('scanPanel.books.pass')}
                            <span className="font-normal text-muted-foreground">
                                {' · '}
                                {t('scanPanel.books.passTag')}
                            </span>
                        </>
                    }
                    meta={
                        shown === null
                            ? null
                            : shown.completed_at
                              ? `#${shown.id} · ${formatDay(shown.completed_at)}`
                              : `#${shown.id}`
                    }
                    defaultOpen={following}
                >
                    {passLines}
                    {outcomeLines}
                    {buttons}
                </Fold>
                {deleteDialog}
            </div>
        );
    }

    return (
        <Card>
            <CardContent className="flex flex-col gap-3">
                <h3 className="text-base font-medium">
                    {t('authorMetadataCard.title', 'Author metadata')}
                </h3>
                {statusLines}
                {passLines}
                {volumeLine}
                {archiveList}
                {outcomeLines}
                {buttons}
            </CardContent>
            {deleteDialog}
        </Card>
    );
};

export default AuthorMetadataCard;
