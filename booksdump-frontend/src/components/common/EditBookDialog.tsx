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
    
    // Authors and series
    const [authors, setAuthors] = useState<Author[]>([]);
    const [series, setSeries] = useState<Series[]>([]);

    // Available authors and series for autocomplete
    const [availableAuthors, setAvailableAuthors] = useState<Author[]>([]);
    const [availableSeries, setAvailableSeries] = useState<Series[]>([]);
    const [authorsQuery, setAuthorsQuery] = useState('');
    const [seriesQuery, setSeriesQuery] = useState('');
    const [authorsLoading, setAuthorsLoading] = useState(false);
    const [seriesLoading, setSeriesLoading] = useState(false);

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
            setAuthorsQuery('');
            setSeriesQuery('');
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
            setAuthorsQuery('');
            setSeriesQuery('');
            setError(null);
            setSuccess(false);
            setTitleError('');
            onClose();
        }
    };

    useEffect(() => {
        if (!open) return;
        const query = authorsQuery.trim();
        if (query.length < 2) {
            setAvailableAuthors([]);
            setAuthorsLoading(false);
            return;
        }

        let active = true;
        setAuthorsLoading(true);
        const timer = setTimeout(async () => {
            try {
                const response = await fetchWithAuth.get('/admin/authors/search', {
                    params: { q: query, limit: 20 },
                });
                if (!active) return;
                setAvailableAuthors(response.data?.authors || []);
            } catch (err) {
                if (!active) return;
                setAvailableAuthors([]);
            } finally {
                if (active) {
                    setAuthorsLoading(false);
                }
            }
        }, 250);

        return () => {
            active = false;
            clearTimeout(timer);
        };
    }, [authorsQuery, open]);

    useEffect(() => {
        if (!open) return;
        const query = seriesQuery.trim();
        if (query.length < 2) {
            setAvailableSeries([]);
            setSeriesLoading(false);
            return;
        }

        let active = true;
        setSeriesLoading(true);
        const timer = setTimeout(async () => {
            try {
                const response = await fetchWithAuth.get('/admin/series/search', {
                    params: { q: query, limit: 20 },
                });
                if (!active) return;
                setAvailableSeries(response.data?.series || []);
            } catch (err) {
                if (!active) return;
                setAvailableSeries([]);
            } finally {
                if (active) {
                    setSeriesLoading(false);
                }
            }
        }, 250);

        return () => {
            active = false;
            clearTimeout(timer);
        };
    }, [seriesQuery, open]);

    const normalizeAuthors = (values: Array<Author | string>): Author[] => {
        const next: Author[] = [];
        const seenIDs = new Set<number>();
        const seenNames = new Set<string>();

        values.forEach((value) => {
            if (typeof value === 'string') {
                const name = value.trim();
                if (!name) return;
                const normalized = name.toLowerCase();
                if (seenNames.has(normalized)) return;
                next.push({ id: 0, full_name: name });
                seenNames.add(normalized);
                return;
            }

            if (value.id && seenIDs.has(value.id)) return;
            const name = value.full_name?.trim();
            if (value.id) {
                seenIDs.add(value.id);
            } else if (name) {
                const normalized = name.toLowerCase();
                if (seenNames.has(normalized)) return;
                seenNames.add(normalized);
            }
            next.push(value);
        });

        return next;
    };

    const normalizeSeries = (values: Array<Series | string>): Series[] => {
        const next: Series[] = [];
        const seenIDs = new Set<number>();
        const seenNames = new Set<string>();

        values.forEach((value) => {
            if (typeof value === 'string') {
                const name = value.trim();
                if (!name) return;
                const normalized = name.toLowerCase();
                if (seenNames.has(normalized)) return;
                next.push({ id: 0, ser: name, ser_no: 0 });
                seenNames.add(normalized);
                return;
            }

            if (value.id && seenIDs.has(value.id)) return;
            const name = value.ser?.trim();
            if (value.id) {
                seenIDs.add(value.id);
            } else if (name) {
                const normalized = name.toLowerCase();
                if (seenNames.has(normalized)) return;
                seenNames.add(normalized);
            }
            next.push(value);
        });

        return next;
    };

    const updateSeriesNumber = (seriesID: number, seriesName: string, rawValue: string) => {
        const trimmed = rawValue.trim();
        let nextValue = 0;
        if (trimmed !== '') {
            const parsed = Number(trimmed);
            if (!Number.isNaN(parsed)) {
                nextValue = Math.max(0, Math.trunc(parsed));
            }
        }

        setSeries((prev) =>
            prev.map((entry) =>
                entry.id === seriesID && entry.ser === seriesName
                    ? {
                          ...entry,
                          ser_no: nextValue,
                      }
                    : entry
            )
        );
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

                    <Autocomplete
                        multiple
                        freeSolo
                        filterSelectedOptions
                        options={availableAuthors}
                        value={authors}
                        inputValue={authorsQuery}
                        onInputChange={(_, newInput, reason) => {
                            if (reason !== 'reset') {
                                setAuthorsQuery(newInput);
                            }
                        }}
                        onChange={(_, newValue) => {
                            setAuthors(normalizeAuthors(newValue));
                        }}
                        getOptionLabel={(option) =>
                            typeof option === 'string' ? option : option.full_name
                        }
                        isOptionEqualToValue={(option, value) =>
                            option.id !== 0 && value.id !== 0
                                ? option.id === value.id
                                : option.full_name === value.full_name
                        }
                        filterOptions={(options) => options}
                        loading={authorsLoading}
                        renderTags={(value, getTagProps) =>
                            value.map((option, index) => (
                                <Chip
                                    variant="outlined"
                                    label={option.full_name}
                                    size="small"
                                    {...getTagProps({ index })}
                                />
                            ))
                        }
                        renderInput={(params) => (
                            <StyledTextField
                                {...params}
                                label={t('authors')}
                                placeholder={t('authorsSearchOrCreate')}
                                helperText={t('authorsEditHint')}
                                disabled={loading}
                            />
                        )}
                    />

                    <Autocomplete
                        multiple
                        freeSolo
                        filterSelectedOptions
                        options={availableSeries}
                        value={series}
                        inputValue={seriesQuery}
                        onInputChange={(_, newInput, reason) => {
                            if (reason !== 'reset') {
                                setSeriesQuery(newInput);
                            }
                        }}
                        onChange={(_, newValue) => {
                            setSeries(normalizeSeries(newValue));
                        }}
                        getOptionLabel={(option) =>
                            typeof option === 'string' ? option : option.ser
                        }
                        isOptionEqualToValue={(option, value) =>
                            option.id !== 0 && value.id !== 0
                                ? option.id === value.id
                                : option.ser === value.ser
                        }
                        filterOptions={(options) => options}
                        loading={seriesLoading}
                        renderTags={(value, getTagProps) =>
                            value.map((option, index) => (
                                <Chip
                                    variant="outlined"
                                    label={`${option.ser}${option.ser_no ? ' #' + option.ser_no : ''}`}
                                    size="small"
                                    {...getTagProps({ index })}
                                />
                            ))
                        }
                        renderInput={(params) => (
                            <StyledTextField
                                {...params}
                                label={t('series')}
                                placeholder={t('seriesSearchOrCreate')}
                                helperText={t('seriesEditHint')}
                                disabled={loading}
                            />
                        )}
                    />

                    {series.length > 0 && (
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                            <Typography variant="caption" color="text.secondary">
                                {t('seriesNumberHint')}
                            </Typography>
                            {series.map((entry) => (
                                <StyledTextField
                                    key={`series-number-${entry.id}-${entry.ser}`}
                                    label={`${entry.ser} ${t('seriesNumber')}`}
                                    type="number"
                                    value={entry.ser_no ? entry.ser_no : ''}
                                    onChange={(e) =>
                                        updateSeriesNumber(entry.id, entry.ser, e.target.value)
                                    }
                                    disabled={loading}
                                    inputProps={{ min: 0 }}
                                />
                            ))}
                        </Box>
                    )}

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
