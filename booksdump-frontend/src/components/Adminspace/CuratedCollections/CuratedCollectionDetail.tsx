import React, { useCallback, useEffect, useState } from 'react';
import {
    Alert,
    Box,
    Button,
    Card,
    CardContent,
    Chip,
    IconButton,
    Stack,
    Tab,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableRow,
    Tabs,
    TextField,
    Typography,
} from '@mui/material';
import DeleteIcon from '@mui/icons-material/Delete';
import { useNavigate, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
    CollectionItem,
    CuratedCollection,
    deleteCuratedCollection,
    getCuratedCollection,
    getImportStatus,
    ignoreItem,
    ImportStatusInfo,
    listCollectionItems,
    patchCuratedCollection,
    resolveItem,
} from './api';

const STATUS_TABS = [
    { key: 'auto_matched', label: 'Auto / Manual' }, // matched + manual handled by API: filter applies to one bucket. We'll handle 'matched' (auto+manual) by querying twice OR fall back to single filter — the tab key is what we send.
    { key: 'ambiguous', label: 'Ambiguous' },
    { key: 'not_found', label: 'Not found' },
    { key: 'ignored', label: 'Ignored' },
];

const POLLING_INTERVAL_MS = 2500;

const ItemsTable: React.FC<{
    items: CollectionItem[];
    statusKey: string;
    onResolve: (itemID: number, bookID: number) => Promise<void>;
    onIgnore: (itemID: number) => Promise<void>;
}> = ({ items, statusKey, onResolve, onIgnore }) => {
    const [resolving, setResolving] = useState<Record<number, string>>({});

    const submit = async (itemID: number) => {
        const raw = resolving[itemID] ?? '';
        const id = parseInt(raw, 10);
        if (Number.isNaN(id) || id <= 0) return;
        await onResolve(itemID, id);
        setResolving((prev) => {
            const next = { ...prev };
            delete next[itemID];
            return next;
        });
    };

    return (
        <Table size="small">
            <TableHead>
                <TableRow>
                    <TableCell>#</TableCell>
                    <TableCell>Title</TableCell>
                    <TableCell>Author</TableCell>
                    <TableCell>Score</TableCell>
                    <TableCell>Book</TableCell>
                    <TableCell></TableCell>
                </TableRow>
            </TableHead>
            <TableBody>
                {items.map((it) => (
                    <TableRow key={it.id} hover>
                        <TableCell>{it.position + 1}</TableCell>
                        <TableCell>{it.external_title}</TableCell>
                        <TableCell>{it.external_author}</TableCell>
                        <TableCell>{it.match_score?.toFixed?.(2) ?? '—'}</TableCell>
                        <TableCell>
                            {it.book_id ? <Chip size="small" label={`#${it.book_id}`} /> : '—'}
                        </TableCell>
                        <TableCell>
                            {(statusKey === 'ambiguous' || statusKey === 'not_found') && (
                                <Stack direction="row" spacing={1} alignItems="center">
                                    <TextField
                                        size="small"
                                        placeholder="book_id"
                                        value={resolving[it.id] ?? ''}
                                        onChange={(e) =>
                                            setResolving((p) => ({ ...p, [it.id]: e.target.value }))
                                        }
                                        sx={{ width: 100 }}
                                    />
                                    <Button size="small" variant="outlined" onClick={() => submit(it.id)}>
                                        Resolve
                                    </Button>
                                    <IconButton size="small" onClick={() => onIgnore(it.id)} title="Ignore">
                                        <DeleteIcon fontSize="small" />
                                    </IconButton>
                                </Stack>
                            )}
                        </TableCell>
                    </TableRow>
                ))}
                {items.length === 0 && (
                    <TableRow>
                        <TableCell colSpan={6}>
                            <Typography variant="body2" color="text.secondary" align="center">
                                Empty
                            </Typography>
                        </TableCell>
                    </TableRow>
                )}
            </TableBody>
        </Table>
    );
};

const CuratedCollectionDetail: React.FC = () => {
    const { t } = useTranslation();
    const { id: idParam } = useParams<{ id: string }>();
    const navigate = useNavigate();
    const id = idParam ? parseInt(idParam, 10) : 0;

    const [coll, setColl] = useState<CuratedCollection | null>(null);
    const [status, setStatus] = useState<ImportStatusInfo | null>(null);
    const [tabKey, setTabKey] = useState<string>('auto_matched');
    const [items, setItems] = useState<CollectionItem[]>([]);
    const [loadErr, setLoadErr] = useState<string | null>(null);

    const loadCollection = useCallback(async () => {
        try {
            const c = await getCuratedCollection(id);
            setColl(c);
        } catch (err: any) {
            setLoadErr(err?.message ?? 'load failed');
        }
    }, [id]);

    const loadStatus = useCallback(async () => {
        try {
            const s = await getImportStatus(id);
            setStatus(s);
            return s;
        } catch {
            return null;
        }
    }, [id]);

    const loadItems = useCallback(
        async (key: string) => {
            try {
                const page = await listCollectionItems(id, key);
                setItems(page.items);
            } catch (err: any) {
                setLoadErr(err?.message ?? 'load items failed');
            }
        },
        [id],
    );

    // Initial load + status polling.
    useEffect(() => {
        if (!id) return;
        loadCollection();
        loadItems(tabKey);
    }, [id, tabKey, loadCollection, loadItems]);

    useEffect(() => {
        if (!id) return;
        let cancelled = false;
        const tick = async () => {
            const s = await loadStatus();
            if (cancelled) return;
            if (s && s.status === 'importing') {
                setTimeout(tick, POLLING_INTERVAL_MS);
            } else if (s) {
                // refresh data once import completes
                loadCollection();
                loadItems(tabKey);
            }
        };
        tick();
        return () => {
            cancelled = true;
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [id]);

    const togglePublish = async () => {
        if (!coll) return;
        await patchCuratedCollection(coll.id, { is_public: !coll.is_public });
        loadCollection();
    };

    const onDelete = async () => {
        if (!coll) return;
        if (!window.confirm(t('curatedCollections.confirmDelete', 'Delete this collection?'))) return;
        await deleteCuratedCollection(coll.id);
        navigate('/admin/collections');
    };

    const onResolve = async (itemID: number, bookID: number) => {
        await resolveItem(id, itemID, bookID);
        loadItems(tabKey);
        loadStatus();
    };

    const onIgnore = async (itemID: number) => {
        await ignoreItem(id, itemID);
        loadItems(tabKey);
        loadStatus();
    };

    if (!id) return <Alert severity="error">invalid id</Alert>;
    if (loadErr) return <Alert severity="error">{loadErr}</Alert>;
    if (!coll) return <Typography>Loading…</Typography>;

    const stats = status?.stats ?? coll.import_stats ?? {};
    const importing = (status?.status ?? coll.import_status) === 'importing';

    return (
        <Box>
            <Stack direction="row" alignItems="center" spacing={2} mb={2}>
                <Typography variant="h5">{coll.name}</Typography>
                {coll.is_public ? (
                    <Chip size="small" label="public" color="success" />
                ) : (
                    <Chip size="small" label="draft" />
                )}
                {importing && <Chip size="small" label="importing…" color="warning" />}
            </Stack>

            <Card sx={{ mb: 2 }}>
                <CardContent>
                    <Stack direction="row" spacing={2}>
                        <Typography variant="body2">
                            {t('curatedCollections.matched', 'Matched')}: {stats.matched ?? 0}
                        </Typography>
                        <Typography variant="body2">
                            {t('curatedCollections.ambiguous', 'Ambiguous')}: {stats.ambiguous ?? 0}
                        </Typography>
                        <Typography variant="body2">
                            {t('curatedCollections.notFound', 'Not found')}: {stats.not_found ?? 0}
                        </Typography>
                    </Stack>
                    {status?.import_error && (
                        <Alert severity="error" sx={{ mt: 1 }}>
                            {status.import_error}
                        </Alert>
                    )}
                    <Stack direction="row" spacing={1} mt={2}>
                        <Button variant="contained" size="small" onClick={togglePublish} disabled={importing}>
                            {coll.is_public
                                ? t('curatedCollections.unpublish', 'Unpublish')
                                : t('curatedCollections.publish', 'Publish')}
                        </Button>
                        <Button variant="outlined" color="error" size="small" onClick={onDelete}>
                            {t('curatedCollections.delete', 'Delete')}
                        </Button>
                    </Stack>
                </CardContent>
            </Card>

            <Card>
                <CardContent>
                    <Tabs value={tabKey} onChange={(_, v) => setTabKey(v)}>
                        {STATUS_TABS.map((tab) => (
                            <Tab key={tab.key} value={tab.key} label={tab.label} />
                        ))}
                    </Tabs>
                    <Box mt={2}>
                        <ItemsTable
                            items={items}
                            statusKey={tabKey}
                            onResolve={onResolve}
                            onIgnore={onIgnore}
                        />
                    </Box>
                </CardContent>
            </Card>
        </Box>
    );
};

export default CuratedCollectionDetail;
