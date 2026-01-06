import React, { useEffect } from 'react';
import {
    Box,
    Button,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    IconButton,
    Typography,
    Alert,
    CircularProgress,
    Chip,
    Grid2,
    Paper,
} from '@mui/material';
import {
    Close as CloseIcon,
    CheckCircle as CheckCircleIcon,
    Cancel as CancelIcon,
} from '@mui/icons-material';
import { useTranslation } from 'react-i18next';
import { useRescan, RescanPreview } from '../../hooks/useRescan';

interface Author {
    id: number;
    name: string;
}

interface Series {
    id: number;
    title: string;
    index: string;
}

type RescanPreviewDialogProps = {
    open: boolean;
    onClose: () => void;
    bookId: number | null;
    onRescanCompleted: () => void;
};

const RescanPreviewDialog: React.FC<RescanPreviewDialogProps> = ({
    open,
    onClose,
    bookId,
    onRescanCompleted,
}) => {
    const { t } = useTranslation();
    const {
        loading,
        error,
        preview,
        coverPreviewUrl,
        coverLoading,
        coverError,
        fetchPreview,
        fetchPreviewCover,
        clearCoverPreview,
        approveRescan,
        clearPreview,
    } = useRescan();

    useEffect(() => {
        if (open && bookId) {
            fetchPreview(bookId);
        }
    }, [open, bookId]);

    useEffect(() => {
        if (preview && bookId && preview.new.has_cover) {
            fetchPreviewCover(bookId);
            return;
        }
        clearCoverPreview();
    }, [preview, bookId]);

    const handleClose = () => {
        if (!loading) {
            clearPreview();
            onClose();
        }
    };

    const handleApprove = async () => {
        if (!bookId) return;
        const success = await approveRescan(bookId, 'approve');
        if (success) {
            onRescanCompleted();
            handleClose();
        }
    };

    const handleReject = async () => {
        if (!bookId) return;
        const success = await approveRescan(bookId, 'reject');
        if (success) {
            onRescanCompleted();
            handleClose();
        }
    };

    const isDifferent = (field: string) => {
        return preview?.diff.includes(field);
    };

    const renderField = (label: string, oldValue: any, newValue: any, fieldName: string) => {
        const isChanged = isDifferent(fieldName);

        return (
            <Grid2 container spacing={2} sx={{ mb: 2 }}>
                <Grid2 size={{ xs: 12 }}>
                    <Typography variant="subtitle2" color="text.secondary" gutterBottom>
                        {label}
                        {isChanged && (
                            <Chip
                                label={t('rescanChanged')}
                                size="small"
                                color="warning"
                                sx={{ ml: 1, height: 20 }}
                            />
                        )}
                    </Typography>
                </Grid2>
                <Grid2 size={{ xs: 6 }}>
                    <Paper
                        variant="outlined"
                        sx={{
                            p: 1.5,
                            bgcolor: isChanged ? 'error.dark' : 'background.default',
                            opacity: isChanged ? 0.7 : 1,
                        }}
                    >
                        <Typography variant="caption" color="text.secondary" display="block" gutterBottom>
                            {t('rescanOldValue')}
                        </Typography>
                        <Typography variant="body2">{oldValue || t('rescanEmpty')}</Typography>
                    </Paper>
                </Grid2>
                <Grid2 size={{ xs: 6 }}>
                    <Paper
                        variant="outlined"
                        sx={{
                            p: 1.5,
                            bgcolor: isChanged ? 'success.dark' : 'background.default',
                            opacity: isChanged ? 0.7 : 1,
                        }}
                    >
                        <Typography variant="caption" color="text.secondary" display="block" gutterBottom>
                            {t('rescanNewValue')}
                        </Typography>
                        <Typography variant="body2">{newValue || t('rescanEmpty')}</Typography>
                    </Paper>
                </Grid2>
            </Grid2>
        );
    };

    const renderAuthors = (authors: Author[]) => {
        return authors.map((a) => a.name).join(', ') || t('noAuthors');
    };

    const renderSeries = (series: Series | null) => {
        if (!series) return t('noSeries');
        return series.title + (series.index ? ` #${series.index}` : '');
    };

    const renderTags = (tags: string[]) => {
        return tags.join(', ') || t('rescanNoTags');
    };

    return (
        <Dialog
            open={open}
            onClose={handleClose}
            maxWidth="md"
            fullWidth
            PaperProps={{
                sx: {
                    maxHeight: '90vh',
                },
            }}
        >
            <DialogTitle sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', pb: 1 }}>
                <Typography variant="h6">{t('rescanPreview')}</Typography>
                <IconButton onClick={handleClose} size="small" disabled={loading}>
                    <CloseIcon />
                </IconButton>
            </DialogTitle>

            <DialogContent dividers>
                {error && (
                    <Alert severity="error" sx={{ mb: 2 }}>
                        {error}
                    </Alert>
                )}

                {loading && !preview && (
                    <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', py: 4 }}>
                        <CircularProgress />
                    </Box>
                )}

                {preview && (
                    <Box>
                        <Alert severity="info" sx={{ mb: 3 }}>
                            {t('rescanPreviewDescription')}
                        </Alert>

                        {renderField(t('title'), preview.old.title, preview.new.title, 'title')}
                        {renderField(t('language'), preview.old.lang, preview.new.lang, 'lang')}
                        {renderField(
                            t('authors'),
                            renderAuthors(preview.old.authors),
                            renderAuthors(preview.new.authors),
                            'authors'
                        )}
                        {renderField(
                            t('series'),
                            renderSeries(preview.old.series),
                            renderSeries(preview.new.series),
                            'series'
                        )}
                        {renderField(
                            t('rescanTags'),
                            renderTags(preview.old.tags),
                            renderTags(preview.new.tags),
                            'tags'
                        )}
                        {renderField(
                            t('publicationDate'),
                            preview.old.docdate,
                            preview.new.docdate,
                            'docdate'
                        )}
                        {renderField(
                            t('annotation'),
                            preview.old.annotation ? preview.old.annotation.substring(0, 200) + '...' : '',
                            preview.new.annotation ? preview.new.annotation.substring(0, 200) + '...' : '',
                            'annotation'
                        )}
                        {renderField(
                            t('rescanCover'),
                            preview.old.has_cover ? t('rescanHasCover') : t('rescanNoCover'),
                            preview.new.has_cover ? t('rescanHasCover') : t('rescanNoCover'),
                            'cover'
                        )}
                        {preview.new.has_cover && (
                            <Box sx={{ mt: 2 }}>
                                <Typography variant="subtitle2" color="text.secondary" gutterBottom>
                                    {t('rescanCoverPreview')}
                                </Typography>
                                {coverError && (
                                    <Alert severity="warning" sx={{ mb: 1 }}>
                                        {coverError}
                                    </Alert>
                                )}
                                {coverLoading && (
                                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                                        <CircularProgress size={20} />
                                        <Typography variant="body2">{t('loading')}</Typography>
                                    </Box>
                                )}
                                {coverPreviewUrl && (
                                    <Box
                                        component="img"
                                        src={coverPreviewUrl}
                                        alt={t('rescanCover')}
                                        sx={{
                                            maxWidth: 220,
                                            maxHeight: 320,
                                            borderRadius: 1,
                                            border: '1px solid',
                                            borderColor: 'divider',
                                        }}
                                    />
                                )}
                            </Box>
                        )}

                        <Box sx={{ mt: 2, p: 2, bgcolor: 'action.hover', borderRadius: 1 }}>
                            <Typography variant="caption" color="text.secondary" display="block">
                                <strong>{t('bookId')}:</strong> {preview.book_id}
                            </Typography>
                            <Typography variant="caption" color="text.secondary" display="block">
                                <strong>{t('rescanPendingId')}:</strong> {preview.pending_rescan_id}
                            </Typography>
                        </Box>
                    </Box>
                )}
            </DialogContent>

            <DialogActions sx={{ px: 3, py: 2 }}>
                <Button
                    onClick={handleClose}
                    disabled={loading}
                    sx={{ color: 'text.primary' }}
                >
                    {t('cancel')}
                </Button>
                <Button
                    onClick={handleReject}
                    variant="outlined"
                    color="error"
                    disabled={loading || !preview}
                    startIcon={loading ? <CircularProgress size={16} /> : <CancelIcon />}
                >
                    {t('rescanReject')}
                </Button>
                <Button
                    onClick={handleApprove}
                    variant="contained"
                    color="success"
                    disabled={loading || !preview}
                    startIcon={loading ? <CircularProgress size={16} /> : <CheckCircleIcon />}
                >
                    {t('rescanApprove')}
                </Button>
            </DialogActions>
        </Dialog>
    );
};

export default RescanPreviewDialog;
