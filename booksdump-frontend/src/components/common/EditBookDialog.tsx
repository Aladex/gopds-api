import React, { useEffect, useState } from 'react';
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
    TextField as MuiTextField,
    Autocomplete,
    Chip,
} from '@mui/material';
import { Close as CloseIcon } from '@mui/icons-material';
import { useTranslation } from 'react-i18next';
import { fetchWithAuth } from '../../api/config';
import { StyledTextField } from '../StyledDataItems';

interface Author {
    id: number;
    full_name: string;
}

interface Series {
    id: number;
    ser: string;
    ser_no: number;
}

interface Book {
    id: number;
    title: string;
    authors: Author[];
    series: Series[];
    annotation: string;
    filename: string;
    cover: string;
    registerdate: string;
    docdate: string;
    lang: string;
    fav: boolean;
    approved: boolean;
    path: string;
    format: string;
    favorite_count: number;
}

type EditBookDialogProps = {
    open: boolean;
    onClose: () => void;
    book: Book | null;
    onBookUpdated: (updatedBook: Book) => void;
};

const EditBookDialog: React.FC<EditBookDialogProps> = ({ open, onClose, book, onBookUpdated }) => {
    const { t } = useTranslation();
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [success, setSuccess] = useState(false);

    // Form fields
    const [title, setTitle] = useState('');
    const [annotation, setAnnotation] = useState('');
    const [docdate, setDocdate] = useState('');
    const [lang, setLang] = useState('');
    
    // Authors and series (for future enhancement)
    const [authors, setAuthors] = useState<Author[]>([]);
    const [series, setSeries] = useState<Series[]>([]);

    // Available authors and series for autocomplete (for future)
    const [availableAuthors, setAvailableAuthors] = useState<Author[]>([]);
    const [availableSeries, setAvailableSeries] = useState<Series[]>([]);

    // Validation errors
    const [titleError, setTitleError] = useState('');

    useEffect(() => {
        if (book && open) {
            setTitle(book.title || '');
            setAnnotation(book.annotation || '');
            setDocdate(book.docdate || '');
            setLang(book.lang || '');
            setAuthors(book.authors || []);
            setSeries(book.series || []);
            setError(null);
            setSuccess(false);
            setTitleError('');
        }
    }, [book, open]);

    const handleClose = () => {
        if (!loading) {
            setTitle('');
            setAnnotation('');
            setDocdate('');
            setLang('');
            setAuthors([]);
            setSeries([]);
            setError(null);
            setSuccess(false);
            setTitleError('');
            onClose();
        }
    };

    const validateForm = (): boolean => {
        let isValid = true;

        if (!title.trim()) {
            setTitleError(t('titleRequired'));
            isValid = false;
        } else {
            setTitleError('');
        }

        return isValid;
    };

    const handleSave = async () => {
        if (!book) return;

        if (!validateForm()) {
            return;
        }

        setLoading(true);
        setError(null);
        setSuccess(false);

        try {
            const updatedBook = {
                ...book,
                title: title.trim(),
                annotation: annotation.trim(),
                docdate: docdate.trim(),
                lang: lang.trim(),
                // For now, we keep authors and series unchanged
                // In future, you can add functionality to edit these
                authors: authors,
                series: series,
            };

            const response = await fetchWithAuth.put(`/admin/books/${book.id}`, updatedBook);

            if (response.status === 200) {
                setSuccess(true);
                // Backend returns data in format: { result: Book, error: null }
                const updatedBookData = response.data.result || response.data;
                onBookUpdated(updatedBookData);

                // Close dialog after a short delay to show success message
                setTimeout(() => {
                    handleClose();
                }, 1000);
            } else {
                setError(t('errorUpdatingBook'));
            }
        } catch (err: any) {
            console.error('Error updating book:', err);
            if (err.response?.data?.detail) {
                setError(err.response.data.detail);
            } else {
                setError(t('errorUpdatingBook'));
            }
        } finally {
            setLoading(false);
        }
    };

    if (!book) return null;

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
                <Typography variant="h6">{t('editBook')}</Typography>
                <IconButton onClick={handleClose} size="small" disabled={loading}>
                    <CloseIcon />
                </IconButton>
            </DialogTitle>

            <DialogContent dividers>
                {error && (
                    <Alert severity="error" sx={{ mb: 2 }} onClose={() => setError(null)}>
                        {error}
                    </Alert>
                )}

                {success && (
                    <Alert severity="success" sx={{ mb: 2 }}>
                        {t('bookUpdatedSuccessfully')}
                    </Alert>
                )}

                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                    <StyledTextField
                        autoFocus
                        label={t('title')}
                        type="text"
                        fullWidth
                        required
                        value={title}
                        onChange={(e) => setTitle(e.target.value)}
                        error={!!titleError}
                        helperText={titleError}
                        disabled={loading}
                    />

                    <StyledTextField
                        label={t('annotation')}
                        type="text"
                        fullWidth
                        multiline
                        rows={6}
                        value={annotation}
                        onChange={(e) => setAnnotation(e.target.value)}
                        disabled={loading}
                    />

                    <StyledTextField
                        label={t('publicationDate')}
                        type="text"
                        fullWidth
                        value={docdate}
                        onChange={(e) => setDocdate(e.target.value)}
                        disabled={loading}
                        placeholder="YYYY-MM-DD"
                        helperText={t('dateFormatHint')}
                    />

                    <StyledTextField
                        label={t('language')}
                        type="text"
                        fullWidth
                        value={lang}
                        onChange={(e) => setLang(e.target.value)}
                        disabled={loading}
                        placeholder="en, ru, uk, etc."
                        helperText={t('languageCodeHint')}
                    />

                    <Box>
                        <Typography variant="subtitle2" color="text.secondary" gutterBottom>
                            {t('authors')}
                        </Typography>
                        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1 }}>
                            {authors.length > 0 ? (
                                authors.map((author) => (
                                    <Chip
                                        key={author.id}
                                        label={author.full_name}
                                        variant="outlined"
                                        size="small"
                                    />
                                ))
                            ) : (
                                <Typography variant="body2" color="text.secondary">
                                    {t('noAuthors')}
                                </Typography>
                            )}
                        </Box>
                        <Typography variant="caption" color="text.secondary" sx={{ mt: 0.5, display: 'block' }}>
                            {t('authorsEditingNotImplemented')}
                        </Typography>
                    </Box>

                    <Box>
                        <Typography variant="subtitle2" color="text.secondary" gutterBottom>
                            {t('series')}
                        </Typography>
                        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1 }}>
                            {series.length > 0 ? (
                                series.map((s) => (
                                    <Chip
                                        key={s.id}
                                        label={`${s.ser}${s.ser_no ? ' #' + s.ser_no : ''}`}
                                        variant="outlined"
                                        size="small"
                                    />
                                ))
                            ) : (
                                <Typography variant="body2" color="text.secondary">
                                    {t('noSeries')}
                                </Typography>
                            )}
                        </Box>
                        <Typography variant="caption" color="text.secondary" sx={{ mt: 0.5, display: 'block' }}>
                            {t('seriesEditingNotImplemented')}
                        </Typography>
                    </Box>

                    <Box sx={{ mt: 1, p: 2, bgcolor: 'action.hover', borderRadius: 1 }}>
                        <Typography variant="caption" color="text.secondary" display="block">
                            <strong>{t('bookId')}:</strong> {book.id}
                        </Typography>
                        <Typography variant="caption" color="text.secondary" display="block">
                            <strong>{t('filename')}:</strong> {book.filename}
                        </Typography>
                        <Typography variant="caption" color="text.secondary" display="block">
                            <strong>{t('format')}:</strong> {book.format}
                        </Typography>
                        <Typography variant="caption" color="text.secondary" display="block">
                            <strong>{t('bookAdded')}:</strong> {book.registerdate}
                        </Typography>
                    </Box>
                </Box>
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
                    onClick={handleSave} 
                    variant="contained"
                    disabled={loading}
                    startIcon={loading ? <CircularProgress size={16} /> : null}
                >
                    {loading ? t('saving') : t('save')}
                </Button>
            </DialogActions>
        </Dialog>
    );
};

export default EditBookDialog;
