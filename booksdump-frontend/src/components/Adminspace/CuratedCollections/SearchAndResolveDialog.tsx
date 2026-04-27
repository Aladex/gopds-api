import React, { useEffect, useState } from 'react';
import {
    Box,
    Button,
    Dialog,
    DialogActions,
    DialogContent,
    DialogTitle,
    Stack,
    TextField,
    Typography,
} from '@mui/material';
import { useTranslation } from 'react-i18next';
import { fetchWithAuth } from '../../../api/config';

interface SearchHit {
    id: number;
    title: string;
    authors?: { id: number; full_name: string }[];
}

// SearchAndResolveDialog opens an inline title search against /api/books/list
// so the admin can pick the right local book for a not_found / wrongly-resolved
// item without leaving the curated-collection page.
const SearchAndResolveDialog: React.FC<{
    open: boolean;
    initialQuery: string;
    onClose: () => void;
    onPick: (bookID: number) => Promise<void>;
}> = ({ open, initialQuery, onClose, onPick }) => {
    const { t } = useTranslation();
    const [query, setQuery] = useState(initialQuery);
    const [hits, setHits] = useState<SearchHit[]>([]);
    const [loading, setLoading] = useState(false);
    const [busy, setBusy] = useState(false);

    useEffect(() => {
        if (open) setQuery(initialQuery);
    }, [open, initialQuery]);

    const search = async () => {
        if (!query.trim()) return;
        setLoading(true);
        try {
            const resp = await fetchWithAuth.get('/books/list', {
                params: { title: query.trim(), limit: 20, offset: 0 },
            });
            setHits(resp.data?.books ?? []);
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        if (open && initialQuery.trim()) {
            // auto-search on open
            (async () => {
                setLoading(true);
                try {
                    const resp = await fetchWithAuth.get('/books/list', {
                        params: { title: initialQuery.trim(), limit: 20, offset: 0 },
                    });
                    setHits(resp.data?.books ?? []);
                } finally {
                    setLoading(false);
                }
            })();
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open]);

    const pick = async (id: number) => {
        if (busy) return;
        setBusy(true);
        try {
            await onPick(id);
            onClose();
        } finally {
            setBusy(false);
        }
    };

    return (
        <Dialog open={open} onClose={onClose} maxWidth="md" fullWidth>
            <DialogTitle>{t('curatedCollections.searchDialog.title', 'Find a book in the library')}</DialogTitle>
            <DialogContent>
                <Stack direction="row" spacing={1} alignItems="center" sx={{ mb: 2 }}>
                    <TextField
                        size="small"
                        fullWidth
                        autoFocus
                        value={query}
                        onChange={(e) => setQuery(e.target.value)}
                        onKeyDown={(e) => {
                            if (e.key === 'Enter') {
                                e.preventDefault();
                                search();
                            }
                        }}
                        label={t('curatedCollections.searchDialog.queryLabel', 'Title')}
                    />
                    <Button onClick={search} variant="outlined" disabled={loading}>
                        {t('curatedCollections.searchDialog.searchBtn', 'Search')}
                    </Button>
                </Stack>

                {loading && (
                    <Typography variant="body2" color="text.secondary">
                        {t('curatedCollections.searchDialog.loading', 'Searching…')}
                    </Typography>
                )}

                {!loading && hits.length === 0 && (
                    <Typography variant="body2" color="text.secondary">
                        {t('curatedCollections.searchDialog.empty', 'No matches yet')}
                    </Typography>
                )}

                <Stack spacing={1}>
                    {hits.map((b) => (
                        <Box
                            key={b.id}
                            sx={{
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'space-between',
                                gap: 1,
                                p: 1,
                                border: 1,
                                borderColor: 'divider',
                                borderRadius: 1,
                            }}
                        >
                            <Box sx={{ overflow: 'hidden' }}>
                                <Typography variant="body2" noWrap>
                                    {b.title}
                                </Typography>
                                <Typography variant="caption" color="text.secondary" noWrap>
                                    #{b.id} · {(b.authors ?? []).map((a) => a.full_name).join(', ')}
                                </Typography>
                            </Box>
                            <Button size="small" variant="contained" disabled={busy} onClick={() => pick(b.id)}>
                                {t('curatedCollections.resolve', 'Resolve')}
                            </Button>
                        </Box>
                    ))}
                </Stack>
            </DialogContent>
            <DialogActions>
                <Button onClick={onClose}>{t('curatedCollections.searchDialog.close', 'Close')}</Button>
            </DialogActions>
        </Dialog>
    );
};

export default SearchAndResolveDialog;
