import React, { useCallback, useEffect, useState } from 'react';
import {
    Alert,
    Box,
    Button,
    Card,
    CardContent,
    Chip,
    IconButton,
    LinearProgress,
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
import SearchIcon from '@mui/icons-material/Search';
import SearchAndResolveDialog from './SearchAndResolveDialog';
import { useNavigate, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
    autoResolveCollection,
    CollectionItem,
    CuratedCollection,
    deleteCuratedCollection,
    getCuratedCollection,
    getImportStatus,
    ignoreItem,
    ImportStatusInfo,
    listCollectionItems,
    llmResolveCollection,
    llmSearchNotFound,
    LookupBook,
    lookupBooksByIDs,
    patchCuratedCollection,
    resolveItem,
} from './api';

const POLLING_INTERVAL_MS = 2500;

interface CandidateInfo {
    book_id: number;
    score: number;
}

// readCandidates safely extracts the candidate list saved by the backend in
// external_extra.candidates during ambiguous matching.
function readCandidates(extra: any): CandidateInfo[] {
    if (!extra || !Array.isArray(extra.candidates)) return [];
    return extra.candidates
        .filter((c: any) => c && typeof c.book_id === 'number')
        .map((c: any) => ({ book_id: c.book_id, score: typeof c.score === 'number' ? c.score : 0 }));
}

const ItemsTable: React.FC<{
    items: CollectionItem[];
    statusKey: string;
    bookInfo: Map<number, LookupBook>;
    onResolve: (itemID: number, bookID: number) => Promise<void>;
    onIgnore: (itemID: number) => Promise<void>;
}> = ({ items, statusKey, bookInfo, onResolve, onIgnore }) => {
    const { t } = useTranslation();
    const [manualID, setManualID] = useState<Record<number, string>>({});
    const [busy, setBusy] = useState<Record<number, boolean>>({});
    const [searchFor, setSearchFor] = useState<{ itemID: number; query: string } | null>(null);

    const setItemBusy = (itemID: number, v: boolean) =>
        setBusy((prev) => ({ ...prev, [itemID]: v }));

    const resolveTo = async (itemID: number, bookID: number) => {
        if (busy[itemID]) return;
        setItemBusy(itemID, true);
        try {
            await onResolve(itemID, bookID);
            setManualID((prev) => {
                const next = { ...prev };
                delete next[itemID];
                return next;
            });
        } finally {
            setItemBusy(itemID, false);
        }
    };

    const submitManual = (itemID: number) => {
        const raw = manualID[itemID] ?? '';
        const id = parseInt(raw, 10);
        if (Number.isNaN(id) || id <= 0) return;
        return resolveTo(itemID, id);
    };

    const ignore = async (itemID: number) => {
        if (busy[itemID]) return;
        setItemBusy(itemID, true);
        try {
            await onIgnore(itemID);
        } finally {
            setItemBusy(itemID, false);
        }
    };

    // Resolution controls are always available — admin can re-resolve a
    // previously matched item (typo, wrong edition picked) or rescue an
    // ignored one by submitting a fresh book_id.
    const isResolvable = true;

    return (
      <>
        <Table size="small">
            <TableHead>
                <TableRow>
                    <TableCell>#</TableCell>
                    <TableCell>{t('curatedCollections.col.title', 'Title')}</TableCell>
                    <TableCell>{t('curatedCollections.col.author', 'Author')}</TableCell>
                    <TableCell>{t('curatedCollections.col.score', 'Score')}</TableCell>
                    <TableCell>{t('curatedCollections.col.candidates', 'Candidates / Book')}</TableCell>
                    <TableCell></TableCell>
                </TableRow>
            </TableHead>
            <TableBody>
                {items.map((it) => {
                    const candidates = readCandidates(it.external_extra);
                    return (
                        <TableRow key={it.id} hover>
                            <TableCell>{it.position + 1}</TableCell>
                            <TableCell>{it.external_title}</TableCell>
                            <TableCell>{it.external_author}</TableCell>
                            <TableCell>{it.match_score?.toFixed?.(2) ?? '—'}</TableCell>
                            <TableCell sx={{ minWidth: 360, maxWidth: 520 }}>
                                {it.book_id ? (
                                    (() => {
                                        const b = bookInfo.get(it.book_id);
                                        const label = b
                                            ? `${b.title} — ${(b.authors ?? []).map((a) => a.full_name).join(', ') || '?'}`
                                            : `#${it.book_id}`;
                                        return (
                                            <Chip
                                                size="small"
                                                color="success"
                                                label={`#${it.book_id} · ${label}`}
                                                sx={{ maxWidth: '100%', '& .MuiChip-label': { whiteSpace: 'normal' } }}
                                            />
                                        );
                                    })()
                                ) : candidates.length > 0 ? (
                                    <Stack spacing={0.5}>
                                        {candidates.map((c) => {
                                            const b = bookInfo.get(c.book_id);
                                            const main = b
                                                ? `${b.title} — ${(b.authors ?? []).map((a) => a.full_name).join(', ') || '?'}`
                                                : `#${c.book_id}`;
                                            return (
                                                <Chip
                                                    key={c.book_id}
                                                    size="small"
                                                    clickable
                                                    disabled={!!busy[it.id]}
                                                    onClick={() => resolveTo(it.id, c.book_id)}
                                                    label={`${c.score.toFixed(2)} · ${main}`}
                                                    sx={{
                                                        maxWidth: '100%',
                                                        justifyContent: 'flex-start',
                                                        '& .MuiChip-label': { whiteSpace: 'normal' },
                                                    }}
                                                />
                                            );
                                        })}
                                    </Stack>
                                ) : (
                                    <Typography variant="caption" color="text.secondary">
                                        {t('curatedCollections.noCandidates', 'no candidates')}
                                    </Typography>
                                )}
                            </TableCell>
                            <TableCell>
                                {isResolvable && (
                                    <Stack direction="row" spacing={1} alignItems="center">
                                        <IconButton
                                            size="small"
                                            onClick={() => setSearchFor({ itemID: it.id, query: it.external_title })}
                                            title={t('curatedCollections.searchInLibrary', 'Search by title in library')}
                                        >
                                            <SearchIcon fontSize="small" />
                                        </IconButton>
                                        <TextField
                                            size="small"
                                            placeholder={t('curatedCollections.bookIdPlaceholder', 'book_id')}
                                            value={manualID[it.id] ?? ''}
                                            onChange={(e) =>
                                                setManualID((p) => ({ ...p, [it.id]: e.target.value }))
                                            }
                                            sx={{ width: 100 }}
                                        />
                                        <Button
                                            size="small"
                                            variant="outlined"
                                            disabled={!!busy[it.id]}
                                            onClick={() => submitManual(it.id)}
                                        >
                                            {t('curatedCollections.resolve', 'Resolve')}
                                        </Button>
                                        <IconButton
                                            size="small"
                                            onClick={() => ignore(it.id)}
                                            title={t('curatedCollections.ignoreAction', 'Ignore')}
                                            disabled={!!busy[it.id]}
                                        >
                                            <DeleteIcon fontSize="small" />
                                        </IconButton>
                                    </Stack>
                                )}
                            </TableCell>
                        </TableRow>
                    );
                })}
                {items.length === 0 && (
                    <TableRow>
                        <TableCell colSpan={6}>
                            <Typography variant="body2" color="text.secondary" align="center">
                                {t('curatedCollections.tabEmpty', 'Empty')}
                            </Typography>
                        </TableCell>
                    </TableRow>
                )}
            </TableBody>
        </Table>
        <SearchAndResolveDialog
            open={!!searchFor}
            initialQuery={searchFor?.query ?? ''}
            onClose={() => setSearchFor(null)}
            onPick={async (bookID) => {
                if (searchFor) await onResolve(searchFor.itemID, bookID);
            }}
        />
      </>
    );
};

const CuratedCollectionDetail: React.FC = () => {
    const { t } = useTranslation();
    const { id: idParam } = useParams<{ id: string }>();
    const navigate = useNavigate();
    const id = idParam ? parseInt(idParam, 10) : 0;

    const [coll, setColl] = useState<CuratedCollection | null>(null);
    const [status, setStatus] = useState<ImportStatusInfo | null>(null);
    const [tabKey, setTabKey] = useState<string>('matched');
    const [items, setItems] = useState<CollectionItem[]>([]);
    const [bookInfo, setBookInfo] = useState<Map<number, LookupBook>>(new Map());
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

                // Collect every book id we want to render — both already-resolved
                // items and ambiguous candidates — and ask the backend for titles
                // and authors so the chips show real metadata, not bare numbers.
                const ids = new Set<number>();
                for (const it of page.items) {
                    if (typeof it.book_id === 'number' && it.book_id > 0) ids.add(it.book_id);
                    const cands = readCandidates(it.external_extra);
                    for (const c of cands) ids.add(c.book_id);
                }
                if (ids.size > 0) {
                    const books = await lookupBooksByIDs(Array.from(ids));
                    setBookInfo((prev) => {
                        const next = new Map(prev);
                        for (const b of books) next.set(b.id, b);
                        return next;
                    });
                }
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

    const [autoResolving, setAutoResolving] = useState(false);
    const onAutoResolve = async () => {
        if (autoResolving) return;
        setAutoResolving(true);
        try {
            const { resolved } = await autoResolveCollection(id);
            await Promise.all([loadCollection(), loadItems(tabKey), loadStatus()]);
            if (typeof window !== 'undefined') {
                window.alert(
                    t('curatedCollections.autoResolveDone', '{{count}} items resolved', { count: resolved }),
                );
            }
        } finally {
            setAutoResolving(false);
        }
    };

    const [aiResolving, setAiResolving] = useState(false);
    const onAIResolve = async () => {
        if (aiResolving) return;
        setAiResolving(true);
        try {
            await llmResolveCollection(id);
            await loadStatus();
        } finally {
            setAiResolving(false);
        }
    };

    const [aiSearching, setAiSearching] = useState(false);
    const onAISearchNotFound = async () => {
        if (aiSearching) return;
        setAiSearching(true);
        try {
            await llmSearchNotFound(id);
            await loadStatus();
        } finally {
            setAiSearching(false);
        }
    };

    // Auto-poll while ai_progress.running is true. Survives a page refresh
    // because the running flag is persisted in the DB.
    useEffect(() => {
        const ai = status?.stats?.ai_progress;
        if (!ai?.running) return;
        let cancelled = false;
        const tick = async () => {
            if (cancelled) return;
            await loadStatus();
            await loadItems(tabKey);
            const cur = await getImportStatus(id);
            if (cancelled) return;
            if (cur.stats?.ai_progress?.running) {
                setTimeout(tick, 2500);
            } else {
                loadCollection();
                loadItems(tabKey);
            }
        };
        const t = setTimeout(tick, 2500);
        return () => {
            cancelled = true;
            clearTimeout(t);
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [status?.stats?.ai_progress?.running, id]);

    if (!id) return <Alert severity="error">invalid id</Alert>;
    if (loadErr) return <Alert severity="error">{loadErr}</Alert>;
    if (!coll) return <Typography>Loading…</Typography>;

    const stats: any = status?.stats ?? coll.import_stats ?? {};
    const importing = (status?.status ?? coll.import_status) === 'importing';
    const processed: number = stats.processed ?? 0;
    const total: number = stats.total ?? 0;
    const progressPct = total > 0 ? Math.min(100, Math.round((processed / total) * 100)) : 0;

    const statusTabs = [
        { key: 'matched', label: t('curatedCollections.tab.autoMatched', 'Matched') },
        { key: 'ambiguous', label: t('curatedCollections.tab.ambiguous', 'Ambiguous') },
        { key: 'not_found', label: t('curatedCollections.tab.notFound', 'Not found') },
        { key: 'ignored', label: t('curatedCollections.tab.ignored', 'Ignored') },
    ];

    return (
        <Box>
            <Stack direction="row" alignItems="center" spacing={2} mb={2}>
                <Typography variant="h5">{coll.name}</Typography>
                {coll.is_public ? (
                    <Chip size="small" label={t('curatedCollections.public', 'Public')} color="success" />
                ) : (
                    <Chip size="small" label={t('curatedCollections.draft', 'Draft')} />
                )}
                {importing && (
                    <Chip
                        size="small"
                        label={t('curatedCollections.importingChip', 'importing…')}
                        color="warning"
                    />
                )}
            </Stack>

            <Card sx={{ mb: 2 }}>
                <CardContent>
                    {importing && total > 0 && (
                        <Box mb={2}>
                            <Typography variant="body2" color="text.secondary" gutterBottom>
                                {t('curatedCollections.progress', 'Progress')}: {processed} / {total} ({progressPct}%)
                            </Typography>
                            <LinearProgress variant="determinate" value={progressPct} />
                        </Box>
                    )}
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
                    {status?.stats?.ai_progress && (
                        <Box mt={2} p={1.5} sx={{ border: 1, borderColor: 'divider', borderRadius: 1 }}>
                            <Stack direction="row" spacing={2} alignItems="center" mb={1}>
                                <Typography variant="body2">
                                    {status.stats.ai_progress.running
                                        ? t('curatedCollections.aiBoxRunning', 'AI resolving…')
                                        : t('curatedCollections.aiBoxIdle', 'AI last run')}
                                </Typography>
                                <Typography variant="body2" color="text.secondary">
                                    {status.stats.ai_progress.processed} / {status.stats.ai_progress.total}
                                    {' · '}
                                    {t('curatedCollections.aiBoxResolved', 'resolved')}: {status.stats.ai_progress.resolved}
                                </Typography>
                            </Stack>
                            {status.stats.ai_progress.total > 0 && (
                                <LinearProgress
                                    variant="determinate"
                                    value={Math.min(100, Math.round((status.stats.ai_progress.processed / status.stats.ai_progress.total) * 100))}
                                    sx={{ mb: 1 }}
                                />
                            )}
                            {status.stats.ai_progress.recent && status.stats.ai_progress.recent.length > 0 && (
                                <Box sx={{ fontSize: 13, fontFamily: 'monospace', color: 'text.secondary' }}>
                                    {status.stats.ai_progress.recent
                                        .slice()
                                        .reverse()
                                        .map((d, i) => (
                                            <Box key={i} sx={{ py: 0.25 }}>
                                                {d.action === 'resolved' ? '✓ ' : '· '}
                                                <strong>{d.external_title}</strong>
                                                {d.action === 'resolved' && d.book_title
                                                    ? ` → #${d.book_id} ${d.book_title}`
                                                    : ' — skipped'}
                                            </Box>
                                        ))}
                                </Box>
                            )}
                        </Box>
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
                        {statusTabs.map((tab) => (
                            <Tab key={tab.key} value={tab.key} label={tab.label} />
                        ))}
                    </Tabs>

                    {tabKey === 'ambiguous' && (
                        <Stack direction="row" spacing={1} mt={2}>
                            <Button
                                size="small"
                                variant="outlined"
                                disabled={autoResolving || aiResolving || aiSearching || importing || (stats.ambiguous ?? 0) === 0}
                                onClick={onAutoResolve}
                            >
                                {autoResolving
                                    ? t('curatedCollections.autoResolving', 'Resolving…')
                                    : t('curatedCollections.autoResolveAll', 'Auto-resolve all ambiguous')}
                            </Button>
                            <Button
                                size="small"
                                variant="outlined"
                                disabled={autoResolving || aiResolving || aiSearching || importing || (stats.ambiguous ?? 0) === 0}
                                onClick={onAIResolve}
                            >
                                {aiResolving
                                    ? t('curatedCollections.aiResolving', 'AI…')
                                    : t('curatedCollections.aiResolveAll', 'Resolve via AI')}
                            </Button>
                        </Stack>
                    )}

                    {tabKey === 'not_found' && (
                        <Stack direction="row" spacing={1} mt={2}>
                            <Button
                                size="small"
                                variant="outlined"
                                disabled={autoResolving || aiResolving || aiSearching || importing || (stats.not_found ?? 0) === 0}
                                onClick={onAISearchNotFound}
                            >
                                {aiSearching
                                    ? t('curatedCollections.aiSearching', 'AI searching…')
                                    : t('curatedCollections.aiSearchNotFound', 'Find via AI')}
                            </Button>
                        </Stack>
                    )}

                    <Box mt={2}>
                        <ItemsTable
                            items={items}
                            statusKey={tabKey}
                            bookInfo={bookInfo}
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
