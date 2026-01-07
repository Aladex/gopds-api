import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
    Box,
    Button,
    Card,
    CardContent,
    Checkbox,
    FormControlLabel,
    LinearProgress,
    Stack,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableRow,
    TextField,
    Typography,
} from '@mui/material';
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

const BookScanning: React.FC = () => {
    const { t } = useTranslation();
    const [status, setStatus] = useState<ScanStatusResponse | null>(null);
    const [unscannedArchives, setUnscannedArchives] = useState<UnscannedArchiveInfo[]>([]);
    const [isLoading, setIsLoading] = useState(false);
    const [statusMessage, setStatusMessage] = useState<string | null>(null);
    const [scanError, setScanError] = useState<string | null>(null);
    const [resetName, setResetName] = useState('');
    const [deleteBooks, setDeleteBooks] = useState(false);
    const [lastBookTitle, setLastBookTitle] = useState<string | null>(null);
    const wsRef = useRef<WebSocket | null>(null);

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
            await fetchWithAuth.post(`/admin/scan/archive/${encodeURIComponent(name)}`);
            setStatusMessage(t('bookScanArchiveComplete', { name }));
            await fetchStatus();
            await fetchUnscanned();
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

    useEffect(() => {
        fetchStatus().then(r => r);
        fetchUnscanned().then(r => r);
    }, [fetchStatus, fetchUnscanned]);

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
                        setStatusMessage(t('bookScanCompleted'));
                        fetchUnscanned().then(r => r);
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
    }, [fetchUnscanned, t]);

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
                    <Button variant="outlined" onClick={fetchStatus}>
                        {t('bookScanStatusRefresh')}
                    </Button>
                    <Button variant="outlined" onClick={fetchUnscanned} disabled={isLoading}>
                        {t('bookScanUnscannedRefresh')}
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
                        <Typography variant="subtitle1" gutterBottom>
                            {t('bookScanUnscannedTitle')}
                        </Typography>
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
                    </CardContent>
                </Card>

                <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                    <CardContent>
                        <Typography variant="subtitle1" gutterBottom>
                            {t('bookScanResetTitle')}
                        </Typography>
                        <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} alignItems="center">
                            <TextField
                                label={t('bookScanResetName')}
                                value={resetName}
                                onChange={(event) => setResetName(event.target.value)}
                                size="small"
                                sx={{ minWidth: 260 }}
                            />
                            <FormControlLabel
                                control={
                                    <Checkbox
                                        checked={deleteBooks}
                                        onChange={(event) => setDeleteBooks(event.target.checked)}
                                    />
                                }
                                label={t('bookScanDeleteBooks')}
                            />
                            <Button variant="outlined" color="warning" onClick={handleReset}>
                                {t('bookScanResetButton')}
                            </Button>
                        </Stack>
                    </CardContent>
                </Card>
            </Box>
        </Box>
    );
};

export default BookScanning;
