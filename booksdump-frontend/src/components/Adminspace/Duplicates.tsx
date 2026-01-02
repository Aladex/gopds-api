import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
    Box,
    Button,
    Card,
    CardContent,
    LinearProgress,
    TextField,
    Stack,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableRow,
    Typography,
} from '@mui/material';
import { fetchWithAuth, WS_URL } from '../../api/config';
import { useTranslation } from 'react-i18next';

interface DuplicateGroup {
    md5_hash: string;
    count: number;
    book_ids: number[];
    example_titles: string[];
}

interface ScanProgress {
    job_id: number;
    status: string;
    processed_books: number;
    total_books: number;
    duplicates_found: number;
    error?: string;
}

const Duplicates: React.FC = () => {
    const { t } = useTranslation();
    const [groups, setGroups] = useState<DuplicateGroup[]>([]);
    const [isLoading, setIsLoading] = useState(false);
    const [isScanning, setIsScanning] = useState(false);
    const [scanProgress, setScanProgress] = useState<ScanProgress | null>(null);
    const [scanError, setScanError] = useState<string | null>(null);
    const [actionResult, setActionResult] = useState<string | null>(null);
    const [statusMessage, setStatusMessage] = useState<string | null>(null);
    const [workerCount, setWorkerCount] = useState<number>(1);
    const wsRef = useRef<WebSocket | null>(null);

    const progressPercent = useMemo(() => {
        if (!scanProgress || scanProgress.total_books === 0) {
            return 0;
        }
        return Math.min(100, Math.round((scanProgress.processed_books / scanProgress.total_books) * 100));
    }, [scanProgress]);

    const fetchGroups = useCallback(async () => {
        setIsLoading(true);
        setActionResult(null);
        try {
            const response = await fetchWithAuth.get('/admin/duplicates');
            setGroups(response.data?.groups || []);
        } catch (error) {
            console.error(error);
        } finally {
            setIsLoading(false);
        }
    }, []);

    const fetchActiveScan = useCallback(async () => {
        try {
            const response = await fetchWithAuth.get('/admin/duplicates/scan/active');
            if (response.data?.status === 'none' || !response.data) {
                setIsScanning(false);
                setScanProgress(null);
                setStatusMessage(null);
                return;
            }
            if (response.data) {
                setScanProgress({
                    job_id: response.data.id,
                    status: response.data.status,
                    processed_books: response.data.processed_books,
                    total_books: response.data.total_books,
                    duplicates_found: response.data.duplicates_found,
                    error: response.data.error,
                });
                setIsScanning(response.data.status === 'running' || response.data.status === 'pending');
            }
        } catch (error: any) {
            console.error(error);
        }
    }, []);

    const handleStartScan = useCallback(async () => {
        setScanError(null);
        setActionResult(null);
        setStatusMessage(null);
        try {
            const response = await fetchWithAuth.post('/admin/duplicates/scan', {
                workers: workerCount,
            });
            const jobId = response.data?.job_id;
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
        } catch (error: any) {
            if (error?.response?.status === 409) {
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
            const response = await fetchWithAuth.post('/admin/duplicates/hide');
            const hiddenCount = response.data?.hidden_count ?? 0;
            const skippedEmpty = response.data?.skipped_empty ?? 0;
            setActionResult(
                t('hideDuplicatesResult', { hidden: hiddenCount, skipped: skippedEmpty })
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
            await fetchWithAuth.post(`/admin/duplicates/scan/${scanProgress.job_id}/stop`);
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
            await fetchWithAuth.post(`/admin/duplicates/scan/${scanProgress.job_id}/force-stop`);
            setStatusMessage(t('scanForceStopRequested'));
            setIsScanning(false);
            await fetchActiveScan();
        } catch (error) {
            console.error(error);
            setScanError(t('scanForceStopError'));
        }
    }, [fetchActiveScan, scanProgress, t]);

    useEffect(() => {
        fetchGroups().then(r => r);
        fetchActiveScan().then(r => r);
    }, [fetchGroups, fetchActiveScan]);

    useEffect(() => {
        const ws = new WebSocket(`${WS_URL}/api/admin/ws`);
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
                    fetchGroups().then(r => r);
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
        <Box>
            <Typography variant="h6" align="center">{t('duplicates')}</Typography>
            <Box sx={{ my: 2 }}>
                <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} alignItems="flex-start" sx={{ mb: 2 }}>
                    <Box>
                        <TextField
                            label={t('workers')}
                            type="number"
                            size="small"
                            value={workerCount}
                            onChange={(event) => {
                                const next = Number(event.target.value);
                                if (!Number.isNaN(next)) {
                                    setWorkerCount(Math.max(1, Math.min(8, next)));
                                }
                            }}
                            sx={{ minWidth: 120, mb: 0.5 }}
                        />
                        <Typography variant="caption" color="text.secondary" display="block">
                            {t('workersHint')}
                        </Typography>
                    </Box>
                </Stack>

                <Stack
                    direction={{ xs: 'column', sm: 'row' }}
                    spacing={1}
                    alignItems="center"
                    justifyContent="flex-start"
                    sx={{ flexWrap: 'wrap' }}
                >
                    <Button
                        variant="contained"
                        onClick={handleStartScan}
                        disabled={isScanning}
                        sx={{
                            minWidth: 120,
                            '&:disabled': { opacity: 0.6, cursor: 'not-allowed' }
                        }}
                    >
                        {t('startScan')}
                    </Button>
                    <Button
                        variant="outlined"
                        color="warning"
                        onClick={handleStopScan}
                        disabled={!isScanning}
                        sx={{
                            minWidth: 120,
                            '&:disabled': { opacity: 0.6, cursor: 'not-allowed' }
                        }}
                    >
                        {t('stopScan')}
                    </Button>
                    <Button
                        variant="outlined"
                        color="error"
                        onClick={handleForceStopScan}
                        disabled={!scanProgress}
                        sx={{
                            minWidth: 120,
                            '&:disabled': { opacity: 0.6, cursor: 'not-allowed' }
                        }}
                    >
                        {t('forceStopScan')}
                    </Button>
                    <Button
                        variant="outlined"
                        color="inherit"
                        onClick={fetchActiveScan}
                        sx={{
                            minWidth: 120,
                            borderColor: (theme) => theme.palette.mode === 'dark'
                                ? 'rgba(255, 255, 255, 0.23)'
                                : 'rgba(0, 0, 0, 0.23)',
                            '&:hover': {
                                borderColor: (theme) => theme.palette.mode === 'dark'
                                    ? 'rgba(255, 255, 255, 0.4)'
                                    : 'rgba(0, 0, 0, 0.4)',
                            },
                            '&:disabled': { opacity: 0.6, cursor: 'not-allowed' }
                        }}
                    >
                        {t('getStatus')}
                    </Button>
                    <Button
                        variant="outlined"
                        color="inherit"
                        onClick={fetchGroups}
                        disabled={isLoading}
                        sx={{
                            minWidth: 120,
                            borderColor: (theme) => theme.palette.mode === 'dark'
                                ? 'rgba(255, 255, 255, 0.23)'
                                : 'rgba(0, 0, 0, 0.23)',
                            '&:hover': {
                                borderColor: (theme) => theme.palette.mode === 'dark'
                                    ? 'rgba(255, 255, 255, 0.4)'
                                    : 'rgba(0, 0, 0, 0.4)',
                            },
                            '&:disabled': { opacity: 0.6, cursor: 'not-allowed' }
                        }}
                    >
                        {t('refresh')}
                    </Button>
                    <Button
                        variant="contained"
                        color="warning"
                        onClick={handleHideDuplicates}
                        disabled={isScanning}
                        sx={{
                            minWidth: 120,
                            '&:disabled': { opacity: 0.6, cursor: 'not-allowed' }
                        }}
                    >
                        {t('hideDuplicates')}
                    </Button>
                </Stack>
            </Box>

            <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                <CardContent>
                    <Typography variant="subtitle1" gutterBottom>
                        {t('scanProgress')}
                    </Typography>
                    {scanProgress ? (
                        <>
                            <Typography variant="body2">
                                {t('scanStatus')}: {scanProgress.status}
                            </Typography>
                            <Typography variant="body2">
                                {t('processedBooks', {
                                    processed: scanProgress.processed_books,
                                    total: scanProgress.total_books,
                                })}
                            </Typography>
                            <Typography variant="body2">
                                {t('duplicatesFound', { count: scanProgress.duplicates_found })}
                            </Typography>
                            <Box sx={{ mt: 1 }}>
                                <LinearProgress variant="determinate" value={progressPercent} />
                            </Box>
                        </>
                    ) : (
                        <Typography variant="body2" color="text.secondary">
                            {t('scanNotStarted')}
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
                    {actionResult && (
                        <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
                            {actionResult}
                        </Typography>
                    )}
                </CardContent>
            </Card>

            <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                <CardContent>
                    <Typography variant="subtitle1" gutterBottom>
                        {t('duplicateGroups')}
                    </Typography>
                    {groups.length === 0 ? (
                        <Typography variant="body2" color="text.secondary">
                            {t('noDuplicateGroups')}
                        </Typography>
                    ) : (
                        <Table size="small">
                            <TableHead>
                                <TableRow>
                                    <TableCell>{t('hash')}</TableCell>
                                    <TableCell>{t('count')}</TableCell>
                                    <TableCell>{t('exampleTitles')}</TableCell>
                                </TableRow>
                            </TableHead>
                            <TableBody>
                                {groups.map((group) => (
                                    <TableRow key={group.md5_hash}>
                                        <TableCell sx={{ fontFamily: 'monospace' }}>
                                            {group.md5_hash}
                                        </TableCell>
                                        <TableCell>{group.count}</TableCell>
                                        <TableCell>
                                            {group.example_titles?.length
                                                ? group.example_titles.join(', ')
                                                : '-'}
                                        </TableCell>
                                    </TableRow>
                                ))}
                            </TableBody>
                        </Table>
                    )}
                </CardContent>
            </Card>
        </Box>
    );
};

export default Duplicates;
