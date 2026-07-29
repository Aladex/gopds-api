import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertCircle, CheckCircle2, Info } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import { Field } from '@/shared/ui/field';
import { Input } from '@/shared/ui/input';
import { Progress } from '@/shared/ui/progress';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/ui/table';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { cn } from '@/shared/lib/utils';
import * as adminApi from '@/api/admin';
import { isApiError } from '@/api/errors';
import { WS_URL } from '@/api/config';

interface DuplicateGroup {
    md5_hash: string;
    count: number;
    book_ids: number[];
    example_titles: string[];
}

/** ActiveScan is the row GET /admin/duplicates/scan/active returns. */
interface ActiveScan {
    id: number;
    status: string;
    processed_books: number;
    total_books: number;
    duplicates_found: number;
    error?: string;
}

interface ScanProgress {
    job_id: number;
    status: string;
    processed_books: number;
    total_books: number;
    duplicates_found: number;
    error?: string;
}

/**
 * The palette has one destructive colour and no warning colour, so the two
 * buttons MUI drew in amber borrow the shade the rescan dialog already uses for
 * "this is reversible, but think first". They stay outlined rather than filled:
 * an amber fill next to the red Force stop reads as two levels of danger where
 * there is only one.
 */
const WARNING_BUTTON = 'border-amber-500/60 text-amber-600 dark:text-amber-400';

/** Every action button is full width on a phone and content width above it. */
const ACTION_BUTTON = 'w-full sm:w-auto sm:min-w-30';

const Duplicates: React.FC = () => {
    const { t } = useTranslation();
    // Below this the three columns stop fitting and each group becomes a
    // card, the same width the other admin tables change at.
    const isMobile = useMediaQuery('(max-width: 899px)');

    const [groups, setGroups] = useState<DuplicateGroup[]>([]);
    const [isLoading, setIsLoading] = useState(false);
    const [isScanning, setIsScanning] = useState(false);
    const [scanProgress, setScanProgress] = useState<ScanProgress | null>(null);
    const [scanError, setScanError] = useState<string | null>(null);
    const [actionResult, setActionResult] = useState<string | null>(null);
    const [statusMessage, setStatusMessage] = useState<string | null>(null);
    const [workerCount, setWorkerCount] = useState<number>(1);
    const wsRef = useRef<WebSocket | null>(null);

    // A division and a rounding, read straight into a progress bar: memoising it
    // costs more bookkeeping than the arithmetic it saves.
    const progressPercent =
        !scanProgress || scanProgress.total_books === 0
            ? 0
            : Math.min(
                  100,
                  Math.round((scanProgress.processed_books / scanProgress.total_books) * 100),
              );

    const fetchGroups = useCallback(async () => {
        setIsLoading(true);
        setActionResult(null);
        try {
            const data = await adminApi.listDuplicates<{ groups?: DuplicateGroup[] }>();
            setGroups(data?.groups || []);
        } catch (error) {
            console.error(error);
        } finally {
            setIsLoading(false);
        }
    }, []);

    const fetchActiveScan = useCallback(async () => {
        try {
            const data = await adminApi.getActiveDuplicateScan<
                ActiveScan | { status: 'none' } | null
            >();
            if (!data || data.status === 'none') {
                setIsScanning(false);
                setScanProgress(null);
                setStatusMessage(null);
                return;
            }
            const scan = data as ActiveScan;
            setScanProgress({
                job_id: scan.id,
                status: scan.status,
                processed_books: scan.processed_books,
                total_books: scan.total_books,
                duplicates_found: scan.duplicates_found,
                error: scan.error,
            });
            setIsScanning(scan.status === 'running' || scan.status === 'pending');
        } catch (error) {
            console.error(error);
        }
    }, []);

    const handleStartScan = useCallback(async () => {
        setScanError(null);
        setActionResult(null);
        setStatusMessage(null);
        try {
            const started = await adminApi.startDuplicateScan<{ job_id?: number }>({
                workers: workerCount,
            });
            const jobId = started?.job_id;
            if (jobId) {
                setScanProgress({
                    job_id: jobId,
                    status: 'pending',
                    processed_books: 0,
                    total_books: 0,
                    duplicates_found: 0,
                });
                setIsScanning(true);
            }
        } catch (error) {
            // The API layer rejects with ApiError, which carries the status
            // directly. The old check read error.response.status — an axios
            // shape this client stopped producing, so "a scan is already
            // running" was silently reported as a generic failure.
            if (isApiError(error) && error.status === 409) {
                setStatusMessage(t('scanAlreadyRunning'));
                await fetchActiveScan();
                return;
            }
            console.error(error);
            setScanError(t('scanError'));
        }
    }, [fetchActiveScan, t, workerCount]);

    const handleHideDuplicates = useCallback(async () => {
        setActionResult(null);
        const confirmed = window.confirm(t('hideDuplicatesConfirm'));
        if (!confirmed) {
            return;
        }
        try {
            const result = await adminApi.hideDuplicates<{
                hidden_count?: number;
                skipped_empty?: number;
            }>(undefined);
            const hiddenCount = result?.hidden_count ?? 0;
            const skippedEmpty = result?.skipped_empty ?? 0;
            setActionResult(
                t('hideDuplicatesResult', { hidden: hiddenCount, skipped: skippedEmpty }),
            );
            await fetchGroups();
        } catch (error) {
            console.error(error);
            setActionResult(t('hideDuplicatesError'));
        }
    }, [fetchGroups, t]);

    const handleStopScan = useCallback(async () => {
        if (!scanProgress) {
            return;
        }
        setActionResult(null);
        try {
            await adminApi.stopDuplicateScan(scanProgress.job_id);
            setStatusMessage(t('scanStopRequested'));
            setIsScanning(false);
            await fetchActiveScan();
        } catch (error) {
            console.error(error);
            setScanError(t('scanStopError'));
        }
    }, [fetchActiveScan, scanProgress, t]);

    const handleForceStopScan = useCallback(async () => {
        if (!scanProgress) {
            return;
        }
        const confirmed = window.confirm(t('forceStopConfirm'));
        if (!confirmed) {
            return;
        }
        setActionResult(null);
        try {
            await adminApi.forceStopDuplicateScan(scanProgress.job_id);
            setStatusMessage(t('scanForceStopRequested'));
            setIsScanning(false);
            await fetchActiveScan();
        } catch (error) {
            console.error(error);
            setScanError(t('scanForceStopError'));
        }
    }, [fetchActiveScan, scanProgress, t]);

    useEffect(() => {
        fetchGroups();
        fetchActiveScan();
    }, [fetchGroups, fetchActiveScan]);

    useEffect(() => {
        const ws = new WebSocket(`${WS_URL}/api/ws`);
        wsRef.current = ws;

        ws.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data);
                if (message.type !== 'duplicate_scan_progress') {
                    return;
                }
                const payload = message.data as ScanProgress;
                setScanProgress(payload);
                if (payload.status === 'completed' || payload.status === 'failed') {
                    setIsScanning(false);
                    fetchGroups();
                    if (payload.status === 'failed') {
                        setScanError(payload.error || t('scanError'));
                    }
                } else {
                    setIsScanning(true);
                }
            } catch (error) {
                console.error('Failed to parse WebSocket message', error);
            }
        };

        ws.onerror = (error) => {
            console.error('Admin WebSocket error', error);
        };

        ws.onclose = () => {
            wsRef.current = null;
        };

        return () => {
            if (wsRef.current) {
                wsRef.current.close();
                wsRef.current = null;
            }
        };
    }, [fetchGroups, t]);

    return (
        <div className="flex flex-col gap-4">
            <h2 className="text-center text-lg font-medium">{t('duplicates')}</h2>

            <Field
                id="duplicates-workers"
                label={t('workers')}
                hint={t('workersHint')}
                className="max-w-40"
            >
                <Input
                    id="duplicates-workers"
                    type="number"
                    min={1}
                    max={8}
                    value={workerCount}
                    onChange={(event) => {
                        const next = Number(event.target.value);
                        if (!Number.isNaN(next)) {
                            setWorkerCount(Math.max(1, Math.min(8, next)));
                        }
                    }}
                />
            </Field>

            <div className="flex flex-wrap gap-2">
                <Button className={ACTION_BUTTON} onClick={handleStartScan} disabled={isScanning}>
                    {t('startScan')}
                </Button>
                <Button
                    variant="outline"
                    className={cn(ACTION_BUTTON, WARNING_BUTTON)}
                    onClick={handleStopScan}
                    disabled={!isScanning}
                >
                    {t('stopScan')}
                </Button>
                <Button
                    variant="destructive"
                    className={ACTION_BUTTON}
                    onClick={handleForceStopScan}
                    disabled={!scanProgress}
                >
                    {t('forceStopScan')}
                </Button>
                <Button variant="outline" className={ACTION_BUTTON} onClick={fetchActiveScan}>
                    {t('getStatus')}
                </Button>
                <Button
                    variant="outline"
                    className={ACTION_BUTTON}
                    onClick={fetchGroups}
                    disabled={isLoading}
                >
                    {t('refresh')}
                </Button>
                <Button
                    variant="outline"
                    className={cn(ACTION_BUTTON, WARNING_BUTTON)}
                    onClick={handleHideDuplicates}
                    disabled={isScanning}
                >
                    {t('hideDuplicates')}
                </Button>
            </div>

            <Card>
                <CardContent className="flex flex-col gap-2">
                    <h3 className="text-base font-medium">{t('scanProgress')}</h3>
                    {scanProgress ? (
                        <>
                            <p className="text-sm">
                                {t('scanStatus')}: {scanProgress.status}
                            </p>
                            <p className="text-sm tabular-nums">
                                {t('processedBooks', {
                                    processed: scanProgress.processed_books,
                                    total: scanProgress.total_books,
                                })}
                            </p>
                            <p className="text-sm tabular-nums">
                                {t('duplicatesFound', { count: scanProgress.duplicates_found })}
                            </p>
                            <Progress value={progressPercent} aria-label={t('scanProgress')} />
                        </>
                    ) : (
                        <p className="text-sm text-muted-foreground">{t('scanNotStarted')}</p>
                    )}

                    {statusMessage && (
                        <Alert>
                            <Info className="size-4" />
                            <AlertDescription>{statusMessage}</AlertDescription>
                        </Alert>
                    )}
                    {scanError && (
                        <Alert variant="destructive">
                            <AlertCircle className="size-4" />
                            <AlertDescription>{scanError}</AlertDescription>
                        </Alert>
                    )}
                    {actionResult && (
                        <Alert>
                            <CheckCircle2 className="size-4" />
                            <AlertDescription>{actionResult}</AlertDescription>
                        </Alert>
                    )}
                </CardContent>
            </Card>

            <Card>
                <CardContent className="flex flex-col gap-2">
                    <h3 className="text-base font-medium">{t('duplicateGroups')}</h3>
                    {groups.length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t('noDuplicateGroups')}</p>
                    ) : isMobile ? (
                        /* An MD5 is thirty-two characters and the titles run
                           longer still; three columns of that on a phone is a
                           sideways scroll, so each group becomes a card. */
                        <div className="flex flex-col gap-3">
                            {groups.map((group) => (
                                <div
                                    key={group.md5_hash}
                                    className="flex flex-col gap-2 rounded border border-border p-3"
                                >
                                    <div className="flex items-baseline justify-between gap-2">
                                        <span className="text-xs uppercase tracking-wide text-muted-foreground">
                                            {t('count')}
                                        </span>
                                        <span className="font-medium tabular-nums">
                                            {group.count}
                                        </span>
                                    </div>
                                    <div className="flex flex-col gap-1">
                                        <span className="text-xs uppercase tracking-wide text-muted-foreground">
                                            {t('hash')}
                                        </span>
                                        <span className="font-mono text-xs break-all">
                                            {group.md5_hash}
                                        </span>
                                    </div>
                                    <div className="flex flex-col gap-1">
                                        <span className="text-xs uppercase tracking-wide text-muted-foreground">
                                            {t('exampleTitles')}
                                        </span>
                                        <span className="text-sm">
                                            {group.example_titles?.length
                                                ? group.example_titles.join(', ')
                                                : '—'}
                                        </span>
                                    </div>
                                </div>
                            ))}
                        </div>
                    ) : (
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t('hash')}</TableHead>
                                    <TableHead className="text-right">{t('count')}</TableHead>
                                    <TableHead>{t('exampleTitles')}</TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {groups.map((group) => (
                                    <TableRow key={group.md5_hash}>
                                        <TableCell className="font-mono text-xs">
                                            {group.md5_hash}
                                        </TableCell>
                                        <TableCell className="text-right tabular-nums">
                                            {group.count}
                                        </TableCell>
                                        <TableCell>
                                            {group.example_titles?.length
                                                ? group.example_titles.join(', ')
                                                : '—'}
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    )}
                </CardContent>
            </Card>
        </div>
    );
};

export default Duplicates;
