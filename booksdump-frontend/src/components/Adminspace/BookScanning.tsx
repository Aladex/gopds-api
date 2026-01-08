import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
    Box,
    Button,
    Card,
    CardContent,
    Checkbox,
    Chip,
    CircularProgress,
    Dialog,
    DialogActions,
    DialogContent,
    DialogContentText,
    DialogTitle,
    FormControlLabel,
    IconButton,
    LinearProgress,
    Snackbar,
    Stack,
    Tab,
    Tabs,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableRow,
    TextField,
    Typography,
} from '@mui/material';
import RefreshIcon from '@mui/icons-material/Refresh';
import DeleteIcon from '@mui/icons-material/Delete';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import { fetchWithAuth, WS_URL } from '../../api/config';
import { useTranslation } from 'react-i18next';

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

const BookScanning: React.FC = () => {
    const { t } = useTranslation();
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
    const [resetName, setResetName] = useState('');
    const [deleteBooks, setDeleteBooks] = useState(false);
    const [lastBookTitle, setLastBookTitle] = useState<string | null>(null);
    const [currentTab, setCurrentTab] = useState(0);
    const [rescanDialogOpen, setRescanDialogOpen] = useState(false);
    const [archiveToRescan, setArchiveToRescan] = useState<string | null>(null);
    const [isRescanning, setIsRescanning] = useState(false);
    const [rescanProgress, setRescanProgress] = useState<ScanStatusResponse | null>(null);
    const [snackbarOpen, setSnackbarOpen] = useState(false);
    const [snackbarMessage, setSnackbarMessage] = useState('');
    const wsRef = useRef<WebSocket | null>(null);
    const scannedIntervalRef = useRef<NodeJS.Timeout | null>(null);
    const rescanPollingRef = useRef<NodeJS.Timeout | null>(null);

    const progressPercent = useMemo(() => {
        if (!status) {
            return 0;
        }
        if (status.total_archives === 0) {
            return 0;
        }
        if (status.progress_percent) {
            return Math.min(100, status.progress_percent);
        }
        return Math.min(100, Math.round((status.archives_processed / status.total_archives) * 100));
    }, [status]);

    const fetchStatus = useCallback(async () => {
        try {
            const response = await fetchWithAuth.get('/admin/scan/status');
            setStatus(response.data);
            setScanError(response.data?.last_error || null);
        } catch (error) {
            console.error(error);
        }
    }, []);

    const fetchUnscanned = useCallback(async () => {
        setIsLoading(true);
        try {
            const response = await fetchWithAuth.get('/admin/scan/unscanned');
            setUnscannedArchives(response.data?.unscanned_archives || []);
        } catch (error) {
            console.error(error);
        } finally {
            setIsLoading(false);
        }
    }, []);

    const fetchScanned = useCallback(async () => {
        setIsLoadingScanned(true);
        try {
            const response = await fetchWithAuth.get('/admin/scan/scanned');
            setScannedArchives(response.data?.scanned_archives || []);
            setScannedTotalCount(response.data?.total_count || 0);
        } catch (error) {
            console.error(error);
        } finally {
            setIsLoadingScanned(false);
        }
    }, []);

    const fetchErrors = useCallback(async () => {
        try {
            const response = await fetchWithAuth.get('/admin/scan/errors');
            setScanErrors(response.data?.errors || []);
            setSelectedErrorIndex(-1);
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
            const response = await fetchWithAuth.post('/admin/scan');
            const startedAt = response.data?.started_at;
            setStatus((prev) => ({
                is_running: true,
                session_id: response.data?.session_id,
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
        } catch (error: any) {
            if (error?.response?.status === 409) {
                setStatusMessage(t('bookScanAlreadyRunning'));
                await fetchStatus();
                return;
            }
            console.error(error);
            setScanError(t('bookScanStartError'));
        }
    }, [fetchStatus, t]);

    const handleScanArchive = useCallback(async (name: string) => {
        setStatusMessage(null);
        setScanError(null);
        try {
            await fetchWithAuth.post('/admin/scan/archive', { name });
            setStatusMessage(t('bookScanStarted'));
        } catch (error) {
            console.error(error);
            setScanError(t('bookScanArchiveError', { name }));
        }
    }, [fetchStatus, fetchUnscanned, t]);

    const handleReset = useCallback(async () => {
        setStatusMessage(null);
        setScanError(null);
        if (!resetName.trim()) {
            setScanError(t('bookScanResetNameRequired'));
            return;
        }
        const confirmed = window.confirm(t('bookScanResetConfirm', { name: resetName }));
        if (!confirmed) {
            return;
        }
        if (deleteBooks) {
            const confirmDelete = window.confirm(t('bookScanDeleteBooksConfirm'));
            if (!confirmDelete) {
                return;
            }
        }
        try {
            await fetchWithAuth.delete(
                `/admin/scan/reset/${encodeURIComponent(resetName)}?confirm=true&delete_books=${deleteBooks ? 'true' : 'false'}`
            );
            setStatusMessage(t('bookScanResetSuccess', { name: resetName }));
            setResetName('');
            setDeleteBooks(false);
            await fetchStatus();
            await fetchUnscanned();
        } catch (error) {
            console.error(error);
            setScanError(t('bookScanResetError'));
        }
    }, [deleteBooks, fetchStatus, fetchUnscanned, resetName, t]);

    const handleRescanClick = useCallback((archiveName: string) => {
        setArchiveToRescan(archiveName);
        setRescanDialogOpen(true);
    }, []);

    const handleResetArchive = useCallback(async (archiveName: string, deleteBooks: boolean = false) => {
        const confirmed = window.confirm(
            deleteBooks
                ? t('bookScanDeleteBooksConfirm')
                : t('bookScanResetConfirm', { name: archiveName })
        );
        if (!confirmed) {
            return;
        }

        if (deleteBooks) {
            const confirmDelete = window.confirm(t('bookScanDeleteBooksConfirm'));
            if (!confirmDelete) {
                return;
            }
        }

        try {
            await fetchWithAuth.delete(
                `/admin/scan/reset/${encodeURIComponent(archiveName)}?confirm=true&delete_books=${deleteBooks ? 'true' : 'false'}`
            );
            setSnackbarMessage(t('bookScanResetSuccess', { name: archiveName }));
            setSnackbarOpen(true);
            await fetchScanned();
            await fetchStatus();
            await fetchUnscanned();
        } catch (error) {
            console.error(error);
            setSnackbarMessage(t('bookScanResetError'));
            setSnackbarOpen(true);
        }
    }, [fetchScanned, fetchStatus, fetchUnscanned, t]);

    const handleDeleteArchiveBooks = useCallback(async (archiveName: string) => {
        const confirmed = window.confirm(
            t('bookScanDeleteBooksConfirm') + '\n' + archiveName
        );
        if (!confirmed) {
            return;
        }

        try {
            await fetchWithAuth.delete(
                `/admin/scan/reset/${encodeURIComponent(archiveName)}?confirm=true&delete_books=true`
            );
            setSnackbarMessage(t('bookScanResetSuccess', { name: archiveName }));
            setSnackbarOpen(true);
            await fetchScanned();
            await fetchStatus();
        } catch (error) {
            console.error(error);
            setSnackbarMessage(t('bookScanResetError'));
            setSnackbarOpen(true);
        }
    }, [fetchScanned, fetchStatus, t]);

    const handleDownloadErrorFile = useCallback(async (item: ScanErrorItem) => {
        try {
            const response = await fetchWithAuth.get('/admin/scan/errors/file', {
                params: {
                    archive: item.archive_name,
                    file: item.file_name,
                },
                responseType: 'blob',
            });
            const blob = new Blob([response.data]);
            const url = window.URL.createObjectURL(blob);
            const link = document.createElement('a');
            const parts = item.file_name.split('/');
            const filename = parts[parts.length - 1] || 'scan_error_file';
            link.href = url;
            link.download = filename;
            document.body.appendChild(link);
            link.click();
            link.remove();
            window.URL.revokeObjectURL(url);
        } catch (error) {
            console.error(error);
            setScanError(t('bookScanErrorDownloadError'));
        }
    }, [t]);

    const handleRescanDialogClose = useCallback(() => {
        setRescanDialogOpen(false);
        setArchiveToRescan(null);
    }, []);

    const handleRescanConfirm = useCallback(async () => {
        if (!archiveToRescan) return;

        setIsRescanning(true);

        try {
            // Start async rescan
            const startResponse = await fetchWithAuth.post('/admin/scan/archive', { name: archiveToRescan });

            if (!startResponse.data?.session_id) {
                throw new Error('No session_id received from server');
            }

            const { session_id } = startResponse.data as StartScanResponse;

            // Initialize rescanProgress with session_id so we can track completion
            setRescanProgress({
                is_running: true,
                session_id: session_id,
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

                // Set up timeout
                const timeoutId = setTimeout(() => {
                    reject(new Error('Rescan timeout - operation took too long'));
                }, maxWaitTime);

                // Check status periodically (WebSocket updates rescanProgress)
                const intervalId = setInterval(() => {
                    // Get latest status from state via a ref check
                    setRescanProgress((current) => {
                        // Check if scan completed (is_running became false)
                        if (current && !current.is_running && current.progress_percent >= 100) {
                            clearTimeout(timeoutId);
                            clearInterval(intervalId);
                            resolve();
                        }
                        return current;
                    });
                }, 500);

                // Store interval ref for cleanup
                rescanPollingRef.current = intervalId;
            });

            // Get final status for results message
            const finalStatus = await fetchWithAuth.get('/admin/scan/status');
            if (finalStatus.data) {
                const message = `Archive "${archiveToRescan}" processed. ${finalStatus.data.total_books} books, ${finalStatus.data.total_errors} errors.`;
                setSnackbarMessage(message);
            }

            // Refresh scanned archives list
            await fetchScanned();
            await fetchStatus();

        } catch (error: any) {
            console.error('Rescan error:', error);

            const errorMessage = error?.response?.data?.message || error?.message || t('bookScanArchiveError', { name: archiveToRescan });
            setSnackbarMessage(errorMessage);
        } finally {
            setIsRescanning(false);
            setRescanProgress(null);
            setArchiveToRescan(null);
            setRescanDialogOpen(false);
            setSnackbarOpen(true);

            // Clear any polling timeout
            if (rescanPollingRef.current) {
                clearTimeout(rescanPollingRef.current);
                rescanPollingRef.current = null;
            }
        }
    }, [archiveToRescan, fetchScanned, fetchStatus, t]);

    const handleSnackbarClose = useCallback(() => {
        setSnackbarOpen(false);
    }, []);

    useEffect(() => {
        fetchStatus().then(r => r);
        fetchUnscanned().then(r => r);
        fetchScanned().then(r => r);
        fetchErrors().then(r => r);
    }, [fetchErrors, fetchStatus, fetchUnscanned, fetchScanned]);

    // Auto-refresh scanned archives every 30 seconds when on scanned tab
    // (WebSocket handles real-time updates, this is just a fallback)
    useEffect(() => {
        if (currentTab === 1) {
            fetchScanned().then(r => r);
            scannedIntervalRef.current = setInterval(() => {
                fetchScanned().then(r => r);
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
        const ws = new WebSocket(`${WS_URL}/api/admin/ws`);
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
                        setStatus((prev) => prev ? {
                            ...prev,
                            current_archive: payload.archive_name,
                        } : prev);
                        break;
                    }
                    case 'book_processed': {
                        const payload = message.data as BookProcessedEvent;
                        setLastBookTitle(payload.book_title);
                        setStatus((prev) => prev ? {
                            ...prev,
                            total_books: prev.total_books + 1,
                        } : prev);
                        break;
                    }
                    case 'archive_completed': {
                        const payload = message.data as ArchiveCompletedEvent;
                        setStatus((prev) => prev ? {
                            ...prev,
                            archives_processed: prev.archives_processed + 1,
                            total_errors: prev.total_errors + payload.errors_count,
                            current_archive: '',
                        } : prev);

                        // Update scanned archives list when an archive completes
                        fetchScanned().then(r => r);
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
                        setRescanProgress((prev) => prev ? {
                            ...prev,
                            is_running: false,
                            progress_percent: 100,
                            archives_processed: payload.total_archives,
                            total_books: payload.total_books,
                            total_errors: payload.total_errors,
                        } : prev);

                        setStatusMessage(t('bookScanCompleted'));
                        fetchUnscanned().then(r => r);
                        fetchScanned().then(r => r);
                        fetchErrors().then(r => r);
                        break;
                    }
                    case 'scan_progress': {
                        const payload = message.data as ScanProgressEvent;
                        setStatus((prev) => prev ? {
                            ...prev,
                            current_archive: payload.current_archive,
                            archives_processed: payload.archives_processed,
                            total_archives: payload.total_archives,
                            total_books: payload.books_processed,
                            progress_percent: payload.progress_percent,
                            elapsed_seconds: payload.elapsed_seconds,
                        } : prev);

                        // Also update rescanProgress if rescan dialog is open
                        setRescanProgress((prev) => prev ? {
                            ...prev,
                            current_archive: payload.current_archive,
                            archives_processed: payload.archives_processed,
                            total_archives: payload.total_archives,
                            total_books: payload.books_processed,
                            progress_percent: payload.progress_percent,
                            elapsed_seconds: payload.elapsed_seconds,
                        } : prev);
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

    return (
        <Box>
            <Typography variant="h6" align="center">{t('bookScanning')}</Typography>

            <Box sx={{ my: 2 }}>
                <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} alignItems="flex-start" sx={{ mb: 2 }}>
                    <Button
                        variant="contained"
                        onClick={handleStartScan}
                        disabled={status?.is_running}
                    >
                        {t('bookScanStart')}
                    </Button>
                    <Button variant="contained" sx={{ backgroundColor: 'action.selected', color: 'text.primary' }} onClick={fetchStatus}>
                        {t('bookScanStatusRefresh')}
                    </Button>
                </Stack>

                <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                    <CardContent>
                        <Typography variant="subtitle1" gutterBottom>
                            {t('bookScanStatusTitle')}
                        </Typography>
                        {status ? (
                            <>
                                <Typography variant="body2">
                                    {t('bookScanStatus')}: {status.is_running ? t('bookScanRunning') : t('bookScanIdle')}
                                </Typography>
                                <Typography variant="body2">
                                    {t('bookScanArchivesProcessed', {
                                        processed: status.archives_processed,
                                        total: status.total_archives,
                                    })}
                                </Typography>
                                <Typography variant="body2">
                                    {t('bookScanBooksProcessed', { count: status.total_books })}
                                </Typography>
                                <Typography variant="body2">
                                    {t('bookScanErrors', { count: status.total_errors })}
                                </Typography>
                                {status.current_archive && (
                                    <Typography variant="body2">
                                        {t('bookScanCurrentArchive')}: {status.current_archive}
                                    </Typography>
                                )}
                                {lastBookTitle && (
                                    <Typography variant="body2">
                                        {t('bookScanLastBook')}: {lastBookTitle}
                                    </Typography>
                                )}
                                <Box sx={{ mt: 1 }}>
                                    <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 0.5 }}>
                                        <Typography variant="body2" color="text.secondary">
                                            {t('bookScanProgress')}
                                        </Typography>
                                        <Typography variant="body2" fontWeight="bold">
                                            {Math.round(progressPercent)}%
                                        </Typography>
                                    </Box>
                                    <LinearProgress variant="determinate" value={progressPercent} />
                                </Box>
                            </>
                        ) : (
                            <Typography variant="body2" color="text.secondary">
                                {t('bookScanNotStarted')}
                            </Typography>
                        )}
                        {statusMessage && (
                            <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
                                {statusMessage}
                            </Typography>
                        )}
                        {scanError && (
                            <Typography variant="body2" color="error" sx={{ mt: 1 }}>
                                {scanError}
                            </Typography>
                        )}
                    </CardContent>
                </Card>

                <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                    <CardContent>
                        <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} alignItems="center" sx={{ mb: 2 }}>
                            <Typography variant="subtitle1">
                                {t('bookScanErrorsTitle')}
                            </Typography>
                            <Button variant="contained" sx={{ backgroundColor: 'action.selected', color: 'text.primary' }} size="small" onClick={fetchErrors}>
                                {t('bookScanErrorsRefresh')}
                            </Button>
                        </Stack>
                        {scanErrors.length === 0 ? (
                            <Typography variant="body2" color="text.secondary">
                                {t('bookScanNoErrors')}
                            </Typography>
                        ) : (
                            <>
                                <TextField
                                    select
                                    label={t('bookScanErrorsSelect')}
                                    value={selectedErrorIndex >= 0 ? String(selectedErrorIndex) : ''}
                                    onChange={(event) => setSelectedErrorIndex(Number(event.target.value))}
                                    SelectProps={{ native: true }}
                                    size="small"
                                    sx={{ minWidth: 320, mb: 2 }}
                                >
                                    <option value="" disabled>
                                        {t('bookScanErrorsSelectPlaceholder')}
                                    </option>
                                    {scanErrors.map((item, index) => (
                                        <option key={`${item.archive_name}-${item.file_name}-${index}`} value={index}>
                                            {item.archive_name} / {item.file_name}
                                        </option>
                                    ))}
                                </TextField>
                                {selectedErrorIndex >= 0 && scanErrors[selectedErrorIndex] && (
                                    <Box>
                                        <Typography variant="body2">
                                            {t('bookScanErrorArchive')}: {scanErrors[selectedErrorIndex].archive_name}
                                        </Typography>
                                        <Typography variant="body2">
                                            {t('bookScanErrorFile')}: {scanErrors[selectedErrorIndex].file_name}
                                        </Typography>
                                        <Typography variant="body2">
                                            {t('bookScanErrorMessage')}: {scanErrors[selectedErrorIndex].error}
                                        </Typography>
                                        <Typography variant="body2" color="text.secondary">
                                            {t('bookScanErrorTime')}: {new Date(scanErrors[selectedErrorIndex].timestamp).toLocaleString()}
                                        </Typography>
                                        <Button
                                            variant="contained"
                                            sx={{ mt: 2, backgroundColor: 'action.selected', color: 'text.primary' }}
                                            size="small"
                                            onClick={() => handleDownloadErrorFile(scanErrors[selectedErrorIndex])}
                                        >
                                            {t('bookScanErrorDownload')}
                                        </Button>
                                    </Box>
                                )}
                            </>
                        )}
                    </CardContent>
                </Card>

                <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                    <Tabs
                        value={currentTab}
                        onChange={(_, newValue) => setCurrentTab(newValue)}
                        sx={{
                            borderBottom: 2,
                            borderColor: 'divider',
                            '& .MuiTab-root': {
                                fontWeight: 500,
                                color: 'text.secondary',
                            },
                            '& .MuiTab-root.Mui-selected': {
                                fontWeight: 'bold',
                                color: 'primary.contrastText',
                                backgroundColor: 'primary.main',
                                borderRadius: '4px 4px 0 0',
                            },
                        }}
                    >
                        <Tab label={t('bookScanUnscannedTitle')} />
                        <Tab label={t('bookScanScannedTitle')} />
                    </Tabs>

                    <CardContent>
                        {currentTab === 0 && (
                            <>
                                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
                                    <Typography variant="subtitle2" color="text.secondary">
                                        {t('bookScanTotalArchives', { count: unscannedArchives.length })}
                                    </Typography>
                                    <Button
                                        size="small"
                                        variant="contained"
                                        sx={{ backgroundColor: 'action.selected', color: 'text.primary' }}
                                        onClick={fetchUnscanned}
                                        disabled={isLoading}
                                    >
                                        {t('bookScanUnscannedRefresh')}
                                    </Button>
                                </Box>
                                {unscannedArchives.length === 0 ? (
                                    <Typography variant="body2" color="text.secondary">
                                        {t('bookScanNoUnscanned')}
                                    </Typography>
                                ) : (
                                    <Table size="small">
                                        <TableHead>
                                            <TableRow>
                                                <TableCell>{t('bookScanArchive')}</TableCell>
                                                <TableCell>{t('bookScanSize')}</TableCell>
                                                <TableCell>{t('bookScanFileCount')}</TableCell>
                                                <TableCell>{t('bookScanCreated')}</TableCell>
                                                <TableCell>{t('actions')}</TableCell>
                                            </TableRow>
                                        </TableHead>
                                        <TableBody>
                                            {unscannedArchives.map((archive) => (
                                                <TableRow key={archive.name}>
                                                    <TableCell>{archive.name}</TableCell>
                                                    <TableCell>{archive.size_mb} MB</TableCell>
                                                    <TableCell>{archive.file_count}</TableCell>
                                                    <TableCell>
                                                        {new Date(archive.created_date).toLocaleString()}
                                                    </TableCell>
                                                    <TableCell>
                                                        <Button
                                                            size="small"
                                                            variant="outlined"
                                                            onClick={() => handleScanArchive(archive.name)}
                                                        >
                                                            {t('bookScanArchiveButton')}
                                                        </Button>
                                                    </TableCell>
                                                </TableRow>
                                            ))}
                                        </TableBody>
                                    </Table>
                                )}
                            </>
                        )}

                        {currentTab === 1 && (
                            <>
                                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
                                    <Typography variant="subtitle2" color="text.secondary">
                                        {t('bookScanTotalArchives', { count: scannedTotalCount })}
                                    </Typography>
                                    <Typography variant="caption" color="text.secondary">
                                        {t('bookScanAutoRefresh')}
                                    </Typography>
                                </Box>
                                {isLoadingScanned && scannedArchives.length === 0 ? (
                                    <Typography variant="body2" color="text.secondary">
                                        {t('loading')}
                                    </Typography>
                                ) : scannedArchives.length === 0 ? (
                                    <Typography variant="body2" color="text.secondary">
                                        {t('bookScanNoScanned')}
                                    </Typography>
                                ) : (
                                    <Table size="small">
                                        <TableHead>
                                            <TableRow>
                                                <TableCell>{t('bookScanArchive')}</TableCell>
                                                <TableCell>{t('bookScanBooksCount')}</TableCell>
                                                <TableCell>{t('bookScanErrorsCount')}</TableCell>
                                                <TableCell>{t('bookScanScannedAt')}</TableCell>
                                                <TableCell>{t('actions')}</TableCell>
                                            </TableRow>
                                        </TableHead>
                                        <TableBody>
                                            {scannedArchives.map((archive) => (
                                                <TableRow key={archive.name}>
                                                    <TableCell>{archive.name}</TableCell>
                                                    <TableCell>
                                                        <Chip
                                                            label={archive.books_count}
                                                            color="success"
                                                            size="small"
                                                            sx={{ fontWeight: 'bold' }}
                                                        />
                                                    </TableCell>
                                                    <TableCell>
                                                        <Chip
                                                            label={archive.errors_count}
                                                            color={archive.errors_count > 0 ? 'error' : 'success'}
                                                            size="small"
                                                            sx={{ fontWeight: 'bold' }}
                                                        />
                                                    </TableCell>
                                                    <TableCell>
                                                        {formatScannedDate(archive.scanned_at)}
                                                    </TableCell>
                                                    <TableCell>
                                                        <Box sx={{ display: 'flex', gap: 1 }}>
                                                            <IconButton
                                                                size="small"
                                                                onClick={() => handleRescanClick(archive.name)}
                                                                title={t('refresh')}
                                                                sx={{
                                                                    backgroundColor: 'primary.main',
                                                                    color: 'primary.contrastText',
                                                                    '&:hover': {
                                                                        backgroundColor: 'primary.dark',
                                                                    },
                                                                }}
                                                            >
                                                                <RefreshIcon />
                                                            </IconButton>
                                                            <IconButton
                                                                size="small"
                                                                onClick={() => handleResetArchive(archive.name, false)}
                                                                title={t('bookScanResetTitle')}
                                                                sx={{
                                                                    backgroundColor: 'warning.main',
                                                                    color: 'warning.contrastText',
                                                                    '&:hover': {
                                                                        backgroundColor: 'warning.dark',
                                                                    },
                                                                }}
                                                            >
                                                                <RestartAltIcon />
                                                            </IconButton>
                                                            <IconButton
                                                                size="small"
                                                                onClick={() => handleDeleteArchiveBooks(archive.name)}
                                                                title={t('bookScanDeleteBooks')}
                                                                sx={{
                                                                    backgroundColor: 'error.main',
                                                                    color: 'error.contrastText',
                                                                    '&:hover': {
                                                                        backgroundColor: 'error.dark',
                                                                    },
                                                                }}
                                                            >
                                                                <DeleteIcon />
                                                            </IconButton>
                                                        </Box>
                                                    </TableCell>
                                                </TableRow>
                                            ))}
                                        </TableBody>
                                    </Table>
                                )}
                            </>
                        )}
                    </CardContent>
                </Card>

            </Box>

            {/* Rescan Confirmation Dialog */}
            <Dialog
                open={rescanDialogOpen}
                onClose={isRescanning ? undefined : handleRescanDialogClose}
                aria-labelledby="rescan-dialog-title"
                aria-describedby="rescan-dialog-description"
                maxWidth="sm"
                fullWidth
            >
                <DialogTitle id="rescan-dialog-title">
                    {isRescanning ? t('rescanningArchive') : `${t('refresh')} ${t('bookScanArchive')}`}
                </DialogTitle>
                <DialogContent>
                    {!isRescanning ? (
                        <DialogContentText id="rescan-dialog-description">
                            {t('bookScanResetConfirm', { name: archiveToRescan || '' })}
                        </DialogContentText>
                    ) : (
                        <Box sx={{ py: 2 }}>
                            <Typography variant="body2" color="text.secondary" gutterBottom>
                                {t('rescanning')}: <strong>{archiveToRescan}</strong>
                            </Typography>

                            {rescanProgress && (
                                <Box sx={{ mt: 3 }}>
                                    {/* Progress Bar */}
                                    <Box sx={{ mb: 2 }}>
                                        <Box sx={{ display: 'flex', justifyContent: 'space-between', mb: 1 }}>
                                            <Typography variant="body2" color="text.secondary">
                                                {t('rescanProgress')}
                                            </Typography>
                                            <Typography variant="body2" color="text.secondary">
                                                {Math.round(rescanProgress.progress_percent)}%
                                            </Typography>
                                        </Box>
                                        <LinearProgress
                                            variant="determinate"
                                            value={Math.min(100, rescanProgress.progress_percent)}
                                        />
                                    </Box>

                                    {/* Current Archive */}
                                    {rescanProgress.current_archive && (
                                        <Typography variant="body2" color="text.secondary" gutterBottom>
                                            {t('rescanCurrent')}: {rescanProgress.current_archive}
                                        </Typography>
                                    )}

                                    {/* Stats */}
                                    <Box sx={{ display: 'flex', gap: 3, mt: 2, flexWrap: 'wrap' }}>
                                        <Box>
                                            <Typography variant="caption" color="text.secondary">
                                                {t('rescanBooksProcessed')}
                                            </Typography>
                                            <Typography variant="h6" color="primary">
                                                {rescanProgress.total_books}
                                            </Typography>
                                        </Box>
                                        <Box>
                                            <Typography variant="caption" color="text.secondary">
                                                {t('rescanErrors')}
                                            </Typography>
                                            <Typography
                                                variant="h6"
                                                color={rescanProgress.total_errors > 0 ? 'error' : 'success.main'}
                                            >
                                                {rescanProgress.total_errors}
                                            </Typography>
                                        </Box>
                                        <Box>
                                            <Typography variant="caption" color="text.secondary">
                                                {t('rescanElapsedTime')}
                                            </Typography>
                                            <Typography variant="h6">
                                                {rescanProgress.elapsed_seconds}s
                                            </Typography>
                                        </Box>
                                    </Box>
                                </Box>
                            )}

                            {!rescanProgress && (
                                <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, mt: 2 }}>
                                    <CircularProgress size={24} />
                                    <Typography variant="body2" color="text.secondary">
                                        {t('rescanStarting')}
                                    </Typography>
                                </Box>
                            )}
                        </Box>
                    )}
                </DialogContent>
                <DialogActions>
                    <Button onClick={handleRescanDialogClose} disabled={isRescanning}>
                        {t('cancel')}
                    </Button>
                    {!isRescanning && (
                        <Button
                            onClick={handleRescanConfirm}
                            variant="contained"
                            color="primary"
                        >
                            {t('refresh')}
                        </Button>
                    )}
                </DialogActions>
            </Dialog>

            {/* Snackbar for notifications */}
            <Snackbar
                open={snackbarOpen}
                autoHideDuration={4000}
                onClose={handleSnackbarClose}
                anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
                message={snackbarMessage}
            />
        </Box>
    );
};

export default BookScanning;
