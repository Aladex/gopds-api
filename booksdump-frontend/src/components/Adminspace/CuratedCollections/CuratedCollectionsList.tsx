import React, { useCallback, useEffect, useState } from 'react';
import {
    Alert,
    Box,
    Card,
    CardContent,
    Chip,
    IconButton,
    Pagination,
    Stack,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableRow,
    Typography,
} from '@mui/material';
import DeleteIcon from '@mui/icons-material/Delete';
import { useTranslation } from 'react-i18next';
import { Link as RouterLink } from 'react-router-dom';
import { CuratedCollection, deleteCuratedCollection, listCuratedCollections } from './api';
import ImportForm from './ImportForm';
import BatchImportForm from './BatchImportForm';

const PAGE_SIZE = 25;

const CuratedCollectionsList: React.FC = () => {
    const { t } = useTranslation();
    const [rows, setRows] = useState<CuratedCollection[]>([]);
    const [page, setPage] = useState(1);
    const [total, setTotal] = useState(0);
    const [loadError, setLoadError] = useState<string | null>(null);

    const refresh = useCallback(async (targetPage: number = page) => {
        try {
            const data = await listCuratedCollections(targetPage, PAGE_SIZE);
            setRows(data.rows);
            setTotal(data.total);
            setPage(data.page);
        } catch (err: any) {
            setLoadError(err?.message ?? 'failed');
        }
    }, [page]);

    useEffect(() => {
        refresh(page);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [page]);

    const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

    const onDelete = async (id: number) => {
        if (!window.confirm(t('curatedCollections.confirmDelete', 'Delete this collection?'))) return;
        await deleteCuratedCollection(id);
        await refresh();
    };

    return (
        <Box>
            <Typography variant="h5" gutterBottom>
                {t('curatedCollections.title', 'Curated collections')}
            </Typography>

            <ImportForm onCreated={() => refresh(1)} />
            <BatchImportForm onCreated={() => refresh(1)} />

            {loadError && <Alert severity="error" sx={{ mt: 2 }}>{loadError}</Alert>}

            <Card sx={{ mt: 2 }}>
                <CardContent>
                    <Table size="small">
                        <TableHead>
                            <TableRow>
                                <TableCell>{t('curatedCollections.name', 'Name')}</TableCell>
                                <TableCell>{t('curatedCollections.status', 'Status')}</TableCell>
                                <TableCell>{t('curatedCollections.matched', 'Matched')}</TableCell>
                                <TableCell>{t('curatedCollections.ambiguous', 'Ambiguous')}</TableCell>
                                <TableCell>{t('curatedCollections.notFound', 'Not found')}</TableCell>
                                <TableCell>{t('curatedCollections.public', 'Public')}</TableCell>
                                <TableCell></TableCell>
                            </TableRow>
                        </TableHead>
                        <TableBody>
                            {rows.map((row) => (
                                <TableRow key={row.id} hover>
                                    <TableCell>
                                        <RouterLink to={`/admin/collections/${row.id}`}>{row.name}</RouterLink>
                                    </TableCell>
                                    <TableCell>{row.import_status ?? '-'}</TableCell>
                                    <TableCell>{row.import_stats?.matched ?? 0}</TableCell>
                                    <TableCell>{row.import_stats?.ambiguous ?? 0}</TableCell>
                                    <TableCell>{row.import_stats?.not_found ?? 0}</TableCell>
                                    <TableCell>
                                        {row.is_public ? (
                                            <Chip size="small" label="public" color="success" />
                                        ) : (
                                            <Chip size="small" label="draft" />
                                        )}
                                    </TableCell>
                                    <TableCell>
                                        <IconButton size="small" onClick={() => onDelete(row.id)}>
                                            <DeleteIcon fontSize="small" />
                                        </IconButton>
                                    </TableCell>
                                </TableRow>
                            ))}
                            {rows.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={7}>
                                        <Typography variant="body2" color="text.secondary" align="center">
                                            {t('curatedCollections.empty', 'No curated collections yet')}
                                        </Typography>
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                    {totalPages > 1 && (
                        <Stack alignItems="center" mt={2}>
                            <Pagination
                                count={totalPages}
                                page={page}
                                onChange={(_, p) => setPage(p)}
                                size="small"
                            />
                        </Stack>
                    )}
                </CardContent>
            </Card>
        </Box>
    );
};

export default CuratedCollectionsList;
