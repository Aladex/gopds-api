import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
    Alert,
    Box,
    Button,
    Card,
    CardContent,
    Dialog,
    DialogActions,
    DialogContent,
    DialogContentText,
    DialogTitle,
    IconButton,
    LinearProgress,
    Snackbar,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableRow,
    TextField,
    Typography,
    useMediaQuery,
    useTheme,
} from '@mui/material';
import EditIcon from '@mui/icons-material/Edit';
import SaveIcon from '@mui/icons-material/Save';
import CloseIcon from '@mui/icons-material/Close';
import AutoFixHighIcon from '@mui/icons-material/AutoFixHigh';
import { fetchWithAuth, WS_URL } from '../../api/config';
import { useTranslation } from 'react-i18next';

interface GenreAdmin {
    id: number;
    genre: string;
    title: string;
}

interface GenreTitleGenStartedEvent {
    total: number;
    timestamp: string;
}

interface GenreTitleGenProgressEvent {
    total: number;
    processed: number;
    current_genre: string;
    progress_percent: number;
    timestamp: string;
}

interface GenreTitleGenCompletedEvent {
    total: number;
    updated: number;
    duration_ms: number;
    timestamp: string;
}

const GenreManagement: React.FC = () => {
    const { t } = useTranslation();
    const theme = useTheme();
    const isMobile = useMediaQuery(theme.breakpoints.down('md'));

    const [genres, setGenres] = useState<GenreAdmin[]>([]);
    const [searchQuery, setSearchQuery] = useState('');
    const [editingId, setEditingId] = useState<number | null>(null);
    const [editValue, setEditValue] = useState('');
    const [isLoading, setIsLoading] = useState(false);
    const [isGenerating, setIsGenerating] = useState(false);
    const [progressPercent, setProgressPercent] = useState(0);
    const [progressCurrent, setProgressCurrent] = useState('');
    const [progressProcessed, setProgressProcessed] = useState(0);
    const [progressTotal, setProgressTotal] = useState(0);
    const [snackbarOpen, setSnackbarOpen] = useState(false);
    const [snackbarMessage, setSnackbarMessage] = useState('');
    const [snackbarSeverity, setSnackbarSeverity] = useState<'success' | 'error'>('success');
    const [confirmDialogOpen, setConfirmDialogOpen] = useState(false);
    const wsRef = useRef<WebSocket | null>(null);

    const fetchGenres = useCallback(async () => {
        setIsLoading(true);
        try {
            const response = await fetchWithAuth.get('/admin/genres');
            setGenres(response.data.result || []);
        } catch (error) {
            console.error('Failed to fetch genres', error);
        } finally {
            setIsLoading(false);
        }
    }, []);

    useEffect(() => {
        fetchGenres();
    }, [fetchGenres]);

    // WebSocket for genre title generation progress
    useEffect(() => {
        const ws = new WebSocket(`${WS_URL}/api/ws`);
        wsRef.current = ws;

        ws.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data);
                switch (message.type) {
                    case 'genre_title_gen_started': {
                        const payload = message.data as GenreTitleGenStartedEvent;
                        setIsGenerating(true);
                        setProgressPercent(0);
                        setProgressProcessed(0);
                        setProgressTotal(payload.total);
                        setProgressCurrent('');
                        break;
                    }
                    case 'genre_title_gen_progress': {
                        const payload = message.data as GenreTitleGenProgressEvent;
                        setIsGenerating(true);
                        setProgressPercent(payload.progress_percent);
                        setProgressProcessed(payload.processed);
                        setProgressTotal(payload.total);
                        setProgressCurrent(payload.current_genre);
                        break;
                    }
                    case 'genre_title_gen_completed': {
                        const payload = message.data as GenreTitleGenCompletedEvent;
                        setIsGenerating(false);
                        setProgressPercent(100);
                        setSnackbarMessage(t('titlesGenerated', { count: payload.updated }));
                        setSnackbarSeverity('success');
                        setSnackbarOpen(true);
                        fetchGenres();
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
            console.error('Genre WS error', error);
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
    }, [fetchGenres, t]);

    const handleEdit = (genre: GenreAdmin) => {
        setEditingId(genre.id);
        setEditValue(genre.title);
    };

    const handleCancelEdit = () => {
        setEditingId(null);
        setEditValue('');
    };

    const handleSave = async (id: number) => {
        try {
            await fetchWithAuth.put(`/admin/genres/${id}`, { title: editValue });
            setGenres((prev) =>
                prev.map((g) => (g.id === id ? { ...g, title: editValue } : g))
            );
            setEditingId(null);
            setEditValue('');
            setSnackbarMessage(t('genreTitleUpdated'));
            setSnackbarSeverity('success');
            setSnackbarOpen(true);
        } catch (error) {
            console.error('Failed to update genre title', error);
            setSnackbarMessage(t('errorUpdatingBook'));
            setSnackbarSeverity('error');
            setSnackbarOpen(true);
        }
    };

    const handleGenerateClick = () => {
        setConfirmDialogOpen(true);
    };

    const handleConfirmGenerate = async () => {
        setConfirmDialogOpen(false);
        try {
            await fetchWithAuth.post('/admin/genres/generate-titles');
        } catch (error) {
            console.error('Failed to start genre title generation', error);
            setSnackbarMessage(t('bookScanStartError'));
            setSnackbarSeverity('error');
            setSnackbarOpen(true);
        }
    };

    const filteredGenres = genres.filter((g) => {
        if (!searchQuery) return true;
        const q = searchQuery.toLowerCase();
        return g.genre.toLowerCase().includes(q) || g.title.toLowerCase().includes(q);
    });

    const renderGenreRow = (genre: GenreAdmin) => {
        const isEditing = editingId === genre.id;
        return (
            <TableRow key={genre.id}>
                <TableCell sx={{ fontFamily: 'monospace', fontSize: '0.85rem' }}>
                    {genre.genre}
                </TableCell>
                <TableCell>
                    {isEditing ? (
                        <TextField
                            value={editValue}
                            onChange={(e) => setEditValue(e.target.value)}
                            size="small"
                            fullWidth
                            autoFocus
                            onKeyDown={(e) => {
                                if (e.key === 'Enter') handleSave(genre.id);
                                if (e.key === 'Escape') handleCancelEdit();
                            }}
                        />
                    ) : (
                        genre.title
                    )}
                </TableCell>
                <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                    {isEditing ? (
                        <>
                            <IconButton size="small" onClick={() => handleSave(genre.id)} color="primary">
                                <SaveIcon fontSize="small" />
                            </IconButton>
                            <IconButton size="small" onClick={handleCancelEdit}>
                                <CloseIcon fontSize="small" />
                            </IconButton>
                        </>
                    ) : (
                        <IconButton size="small" onClick={() => handleEdit(genre)}>
                            <EditIcon fontSize="small" />
                        </IconButton>
                    )}
                </TableCell>
            </TableRow>
        );
    };

    const renderGenreCard = (genre: GenreAdmin) => {
        const isEditing = editingId === genre.id;
        return (
            <Card key={genre.id} sx={{ mb: 1 }}>
                <CardContent sx={{ py: 1, '&:last-child': { pb: 1 } }}>
                    <Typography variant="caption" color="text.secondary" sx={{ fontFamily: 'monospace' }}>
                        {genre.genre}
                    </Typography>
                    {isEditing ? (
                        <Box sx={{ display: 'flex', gap: 1, mt: 0.5 }}>
                            <TextField
                                value={editValue}
                                onChange={(e) => setEditValue(e.target.value)}
                                size="small"
                                fullWidth
                                autoFocus
                                onKeyDown={(e) => {
                                    if (e.key === 'Enter') handleSave(genre.id);
                                    if (e.key === 'Escape') handleCancelEdit();
                                }}
                            />
                            <IconButton size="small" onClick={() => handleSave(genre.id)} color="primary">
                                <SaveIcon fontSize="small" />
                            </IconButton>
                            <IconButton size="small" onClick={handleCancelEdit}>
                                <CloseIcon fontSize="small" />
                            </IconButton>
                        </Box>
                    ) : (
                        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                            <Typography variant="body2">{genre.title}</Typography>
                            <IconButton size="small" onClick={() => handleEdit(genre)}>
                                <EditIcon fontSize="small" />
                            </IconButton>
                        </Box>
                    )}
                </CardContent>
            </Card>
        );
    };

    return (
        <Box sx={{ mt: 2 }}>
            <Box sx={{ display: 'flex', gap: 2, mb: 2, flexWrap: 'wrap', alignItems: 'center' }}>
                <TextField
                    label={t('searchGenres')}
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                    size="small"
                    sx={{ minWidth: 200, flexGrow: 1, maxWidth: 400 }}
                />
                <Button
                    variant="contained"
                    startIcon={<AutoFixHighIcon />}
                    onClick={handleGenerateClick}
                    disabled={isGenerating}
                >
                    {t('generateWithAI')}
                </Button>
            </Box>

            {isGenerating && (
                <Box sx={{ mb: 2 }}>
                    <Typography variant="body2" color="text.secondary" sx={{ mb: 0.5 }}>
                        {t('generatingTitles')} {progressProcessed}/{progressTotal}
                        {progressCurrent && ` — ${progressCurrent}`}
                    </Typography>
                    <LinearProgress variant="determinate" value={progressPercent} />
                </Box>
            )}

            {isLoading ? (
                <LinearProgress />
            ) : isMobile ? (
                filteredGenres.map(renderGenreCard)
            ) : (
                <Table size="small">
                    <TableHead>
                        <TableRow>
                            <TableCell>{t('genreTag')}</TableCell>
                            <TableCell>{t('genreTitle')}</TableCell>
                            <TableCell align="right">{t('actions')}</TableCell>
                        </TableRow>
                    </TableHead>
                    <TableBody>
                        {filteredGenres.map(renderGenreRow)}
                    </TableBody>
                </Table>
            )}

            <Dialog open={confirmDialogOpen} onClose={() => setConfirmDialogOpen(false)}>
                <DialogTitle>{t('generateWithAI')}</DialogTitle>
                <DialogContent>
                    <DialogContentText>{t('generateConfirm')}</DialogContentText>
                </DialogContent>
                <DialogActions>
                    <Button onClick={() => setConfirmDialogOpen(false)}>{t('cancel')}</Button>
                    <Button onClick={handleConfirmGenerate} variant="contained">{t('generateWithAI')}</Button>
                </DialogActions>
            </Dialog>

            <Snackbar
                open={snackbarOpen}
                autoHideDuration={4000}
                onClose={() => setSnackbarOpen(false)}
                anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
            >
                <Alert
                    onClose={() => setSnackbarOpen(false)}
                    severity={snackbarSeverity}
                    variant="filled"
                >
                    {snackbarMessage}
                </Alert>
            </Snackbar>
        </Box>
    );
};

export default GenreManagement;
