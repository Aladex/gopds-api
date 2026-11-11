import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import * as adminApi from '@/api/admin';
import { isApiError } from '@/api/errors';
import { useWebSocket, WSMessage } from '@/context/WebSocketContext';

/*
 * The scan tab's own state: the archive scan, the fix scan, the archive lists
 * and the read errors, their handlers and their socket subscriptions. It is
 * BookScanning's state moved out unchanged, so the pipeline shell and the
 * stage panels read one copy of it. Nothing here polls: the socket events and
 * a reconnect re-read what changed, as before.
 */

export interface ScanStatusResponse {
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

export interface UnscannedArchiveInfo {
    name: string;
    size_mb: number;
    file_count: number;
    created_date: string;
}

export interface ScanErrorItem {
    file_name: string;
    archive_name: string;
    error: string;
    timestamp: string;
}

export interface ScannedArchiveInfo {
    name: string;
    books_count: number;
    errors_count: number;
    scanned_at: string;
}

export interface StartScanResponse {
    session_id: string;
    started_at: string;
}

export interface ScanStartedEvent {
    total_archives: number;
    timestamp: string;
}

export interface ArchiveStartedEvent {
    archive_name: string;
    timestamp: string;
}

export interface ArchiveCompletedEvent {
    archive_name: string;
    books_count: number;
    errors_count: number;
    duration_ms: number;
    timestamp: string;
}

export interface ScanCompletedEvent {
    total_archives: number;
    total_books: number;
    total_errors: number;
    duration_ms: number;
    // Flushes the coalesced progress value: the last book may land after the
    // final progress tick.
    last_book_title?: string;
    timestamp: string;
}

export interface ScanErrorEvent {
    message: string;
    timestamp: string;
}

export interface ScanProgressEvent {
    current_archive: string;
    archives_processed: number;
    total_archives: number;
    books_processed: number;
    total_books: number;
    progress_percent: number;
    elapsed_seconds: number;
    // The per-book event's payload, folded into the throttled progress frame.
    last_book_title?: string;
    timestamp: string;
}

export interface FixScanStatusResponse {
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

export interface FixScanStartedEvent {
    scan_type: string;
    total_books: number;
    total_archives: number;
    timestamp: string;
}

export interface FixScanProgressEvent {
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

export interface FixScanCompletedEvent {
    scan_type: string;
    total_books: number;
    updated_books: number;
    total_archives: number;
    error_count: number;
    duration_ms: number;
    timestamp: string;
}

export interface FixScanErrorEvent {
    scan_type: string;
    message: string;
    timestamp: string;
}

export function useBookScan() {
    const { t } = useTranslation();
    const [status, setStatus] = useState<ScanStatusResponse | null>(null);
    const [unscannedArchives, setUnscannedArchives] = useState<UnscannedArchiveInfo[]>([]);
    const [scannedArchives, setScannedArchives] = useState<ScannedArchiveInfo[]>([]);
    const [scannedTotalCount, setScannedTotalCount] = useState(0);
    const [scanErrors, setScanErrors] = useState<ScanErrorItem[]>([]);
    const [isLoading, setIsLoading] = useState(false);
    const [isLoadingScanned, setIsLoadingScanned] = useState(false);
    // Whether each list has answered once (or failed to): until then the
    // pipeline reads it as loading rather than as an empty list.
    const [unscannedLoaded, setUnscannedLoaded] = useState(false);
    const [scannedLoaded, setScannedLoaded] = useState(false);
    const [statusMessage, setStatusMessage] = useState<string | null>(null);
    const [scanError, setScanError] = useState<string | null>(null);
    const [lastBookTitle, setLastBookTitle] = useState<string | null>(null);
    const [rescanDialogOpen, setRescanDialogOpen] = useState(false);
    const [archiveToRescan, setArchiveToRescan] = useState<string | null>(null);
    const [isRescanning, setIsRescanning] = useState(false);
    const [rescanProgress, setRescanProgress] = useState<ScanStatusResponse | null>(null);
    const [fixScanStatus, setFixScanStatus] = useState<FixScanStatusResponse | null>(null);
    const [isFixScanning, setIsFixScanning] = useState(false);
    const { isConnected, subscribe } = useWebSocket();
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
            setUnscannedLoaded(true);
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
            setScannedLoaded(true);
        }
    }, []);

    const fetchErrors = useCallback(async () => {
        try {
            const data = await adminApi.listScanErrors<{ errors?: ScanErrorItem[] }>();
            setScanErrors(data?.errors || []);
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
            const started = await adminApi.startScan<{
                started_at?: string;
                session_id?: string;
            }>();
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

    // Scan events ride the shared app-wide socket; see WebSocketContext.
    const handleScanMessage = useCallback(
        (message: WSMessage) => {
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
                    setLastBookTitle(null);
                    break;
                }
                case 'archive_started': {
                    const payload = message.data as ArchiveStartedEvent;
                    setStatus((prev) =>
                        prev ? { ...prev, current_archive: payload.archive_name } : prev,
                    );
                    break;
                }
                case 'archive_reset':
                    // An archive left the scanned list without any scan
                    // running (a reset from this tab or another one).
                    fetchScanned();
                    break;
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
                    if (payload.last_book_title) {
                        setLastBookTitle(payload.last_book_title);
                    }
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
                    if (payload.last_book_title) {
                        setLastBookTitle(payload.last_book_title);
                    }
                    break;
                }
                case 'scan_error': {
                    const payload = message.data as ScanErrorEvent;
                    setScanError(payload.message || t('bookScanError'));
                    break;
                }
                default:
                    break;
            }
        },
        [fetchErrors, fetchUnscanned, fetchScanned, t],
    );

    const handleFixScanMessage = useCallback(
        (message: WSMessage) => {
            switch (message.type) {
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
                    // Its per-book author metadata failures joined the errors list.
                    fetchErrors();
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
        },
        [fetchErrors, t],
    );

    useEffect(() => subscribe('scan', handleScanMessage), [subscribe, handleScanMessage]);
    useEffect(() => subscribe('fix_scan', handleFixScanMessage), [subscribe, handleFixScanMessage]);

    // The hub has no replay: anything that completed while the socket was
    // down never arrives as an event. A fresh connection re-reads the state
    // this page mirrors from events.
    const wasConnectedRef = useRef(isConnected);
    useEffect(() => {
        const wasConnected = wasConnectedRef.current;
        wasConnectedRef.current = isConnected;
        if (isConnected && !wasConnected) {
            fetchScanned();
            fetchStatus();
        }
    }, [isConnected, fetchScanned, fetchStatus]);

    return {
        status,
        progressPercent,
        unscannedArchives,
        unscannedLoaded,
        scannedArchives,
        scannedLoaded,
        scannedTotalCount,
        scanErrors,
        isLoading,
        isLoadingScanned,
        statusMessage,
        scanError,
        lastBookTitle,
        rescanDialogOpen,
        archiveToRescan,
        isRescanning,
        rescanProgress,
        fixScanStatus,
        isFixScanning,
        fetchStatus,
        fetchUnscanned,
        fetchScanned,
        fetchErrors,
        formatScannedDate,
        handleStartScan,
        handleScanArchive,
        handleRescanClick,
        handleResetArchive,
        handleDeleteArchiveBooks,
        handleDownloadErrorFile,
        handleRescanDialogClose,
        handleRescanConfirm,
        handleStartFixScan,
        handleCancelFixScan,
    };
}

export type BookScanState = ReturnType<typeof useBookScan>;
