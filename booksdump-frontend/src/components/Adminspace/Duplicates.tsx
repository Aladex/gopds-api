import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
    Box,
    Button,
    Card,
    CardContent,
    LinearProgress,
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

    const handleStartScan = useCallback(async () => {
        setScanError(null);
        setActionResult(null);
        try {
            const response = await fetchWithAuth.post('/admin/duplicates/scan');
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
        } catch (error) {
            console.error(error);
            setScanError(t('scanError'));
        }
    }, [t]);

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

    useEffect(() => {
        fetchGroups().then(r => r);
    }, [fetchGroups]);

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
            <Stack direction="row" spacing={2} justifyContent="center" sx={{ my: 2, flexWrap: 'wrap' }}>
                <Button variant="contained" onClick={handleStartScan} disabled={isScanning}>
                    {t('startScan')}
                </Button>
                <Button variant="outlined" onClick={fetchGroups} disabled={isLoading}>
                    {t('refresh')}
                </Button>
                <Button
                    variant="outlined"
                    color="warning"
                    onClick={handleHideDuplicates}
                    disabled={isScanning}
                >
                    {t('hideDuplicates')}
                </Button>
            </Stack>

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
