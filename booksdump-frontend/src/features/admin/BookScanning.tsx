import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { AlertCircle, RefreshCw, RotateCcw, Trash2 } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Badge } from '@/shared/ui/badge';
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
import { Field } from '@/shared/ui/field';
import { Progress } from '@/shared/ui/progress';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/shared/ui/select';
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from '@/shared/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/ui/tabs';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { cn } from '@/shared/lib/utils';
import * as adminApi from '@/api/admin';
import { isApiError } from '@/api/errors';
import { WS_URL } from '@/api/config';

interface ScanStatusResponse {
    is_running: boolean;
    session_id?: string;
    total_archives: number;
    archives_processed: number;
    current_archive?: string;
    total_books: number;
    total_errors: number;
    progress_percent: number;
    started_at?: string;
    elapsed_seconds: number;
    finished_at?: string;
    last_error?: string;
}

interface UnscannedArchiveInfo {
    name: string;
    size_mb: number;
    file_count: number;
    created_date: string;
}

interface ScanErrorItem {
    file_name: string;
    archive_name: string;
    error: string;
    timestamp: string;
}

interface ScannedArchiveInfo {
    name: string;
    books_count: number;
    errors_count: number;
    scanned_at: string;
}

interface StartScanResponse {
    session_id: string;
    started_at: string;
}

interface ScanStartedEvent {
    total_archives: number;
    timestamp: string;
}

interface ArchiveStartedEvent {
    archive_name: string;
    timestamp: string;
}

interface BookProcessedEvent {
    archive_name: string;
    book_title: string;
    book_id: number;
    timestamp: string;
}

interface ArchiveCompletedEvent {
    archive_name: string;
    books_count: number;
    errors_count: number;
    duration_ms: number;
    timestamp: string;
}

interface ScanCompletedEvent {
    total_archives: number;
    total_books: number;
    total_errors: number;
    duration_ms: number;
    timestamp: string;
}

interface ScanErrorEvent {
    message: string;
    timestamp: string;
}

interface ScanProgressEvent {
    current_archive: string;
    archives_processed: number;
    total_archives: number;
    books_processed: number;
    total_books: number;
    progress_percent: number;
    elapsed_seconds: number;
    timestamp: string;
}

interface FixScanStatusResponse {
    is_running: boolean;
    session_id?: string;
    total_books: number;
    books_processed: number;
    books_updated: number;
    total_archives: number;
    current_archive?: string;
    error_count: number;
    progress_percent: number;
    started_at?: string;
    elapsed_seconds: number;
    finished_at?: string;
    last_error?: string;
}

interface FixScanStartedEvent {
    scan_type: string;
    total_books: number;
    total_archives: number;
    timestamp: string;
}

interface FixScanProgressEvent {
    scan_type: string;
    current_archive: string;
    books_processed: number;
    total_books: number;
    books_updated: number;
    error_count: number;
    progress_percent: number;
    elapsed_seconds: number;
    timestamp: string;
}

interface FixScanCompletedEvent {
    scan_type: string;
    total_books: number;
    updated_books: number;
    total_archives: number;
    error_count: number;
    duration_ms: number;
    timestamp: string;
}

interface FixScanErrorEvent {
    scan_type: string;
    message: string;
    timestamp: string;
}

/** The two archive lists, keyed rather than indexed so the tab reads as itself. */
type ArchiveTab = 'unscanned' | 'scanned';

/**
 * The palette has no warning colour, so Reset — destructive but reversible —
 * borrows the amber the rescan dialog already uses. Delete keeps the one real
 * destructive colour, which is the point of having only one.
 */
const WARNING_BUTTON = 'border-amber-500/60 text-amber-600 dark:text-amber-400';

/** A labelled figure with its bar, used by both the scan and fix-scan cards. */
const ProgressRow: React.FC<{ label: string; percent: number }> = ({ label, percent }) => (
    <div className="flex flex-col gap-1">
        <div className="flex items-center justify-between">
            <span className="text-sm text-muted-foreground">{label}</span>
            <span className="text-sm font-semibold tabular-nums">{Math.round(percent)}%</span>
        </div>
        <Progress value={Math.min(100, percent)} aria-label={label} />
    </div>
);

const BookScanning: React.FC = () => {
    const { t } = useTranslation();
    // Below this the five-column archive tables stop fitting and each archive
    // becomes a card. It is the width MUI called `md`.
    const isMobile = useMediaQuery('(max-width: 899px)');
    const [status, setStatus] = useState<ScanStatusResponse | null>(null);
    const [unscannedArchives, setUnscannedArchives] = useState<UnscannedArchiveInfo[]>([]);
    const [scannedArchives, setScannedArchives] = useState<ScannedArchiveInfo[]>([]);
    const [scannedTotalCount, setScannedTotalCount] = useState(0);
    const [scanErrors, setScanErrors] = useState<ScanErrorItem[]>([]);
    const [selectedErrorIndex, setSelectedErrorIndex] = useState<number>(-1);
    const [isLoading, setIsLoading] = useState(false);
    const [isLoadingScanned, setIsLoadingScanned] = useState(false);
    const [statusMessage, setStatusMessage] = useState<string | null>(null);
    const [scanError, setScanError] = useState<string | null>(null);
    const [lastBookTitle, setLastBookTitle] = useState<string | null>(null);
    const [currentTab, setCurrentTab] = useState<ArchiveTab>('unscanned');
    const [rescanDialogOpen, setRescanDialogOpen] = useState(false);
    const [archiveToRescan, setArchiveToRescan] = useState<string | null>(null);
    const [isRescanning, setIsRescanning] = useState(false);
    const [rescanProgress, setRescanProgress] = useState<ScanStatusResponse | null>(null);
    const [fixScanStatus, setFixScanStatus] = useState<FixScanStatusResponse | null>(null);
    const [isFixScanning, setIsFixScanning] = useState(false);
    const wsRef = useRef<WebSocket | null>(null);
    const scannedIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
    const rescanPollingRef = useRef<ReturnType<typeof setInterval> | null>(null);

    /** progressPercent prefers the server's own figure, falling back to the counts. */
    const percentFromStatus = () => {
        if (!status || status.total_archives === 0) {
            return 0;
        }
        if (status.progress_percent) {
            return Math.min(100, status.progress_percent);
        }
        return Math.min(100, Math.round((status.archives_processed / status.total_archives) * 100));
    };
    const progressPercent = percentFromStatus();

    const fetchStatus = useCallback(async () => {
        try {
            const data = await adminApi.getScanStatus<ScanStatusResponse>();
            setStatus(data);
            setScanError(data?.last_error || null);
        } catch (error) {
            console.error(error);
        }
    }, []);

    const fetchUnscanned = useCallback(async () => {
        setIsLoading(true);
        try {
            const data = await adminApi.listUnscannedArchives<{
                unscanned_archives?: UnscannedArchiveInfo[];
            }>();
            setUnscannedArchives(data?.unscanned_archives || []);
        } catch (error) {
            console.error(error);
        } finally {
            setIsLoading(false);
        }
    }, []);

    const fetchScanned = useCallback(async () => {
        setIsLoadingScanned(true);
        try {
            const data = await adminApi.listScannedArchives<{
                scanned_archives?: ScannedArchiveInfo[];
                total_count?: number;
            }>();
            setScannedArchives(data?.scanned_archives || []);
            setScannedTotalCount(data?.total_count || 0);
        } catch (error) {
            console.error(error);
        } finally {
            setIsLoadingScanned(false);
        }
    }, []);

    const fetchErrors = useCallback(async () => {
        try {
            const data = await adminApi.listScanErrors<{ errors?: ScanErrorItem[] }>();
            setScanErrors(data?.errors || []);
            setSelectedErrorIndex(-1);
        } catch (error) {
            console.error(error);
        }
    }, []);

    const fetchFixScanStatus = useCallback(async () => {
        try {
            const data = await adminApi.getFixScanStatus<FixScanStatusResponse>();
            if (data?.is_running) {
                setIsFixScanning(true);
                setFixScanStatus(data);
            }
        } catch (error) {
            console.error(error);
        }
    }, []);

    const formatScannedDate = useCallback((dateString: string): string => {
        const date = new Date(dateString);
        return date.toLocaleDateString('en-US', {
            month: 'short',
            day: 'numeric',
            year: 'numeric',
            hour: '2-digit',
            minute: '2-digit',
        });
    }, []);

    const handleStartScan = useCallback(async () => {
        setStatusMessage(null);
        setScanError(null);
        try {
            const started = await adminApi.startScan<{ started_at?: string; session_id?: string }>();
            const startedAt = started?.started_at;
            setStatus((prev) => ({
                is_running: true,
                session_id: started?.session_id,
                total_archives: prev?.total_archives ?? 0,
                archives_processed: 0,
                current_archive: '',
                total_books: 0,
                total_errors: 0,
                progress_percent: 0,
                started_at: startedAt,
                elapsed_seconds: 0,
            }));
            setStatusMessage(t('bookScanStarted'));
        } catch (error) {
            // The API layer rejects with ApiError, which carries the status
            // directly. The old check read error.response.status — an axios
            // shape this client stopped producing, so "already running" was
            // reported as a generic failure.
            if (isApiError(error) && error.status === 409) {
                setStatusMessage(t('bookScanAlreadyRunning'));
                await fetchStatus();
                return;
            }
            console.error(error);
            setScanError(t('bookScanStartError'));
        }
    }, [fetchStatus, t]);

    const handleScanArchive = useCallback(
        async (name: string) => {
            setStatusMessage(null);
            setScanError(null);
            try {
                await adminApi.scanArchive({ name });
                setStatusMessage(t('bookScanStarted'));
            } catch (error) {
                console.error(error);
                setScanError(t('bookScanArchiveError', { name }));
            }
        },
        [t],
    );

    const handleRescanClick = useCallback((archiveName: string) => {
        setArchiveToRescan(archiveName);
        setRescanDialogOpen(true);
    }, []);

    const handleResetArchive = useCallback(
        async (archiveName: string) => {
            if (!window.confirm(t('bookScanResetConfirm', { name: archiveName }))) {
                return;
            }
            try {
                await adminApi.resetArchive(archiveName, false);
                toast.success(t('bookScanResetSuccess', { name: archiveName }));
                await fetchScanned();
                await fetchStatus();
                await fetchUnscanned();
            } catch (error) {
                console.error(error);
                toast.error(t('bookScanResetError'));
            }
        },
        [fetchScanned, fetchStatus, fetchUnscanned, t],
    );

    const handleDeleteArchiveBooks = useCallback(
        async (archiveName: string) => {
            if (!window.confirm(`${t('bookScanDeleteBooksConfirm')}\n${archiveName}`)) {
                return;
            }
            try {
                await adminApi.resetArchive(archiveName, true);
                toast.success(t('bookScanResetSuccess', { name: archiveName }));
                await fetchScanned();
                await fetchStatus();
            } catch (error) {
                console.error(error);
                toast.error(t('bookScanResetError'));
            }
        },
        [fetchScanned, fetchStatus, t],
    );

    const handleDownloadErrorFile = useCallback(
        async (item: ScanErrorItem) => {
            try {
                // The endpoint already answers with a Blob; wrapping it in
                // another Blob only copied the bytes.
                const blob = await adminApi.getScanErrorFile(item.archive_name, item.file_name);
                const url = window.URL.createObjectURL(blob);
                const link = document.createElement('a');
                const parts = item.file_name.split('/');
                link.href = url;
                link.download = parts[parts.length - 1] || 'scan_error_file';
                document.body.appendChild(link);
                link.click();
                link.remove();
                window.URL.revokeObjectURL(url);
            } catch (error) {
                console.error(error);
                setScanError(t('bookScanErrorDownloadError'));
            }
        },
        [t],
    );

    const handleRescanDialogClose = useCallback(() => {
        setRescanDialogOpen(false);
        setArchiveToRescan(null);
    }, []);

    const handleRescanConfirm = useCallback(async () => {
        if (!archiveToRescan) return;

        setIsRescanning(true);

        try {
            // Start async rescan
            const startResponse = await adminApi.scanArchive<StartScanResponse>({
                name: archiveToRescan,
            });

            if (!startResponse?.session_id) {
                throw new Error('No session_id received from server');
            }

            // Initialize rescanProgress with session_id so we can track completion
            setRescanProgress({
                is_running: true,
                session_id: startResponse.session_id,
                total_archives: 1,
                archives_processed: 0,
                current_archive: archiveToRescan,
                total_books: 0,
                total_errors: 0,
                progress_percent: 0,
                elapsed_seconds: 0,
            });

            // Wait for scan to complete via WebSocket events
            // WebSocket handler will automatically update rescanProgress
            await new Promise<void>((resolve, reject) => {
                const maxWaitTime = 300000; // 5 minutes timeout

                const timeoutId = setTimeout(() => {
                    reject(new Error('Rescan timeout - operation took too long'));
                }, maxWaitTime);

                // Check status periodically (WebSocket updates rescanProgress)
                const intervalId = setInterval(() => {
                    // Read the latest state through the setter, which is the
                    // only way to see it from inside a closure this old.
                    setRescanProgress((current) => {
                        if (current && !current.is_running && current.progress_percent >= 100) {
                            clearTimeout(timeoutId);
                            clearInterval(intervalId);
                            resolve();
                        }
                        return current;
                    });
                }, 500);

                rescanPollingRef.current = intervalId;
            });

            // Get final status for results message
            const finalStatus = await adminApi.getScanStatus<ScanStatusResponse>();
            if (finalStatus) {
                toast.success(
                    t('bookScanRescanResult', {
                        name: archiveToRescan,
                        books: finalStatus.total_books,
                        errors: finalStatus.total_errors,
                    }),
                );
            }

            // Refresh scanned archives list
            await fetchScanned();
            await fetchStatus();
        } catch (error) {
            console.error('Rescan error:', error);
            toast.error(
                error instanceof Error
                    ? error.message
                    : t('bookScanArchiveError', { name: archiveToRescan }),
            );
        } finally {
            setIsRescanning(false);
            setRescanProgress(null);
            setArchiveToRescan(null);
            setRescanDialogOpen(false);

            if (rescanPollingRef.current) {
                clearInterval(rescanPollingRef.current);
                rescanPollingRef.current = null;
            }
        }
    }, [archiveToRescan, fetchScanned, fetchStatus, t]);

    const handleStartFixScan = useCallback(async () => {
        setStatusMessage(null);
        setScanError(null);
        try {
            await adminApi.startFixScan({ workers: 4 });
            setIsFixScanning(true);
            setFixScanStatus({
                is_running: true,
                total_books: 0,
                books_processed: 0,
                books_updated: 0,
                total_archives: 0,
                error_count: 0,
                progress_percent: 0,
                elapsed_seconds: 0,
            });
            toast.success(t('bookScanStarted'));
        } catch (error) {
            if (isApiError(error) && error.status === 409) {
                toast.info(t('fixScanAlreadyRunning'));
                return;
            }
            console.error(error);
            setScanError(t('bookScanStartError'));
        }
    }, [t]);

    const handleCancelFixScan = useCallback(async () => {
        try {
            await adminApi.cancelFixScan();
            toast.success(t('scanStopRequested'));
        } catch (error) {
            console.error(error);
            setScanError(t('scanStopError'));
        }
    }, [t]);

    useEffect(() => {
        fetchStatus();
        fetchUnscanned();
        fetchScanned();
        fetchErrors();
        fetchFixScanStatus();
    }, [fetchErrors, fetchFixScanStatus, fetchStatus, fetchUnscanned, fetchScanned]);

    // Auto-refresh scanned archives every 30 seconds when on scanned tab
    // (WebSocket handles real-time updates, this is just a fallback)
    useEffect(() => {
        if (currentTab === 'scanned') {
            fetchScanned();
            scannedIntervalRef.current = setInterval(() => {
                fetchScanned();
            }, 30000); // Reduced frequency since WebSocket handles updates
        }

        return () => {
            if (scannedIntervalRef.current) {
                clearInterval(scannedIntervalRef.current);
                scannedIntervalRef.current = null;
            }
        };
    }, [currentTab, fetchScanned]);

    useEffect(() => {
        const ws = new WebSocket(`${WS_URL}/api/ws`);
        wsRef.current = ws;

        ws.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data);
                switch (message.type) {
                    case 'scan_started': {
                        const payload = message.data as ScanStartedEvent;
                        setStatus((prev) => ({
                            is_running: true,
                            session_id: prev?.session_id,
                            total_archives: payload.total_archives,
                            archives_processed: 0,
                            current_archive: '',
                            total_books: 0,
                            total_errors: 0,
                            progress_percent: 0,
                            started_at: payload.timestamp,
                            elapsed_seconds: 0,
                        }));
                        setStatusMessage(t('bookScanStarted'));
                        setScanError(null);
                        break;
                    }
                    case 'archive_started': {
                        const payload = message.data as ArchiveStartedEvent;
                        setStatus((prev) =>
                            prev ? { ...prev, current_archive: payload.archive_name } : prev,
                        );
                        break;
                    }
                    case 'book_processed': {
                        const payload = message.data as BookProcessedEvent;
                        setLastBookTitle(payload.book_title);
                        setStatus((prev) =>
                            prev ? { ...prev, total_books: prev.total_books + 1 } : prev,
                        );
                        break;
                    }
                    case 'archive_completed': {
                        const payload = message.data as ArchiveCompletedEvent;
                        setStatus((prev) =>
                            prev
                                ? {
                                      ...prev,
                                      archives_processed: prev.archives_processed + 1,
                                      total_errors: prev.total_errors + payload.errors_count,
                                      current_archive: '',
                                  }
                                : prev,
                        );

                        // Update scanned archives list when an archive completes
                        fetchScanned();
                        break;
                    }
                    case 'scan_completed': {
                        const payload = message.data as ScanCompletedEvent;
                        setStatus((prev) => ({
                            is_running: false,
                            session_id: prev?.session_id,
                            total_archives: payload.total_archives,
                            archives_processed: payload.total_archives,
                            current_archive: '',
                            total_books: payload.total_books,
                            total_errors: payload.total_errors,
                            progress_percent: 100,
                            started_at: prev?.started_at,
                            elapsed_seconds: prev?.elapsed_seconds ?? 0,
                            finished_at: payload.timestamp,
                        }));

                        // Also mark rescanProgress as completed
                        setRescanProgress((prev) =>
                            prev
                                ? {
                                      ...prev,
                                      is_running: false,
                                      progress_percent: 100,
                                      archives_processed: payload.total_archives,
                                      total_books: payload.total_books,
                                      total_errors: payload.total_errors,
                                  }
                                : prev,
                        );

                        setStatusMessage(t('bookScanCompleted'));
                        fetchUnscanned();
                        fetchScanned();
                        fetchErrors();
                        break;
                    }
                    case 'scan_progress': {
                        const payload = message.data as ScanProgressEvent;
                        const patch = {
                            current_archive: payload.current_archive,
                            archives_processed: payload.archives_processed,
                            total_archives: payload.total_archives,
                            total_books: payload.books_processed,
                            progress_percent: payload.progress_percent,
                            elapsed_seconds: payload.elapsed_seconds,
                        };
                        setStatus((prev) => (prev ? { ...prev, ...patch } : prev));
                        // Also update rescanProgress if the rescan dialog is open
                        setRescanProgress((prev) => (prev ? { ...prev, ...patch } : prev));
                        break;
                    }
                    case 'scan_error': {
                        const payload = message.data as ScanErrorEvent;
                        setScanError(payload.message || t('bookScanError'));
                        break;
                    }
                    case 'fix_scan_started': {
                        const payload = message.data as FixScanStartedEvent;
                        setIsFixScanning(true);
                        setFixScanStatus({
                            is_running: true,
                            total_books: payload.total_books,
                            books_processed: 0,
                            books_updated: 0,
                            total_archives: payload.total_archives,
                            error_count: 0,
                            progress_percent: 0,
                            started_at: payload.timestamp,
                            elapsed_seconds: 0,
                        });
                        break;
                    }
                    case 'fix_scan_progress': {
                        const payload = message.data as FixScanProgressEvent;
                        setIsFixScanning(true);
                        setFixScanStatus((prev) => ({
                            is_running: true,
                            total_books: payload.total_books || prev?.total_books || 0,
                            total_archives: prev?.total_archives ?? 0,
                            started_at: prev?.started_at,
                            current_archive: payload.current_archive,
                            books_processed: payload.books_processed,
                            books_updated: payload.books_updated,
                            error_count: payload.error_count,
                            progress_percent: payload.progress_percent,
                            elapsed_seconds: payload.elapsed_seconds,
                        }));
                        break;
                    }
                    case 'fix_scan_completed': {
                        const payload = message.data as FixScanCompletedEvent;
                        setIsFixScanning(false);
                        toast.success(
                            t('fixScanCompleted', {
                                updated: payload.updated_books,
                                total: payload.total_books,
                            }),
                        );
                        setTimeout(() => {
                            setFixScanStatus(null);
                        }, 3000);
                        break;
                    }
                    case 'fix_scan_error': {
                        const payload = message.data as FixScanErrorEvent;
                        toast.error(payload.message || t('scanError'));
                        break;
                    }
                    default:
                        break;
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
    }, [fetchErrors, fetchUnscanned, fetchScanned, t]);

    const selectedError = selectedErrorIndex >= 0 ? scanErrors[selectedErrorIndex] : undefined;

    /** The three per-archive actions, shared by the card and the table row. */
    const archiveActions = (archive: ScannedArchiveInfo) => (
        <div className="flex gap-2">
            <Button
                size="icon-sm"
                onClick={() => handleRescanClick(archive.name)}
                title={t('refresh')}
                aria-label={`${t('refresh')}: ${archive.name}`}
            >
                <RefreshCw className="size-4" />
            </Button>
            <Button
                variant="outline"
                size="icon-sm"
                className={WARNING_BUTTON}
                onClick={() => handleResetArchive(archive.name)}
                title={t('bookScanResetTitle')}
                aria-label={`${t('bookScanResetTitle')}: ${archive.name}`}
            >
                <RotateCcw className="size-4" />
            </Button>
            <Button
                variant="destructive"
                size="icon-sm"
                onClick={() => handleDeleteArchiveBooks(archive.name)}
                title={t('bookScanDeleteBooks')}
                aria-label={`${t('bookScanDeleteBooks')}: ${archive.name}`}
            >
                <Trash2 className="size-4" />
            </Button>
        </div>
    );

    return (
        <div className="flex flex-col gap-4">
            <h2 className="text-center text-lg font-medium">{t('bookScanning')}</h2>

            <div className="flex flex-wrap gap-2">
                <Button onClick={handleStartScan} disabled={status?.is_running || isFixScanning}>
                    {t('bookScanStart')}
                </Button>
                <Button
                    variant="secondary"
                    onClick={handleStartFixScan}
                    disabled={status?.is_running || isFixScanning}
                >
                    {t('fixScanStart')}
                </Button>
                <Button variant="outline" onClick={fetchStatus}>
                    {t('bookScanStatusRefresh')}
                </Button>
            </div>

            <Card>
                <CardContent className="flex flex-col gap-2">
                    <h3 className="text-base font-medium">{t('bookScanStatusTitle')}</h3>
                    {status ? (
                        <>
                            <p className="text-sm">
                                {t('bookScanStatus')}:{' '}
                                {status.is_running ? t('bookScanRunning') : t('bookScanIdle')}
                            </p>
                            <p className="text-sm tabular-nums">
                                {t('bookScanArchivesProcessed', {
                                    processed: status.archives_processed,
                                    total: status.total_archives,
                                })}
                            </p>
                            <p className="text-sm tabular-nums">
                                {t('bookScanBooksProcessed', { count: status.total_books })}
                            </p>
                            <p className="text-sm tabular-nums">
                                {t('bookScanErrors', { count: status.total_errors })}
                            </p>
                            {status.current_archive && (
                                <p className="text-sm break-words">
                                    {t('bookScanCurrentArchive')}: {status.current_archive}
                                </p>
                            )}
                            {lastBookTitle && (
                                <p className="text-sm break-words">
                                    {t('bookScanLastBook')}: {lastBookTitle}
                                </p>
                            )}
                            <ProgressRow label={t('bookScanProgress')} percent={progressPercent} />
                        </>
                    ) : (
                        <p className="text-sm text-muted-foreground">{t('bookScanNotStarted')}</p>
                    )}

                    {statusMessage && (
                        <p className="text-sm text-muted-foreground">{statusMessage}</p>
                    )}
                    {scanError && (
                        <Alert variant="destructive">
                            <AlertCircle className="size-4" />
                            <AlertDescription>{scanError}</AlertDescription>
                        </Alert>
                    )}
                </CardContent>
            </Card>

            {(isFixScanning || fixScanStatus) && (
                <Card>
                    <CardContent className="flex flex-col gap-2">
                        <h3 className="text-base font-medium">{t('fixScanStatusTitle')}</h3>
                        {fixScanStatus && (
                            <>
                                <p className="text-sm tabular-nums">
                                    {t('fixScanBooksProcessed', {
                                        processed: fixScanStatus.books_processed,
                                        total: fixScanStatus.total_books,
                                    })}
                                </p>
                                <p className="text-sm tabular-nums">
                                    {t('fixScanBooksUpdated', { count: fixScanStatus.books_updated })}
                                </p>
                                <p className="text-sm tabular-nums">
                                    {t('fixScanErrors', { count: fixScanStatus.error_count })}
                                </p>
                                {fixScanStatus.current_archive && (
                                    <p className="text-sm break-words">
                                        {t('bookScanCurrentArchive')}: {fixScanStatus.current_archive}
                                    </p>
                                )}
                                <ProgressRow
                                    label={t('bookScanProgress')}
                                    percent={fixScanStatus.progress_percent}
                                />
                                {isFixScanning && (
                                    <Button
                                        variant="outline"
                                        size="sm"
                                        className="mt-2 self-start text-destructive hover:text-destructive"
                                        onClick={handleCancelFixScan}
                                    >
                                        {t('cancel')}
                                    </Button>
                                )}
                            </>
                        )}
                    </CardContent>
                </Card>
            )}

            <Card>
                <CardContent className="flex flex-col gap-3">
                    <div className="flex flex-wrap items-center gap-3">
                        <h3 className="text-base font-medium">{t('bookScanErrorsTitle')}</h3>
                        <Button variant="outline" size="sm" onClick={fetchErrors}>
                            {t('bookScanErrorsRefresh')}
                        </Button>
                    </div>

                    {scanErrors.length === 0 ? (
                        <p className="text-sm text-muted-foreground">{t('bookScanNoErrors')}</p>
                    ) : (
                        <>
                            <Field
                                id="scan-error-select"
                                label={t('bookScanErrorsSelect')}
                                className="max-w-80"
                            >
                                <Select
                                    value={selectedErrorIndex >= 0 ? String(selectedErrorIndex) : ''}
                                    onValueChange={(value) => setSelectedErrorIndex(Number(value))}
                                >
                                    <SelectTrigger id="scan-error-select" className="w-full">
                                        <SelectValue
                                            placeholder={t('bookScanErrorsSelectPlaceholder')}
                                        />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {scanErrors.map((item, index) => (
                                            <SelectItem
                                                key={`${item.archive_name}-${item.file_name}-${index}`}
                                                value={String(index)}
                                            >
                                                {item.archive_name} / {item.file_name}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </Field>

                            {selectedError && (
                                <div className="flex flex-col gap-1">
                                    <p className="text-sm break-words">
                                        {t('bookScanErrorArchive')}: {selectedError.archive_name}
                                    </p>
                                    <p className="text-sm break-words">
                                        {t('bookScanErrorFile')}: {selectedError.file_name}
                                    </p>
                                    <p className="text-sm break-words">
                                        {t('bookScanErrorMessage')}: {selectedError.error}
                                    </p>
                                    <p className="text-sm text-muted-foreground">
                                        {t('bookScanErrorTime')}:{' '}
                                        {new Date(selectedError.timestamp).toLocaleString()}
                                    </p>
                                    <Button
                                        variant="outline"
                                        size="sm"
                                        className="mt-2 self-start"
                                        onClick={() => handleDownloadErrorFile(selectedError)}
                                    >
                                        {t('bookScanErrorDownload')}
                                    </Button>
                                </div>
                            )}
                        </>
                    )}
                </CardContent>
            </Card>

            <Card>
                <CardContent>
                    <Tabs
                        value={currentTab}
                        onValueChange={(value) => setCurrentTab(value as ArchiveTab)}
                    >
                        <TabsList>
                            <TabsTrigger value="unscanned" className="flex-1 sm:flex-none">
                                {t('bookScanUnscannedTitle')}
                            </TabsTrigger>
                            <TabsTrigger value="scanned" className="flex-1 sm:flex-none">
                                {t('bookScanScannedTitle')}
                            </TabsTrigger>
                        </TabsList>

                        <TabsContent value="unscanned" className="flex flex-col gap-3">
                            <div className="flex flex-wrap items-center justify-between gap-2">
                                <span className="text-sm text-muted-foreground tabular-nums">
                                    {t('bookScanTotalArchives', { count: unscannedArchives.length })}
                                </span>
                                <Button
                                    variant="outline"
                                    size="sm"
                                    onClick={fetchUnscanned}
                                    disabled={isLoading}
                                >
                                    {t('bookScanUnscannedRefresh')}
                                </Button>
                            </div>

                            {unscannedArchives.length === 0 ? (
                                <p className="text-sm text-muted-foreground">
                                    {t('bookScanNoUnscanned')}
                                </p>
                            ) : isMobile ? (
                                <div className="flex flex-col gap-3">
                                    {unscannedArchives.map((archive) => (
                                        <div
                                            key={archive.name}
                                            className="flex flex-col gap-2 rounded-lg border border-border p-3"
                                        >
                                            <p className="text-sm font-semibold break-words">
                                                {archive.name}
                                            </p>
                                            <div className="flex flex-col gap-0.5 text-xs text-muted-foreground">
                                                <span>
                                                    {t('bookScanSize')}:{' '}
                                                    <strong className="tabular-nums">
                                                        {archive.size_mb} MB
                                                    </strong>
                                                </span>
                                                <span>
                                                    {t('bookScanFileCount')}:{' '}
                                                    <strong className="tabular-nums">
                                                        {archive.file_count}
                                                    </strong>
                                                </span>
                                                <span>
                                                    {t('bookScanCreated')}:{' '}
                                                    <strong>
                                                        {new Date(
                                                            archive.created_date,
                                                        ).toLocaleString()}
                                                    </strong>
                                                </span>
                                            </div>
                                            <Button
                                                variant="outline"
                                                size="sm"
                                                className="w-full"
                                                onClick={() => handleScanArchive(archive.name)}
                                            >
                                                {t('bookScanArchiveButton')}
                                            </Button>
                                        </div>
                                    ))}
                                </div>
                            ) : (
                                <div className="rounded border border-border">
                                    <Table>
                                        <TableHeader>
                                            <TableRow>
                                                <TableHead>{t('bookScanArchive')}</TableHead>
                                                <TableHead className="text-right">
                                                    {t('bookScanSize')}
                                                </TableHead>
                                                <TableHead className="text-right">
                                                    {t('bookScanFileCount')}
                                                </TableHead>
                                                <TableHead>{t('bookScanCreated')}</TableHead>
                                                <TableHead>
                                                    <span className="sr-only">{t('actions')}</span>
                                                </TableHead>
                                            </TableRow>
                                        </TableHeader>
                                        <TableBody>
                                            {unscannedArchives.map((archive) => (
                                                <TableRow key={archive.name}>
                                                    <TableCell className="break-all">
                                                        {archive.name}
                                                    </TableCell>
                                                    <TableCell className="text-right tabular-nums whitespace-nowrap">
                                                        {archive.size_mb} MB
                                                    </TableCell>
                                                    <TableCell className="text-right tabular-nums">
                                                        {archive.file_count}
                                                    </TableCell>
                                                    <TableCell className="whitespace-nowrap tabular-nums">
                                                        {new Date(
                                                            archive.created_date,
                                                        ).toLocaleString()}
                                                    </TableCell>
                                                    <TableCell>
                                                        <Button
                                                            variant="outline"
                                                            size="sm"
                                                            onClick={() =>
                                                                handleScanArchive(archive.name)
                                                            }
                                                        >
                                                            {t('bookScanArchiveButton')}
                                                        </Button>
                                                    </TableCell>
                                                </TableRow>
                                            ))}
                                        </TableBody>
                                    </Table>
                                </div>
                            )}
                        </TabsContent>

                        <TabsContent value="scanned" className="flex flex-col gap-3">
                            <div className="flex flex-wrap items-center justify-between gap-2">
                                <span className="text-sm text-muted-foreground tabular-nums">
                                    {t('bookScanTotalArchives', { count: scannedTotalCount })}
                                </span>
                                <span className="text-xs text-muted-foreground">
                                    {t('bookScanAutoRefresh')}
                                </span>
                            </div>

                            {isLoadingScanned && scannedArchives.length === 0 ? (
                                <p className="text-sm text-muted-foreground">{t('loading')}</p>
                            ) : scannedArchives.length === 0 ? (
                                <p className="text-sm text-muted-foreground">
                                    {t('bookScanNoScanned')}
                                </p>
                            ) : isMobile ? (
                                <div className="flex flex-col gap-3">
                                    {scannedArchives.map((archive) => (
                                        <div
                                            key={archive.name}
                                            className="flex flex-col gap-2 rounded-lg border border-border p-3"
                                        >
                                            <p className="text-sm font-semibold break-words">
                                                {archive.name}
                                            </p>
                                            <div className="flex flex-col gap-1 text-xs text-muted-foreground">
                                                <span className="flex items-center gap-2">
                                                    {t('bookScanBooksCount')}:
                                                    <Badge className="tabular-nums">
                                                        {archive.books_count}
                                                    </Badge>
                                                </span>
                                                <span className="flex items-center gap-2">
                                                    {t('bookScanErrorsCount')}:
                                                    <Badge
                                                        variant={
                                                            archive.errors_count > 0
                                                                ? 'destructive'
                                                                : 'default'
                                                        }
                                                        className="tabular-nums"
                                                    >
                                                        {archive.errors_count}
                                                    </Badge>
                                                </span>
                                                <span>
                                                    {t('bookScanScannedAt')}:{' '}
                                                    <strong>
                                                        {formatScannedDate(archive.scanned_at)}
                                                    </strong>
                                                </span>
                                            </div>
                                            {archiveActions(archive)}
                                        </div>
                                    ))}
                                </div>
                            ) : (
                                <div className="rounded border border-border">
                                    <Table>
                                        <TableHeader>
                                            <TableRow>
                                                <TableHead>{t('bookScanArchive')}</TableHead>
                                                <TableHead className="text-right">
                                                    {t('bookScanBooksCount')}
                                                </TableHead>
                                                <TableHead className="text-right">
                                                    {t('bookScanErrorsCount')}
                                                </TableHead>
                                                <TableHead>{t('bookScanScannedAt')}</TableHead>
                                                <TableHead>
                                                    <span className="sr-only">{t('actions')}</span>
                                                </TableHead>
                                            </TableRow>
                                        </TableHeader>
                                        <TableBody>
                                            {scannedArchives.map((archive) => (
                                                <TableRow key={archive.name}>
                                                    <TableCell className="break-all">
                                                        {archive.name}
                                                    </TableCell>
                                                    <TableCell className="text-right">
                                                        <Badge className="tabular-nums">
                                                            {archive.books_count}
                                                        </Badge>
                                                    </TableCell>
                                                    <TableCell className="text-right">
                                                        <Badge
                                                            variant={
                                                                archive.errors_count > 0
                                                                    ? 'destructive'
                                                                    : 'default'
                                                            }
                                                            className="tabular-nums"
                                                        >
                                                            {archive.errors_count}
                                                        </Badge>
                                                    </TableCell>
                                                    <TableCell className="whitespace-nowrap tabular-nums">
                                                        {formatScannedDate(archive.scanned_at)}
                                                    </TableCell>
                                                    <TableCell>{archiveActions(archive)}</TableCell>
                                                </TableRow>
                                            ))}
                                        </TableBody>
                                    </Table>
                                </div>
                            )}
                        </TabsContent>
                    </Tabs>
                </CardContent>
            </Card>

            {/* Rescan Confirmation Dialog */}
            <Dialog
                open={rescanDialogOpen}
                onOpenChange={(next) => {
                    // A rescan in flight owns the dialog: closing it would strand
                    // the progress it is the only view of.
                    if (!next && !isRescanning) handleRescanDialogClose();
                }}
            >
                <DialogContent closeLabel={t('close')} showCloseButton={!isRescanning}>
                    <DialogHeader>
                        <DialogTitle>
                            {isRescanning
                                ? t('rescanningArchive')
                                : `${t('refresh')} ${t('bookScanArchive')}`}
                        </DialogTitle>
                        <DialogDescription>
                            {isRescanning
                                ? `${t('rescanning')}: ${archiveToRescan ?? ''}`
                                : t('bookScanResetConfirm', { name: archiveToRescan || '' })}
                        </DialogDescription>
                    </DialogHeader>

                    {isRescanning &&
                        (rescanProgress ? (
                            <div className="flex flex-col gap-3">
                                <ProgressRow
                                    label={t('rescanProgress')}
                                    percent={rescanProgress.progress_percent}
                                />

                                {rescanProgress.current_archive && (
                                    <p className="text-sm break-words text-muted-foreground">
                                        {t('rescanCurrent')}: {rescanProgress.current_archive}
                                    </p>
                                )}

                                <div className="flex flex-wrap gap-6">
                                    <div className="flex flex-col">
                                        <span className="text-xs text-muted-foreground">
                                            {t('rescanBooksProcessed')}
                                        </span>
                                        <span className="text-lg font-medium text-primary tabular-nums">
                                            {rescanProgress.total_books}
                                        </span>
                                    </div>
                                    <div className="flex flex-col">
                                        <span className="text-xs text-muted-foreground">
                                            {t('rescanErrors')}
                                        </span>
                                        <span
                                            className={cn(
                                                'text-lg font-medium tabular-nums',
                                                rescanProgress.total_errors > 0
                                                    ? 'text-destructive'
                                                    : 'text-primary',
                                            )}
                                        >
                                            {rescanProgress.total_errors}
                                        </span>
                                    </div>
                                    <div className="flex flex-col">
                                        <span className="text-xs text-muted-foreground">
                                            {t('rescanElapsedTime')}
                                        </span>
                                        <span className="text-lg font-medium tabular-nums">
                                            {rescanProgress.elapsed_seconds}s
                                        </span>
                                    </div>
                                </div>
                            </div>
                        ) : (
                            <div role="status" className="flex flex-col gap-2">
                                <p className="text-sm text-muted-foreground">{t('rescanStarting')}</p>
                                <Progress aria-label={t('rescanStarting')} />
                            </div>
                        ))}

                    <DialogFooter>
                        <Button
                            variant="ghost"
                            onClick={handleRescanDialogClose}
                            disabled={isRescanning}
                        >
                            {t('cancel')}
                        </Button>
                        {!isRescanning && (
                            <Button onClick={handleRescanConfirm}>{t('refresh')}</Button>
                        )}
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
};

export default BookScanning;
