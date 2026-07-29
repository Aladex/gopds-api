import React, { useCallback, useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { AlertCircle, Search, Trash2 } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Badge } from '@/shared/ui/badge';
import { Button } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import { Input } from '@/shared/ui/input';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { Progress } from '@/shared/ui/progress';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/ui/tabs';

import SearchAndResolveDialog from '@/features/admin/CuratedCollections/SearchAndResolveDialog';
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
} from '@/features/admin/CuratedCollections/api';

const POLLING_INTERVAL_MS = 2500;

interface CandidateInfo {
    book_id: number;
    score: number;
}

// readCandidates safely extracts the candidate list saved by the backend in
// external_extra.candidates during ambiguous matching.
function readCandidates(extra: unknown): CandidateInfo[] {
    if (!extra || typeof extra !== 'object') return [];
    const candidates = (extra as { candidates?: unknown }).candidates;
    if (!Array.isArray(candidates)) return [];
    return candidates
        .filter(
            (c): c is { book_id: number; score?: unknown } =>
                !!c &&
                typeof c === 'object' &&
                typeof (c as { book_id?: unknown }).book_id === 'number',
        )
        .map((c) => ({ book_id: c.book_id, score: typeof c.score === 'number' ? c.score : 0 }));
}

/** describeBook is the "title — authors" line a chip carries, or a bare id. */
const describeBook = (bookID: number, book: LookupBook | undefined): string =>
    book
        ? `${book.title} — ${(book.authors ?? []).map((a) => a.full_name).join(', ') || '?'}`
        : `#${bookID}`;

const ItemsTable: React.FC<{
    items: CollectionItem[];
    bookInfo: Map<number, LookupBook>;
    onResolve: (itemID: number, bookID: number) => Promise<void>;
    onIgnore: (itemID: number) => Promise<void>;
}> = ({ items, bookInfo, onResolve, onIgnore }) => {
    const { t } = useTranslation();
    const [manualID, setManualID] = useState<Record<number, string>>({});
    const [busy, setBusy] = useState<Record<number, boolean>>({});
    const [searchFor, setSearchFor] = useState<{ itemID: number; query: string } | null>(null);

    // Below this the six columns stop fitting and each item becomes a card, the
    // same width the other admin tables change at.
    const isMobile = useMediaQuery('(max-width: 899px)');

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

    // The candidates cell and the row of actions are the whole of an item's
    // interface, and both layouts need them intact. Written once here rather
    // than twice below, where the two copies would drift.
    const Candidates: React.FC<{ item: CollectionItem }> = ({ item }) => {
        const candidates = readCandidates(item.external_extra);
        if (item.book_id) {
            return (
                <Badge className="h-auto whitespace-normal text-left">
                    #{item.book_id} · {describeBook(item.book_id, bookInfo.get(item.book_id))}
                </Badge>
            );
        }
        if (candidates.length === 0) {
            return (
                <span className="text-xs text-muted-foreground">
                    {t('curatedCollections.noCandidates', 'no candidates')}
                </span>
            );
        }
        return (
            <div className="flex flex-col items-start gap-1">
                {/* A candidate is a button, not a chip that happens to react to
                    a click: pressing it resolves the item. */}
                {candidates.map((c) => (
                    <Button
                        key={c.book_id}
                        variant="outline"
                        size="sm"
                        disabled={!!busy[item.id]}
                        onClick={() => resolveTo(item.id, c.book_id)}
                        className="h-auto max-w-full justify-start py-1 text-left whitespace-normal"
                    >
                        <span className="tabular-nums">{c.score.toFixed(2)}</span>
                        {' · '}
                        {describeBook(c.book_id, bookInfo.get(c.book_id))}
                    </Button>
                ))}
            </div>
        );
    };

    const Actions: React.FC<{ item: CollectionItem }> = ({ item }) => (
        <div className="flex flex-wrap items-center gap-1">
            <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => setSearchFor({ itemID: item.id, query: item.external_title })}
                title={t('curatedCollections.searchInLibrary', 'Search by title in library')}
                aria-label={`${t('curatedCollections.searchInLibrary', 'Search by title in library')}: ${item.external_title}`}
            >
                <Search className="size-4" />
            </Button>
            <Input
                inputMode="numeric"
                className="w-24"
                placeholder={t('curatedCollections.bookIdPlaceholder', 'book_id')}
                aria-label={`${t('curatedCollections.bookIdPlaceholder', 'book_id')}: ${item.external_title}`}
                value={manualID[item.id] ?? ''}
                onChange={(event) => setManualID((p) => ({ ...p, [item.id]: event.target.value }))}
                onKeyDown={(event) => {
                    if (event.key === 'Enter') submitManual(item.id);
                }}
            />
            <Button
                variant="outline"
                size="sm"
                disabled={!!busy[item.id]}
                onClick={() => submitManual(item.id)}
            >
                {t('curatedCollections.resolve', 'Resolve')}
            </Button>
            <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => ignore(item.id)}
                disabled={!!busy[item.id]}
                title={t('curatedCollections.ignoreAction', 'Ignore')}
                aria-label={`${t('curatedCollections.ignoreAction', 'Ignore')}: ${item.external_title}`}
            >
                <Trash2 className="size-4" />
            </Button>
        </div>
    );

    const empty = (
        <p className="py-6 text-center text-muted-foreground">
            {t('curatedCollections.tabEmpty', 'Empty')}
        </p>
    );

    return (
        <>
            {isMobile ? (
                /* Six columns, one of them a stack of candidate buttons and
                   another a whole form, do not fit a phone. Each item becomes a
                   card carrying the same controls. */
                <div className="flex flex-col gap-3">
                    {items.length === 0
                        ? empty
                        : items.map((it) => (
                              <div
                                  key={it.id}
                                  className="flex flex-col gap-3 rounded border border-border p-3"
                              >
                                  <div className="flex min-w-0 items-baseline gap-2">
                                      <span className="tabular-nums text-muted-foreground">
                                          {it.position + 1}
                                      </span>
                                      <div className="flex min-w-0 flex-col">
                                          <span className="font-medium">{it.external_title}</span>
                                          <span className="text-sm text-muted-foreground">
                                              {it.external_author}
                                          </span>
                                      </div>
                                  </div>

                                  <div className="flex items-baseline gap-1 text-sm">
                                      <span className="text-xs text-muted-foreground">
                                          {t('curatedCollections.col.score', 'Score')}
                                      </span>
                                      <span className="tabular-nums">
                                          {it.match_score?.toFixed?.(2) ?? '—'}
                                      </span>
                                  </div>

                                  <div className="flex flex-col gap-1">
                                      <span className="text-xs text-muted-foreground">
                                          {t(
                                              'curatedCollections.col.candidates',
                                              'Candidates / Book',
                                          )}
                                      </span>
                                      <Candidates item={it} />
                                  </div>

                                  <Actions item={it} />
                              </div>
                          ))}
                </div>
            ) : (
                <div className="rounded border border-border">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead className="text-right">#</TableHead>
                                <TableHead>{t('curatedCollections.col.title', 'Title')}</TableHead>
                                <TableHead>
                                    {t('curatedCollections.col.author', 'Author')}
                                </TableHead>
                                <TableHead className="text-right">
                                    {t('curatedCollections.col.score', 'Score')}
                                </TableHead>
                                <TableHead>
                                    {t('curatedCollections.col.candidates', 'Candidates / Book')}
                                </TableHead>
                                {/* The actions column still needs a name, or a
                                    screen reader announces the cell without
                                    saying which column it is in. */}
                                <TableHead>
                                    <span className="sr-only">
                                        {t('curatedCollections.col.actions', 'Actions')}
                                    </span>
                                </TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {items.map((it) => (
                                <TableRow key={it.id}>
                                    <TableCell className="text-right tabular-nums text-muted-foreground">
                                        {it.position + 1}
                                    </TableCell>
                                    <TableCell>{it.external_title}</TableCell>
                                    <TableCell>{it.external_author}</TableCell>
                                    <TableCell className="text-right tabular-nums">
                                        {it.match_score?.toFixed?.(2) ?? '—'}
                                    </TableCell>
                                    <TableCell className="min-w-90 max-w-130">
                                        <Candidates item={it} />
                                    </TableCell>
                                    <TableCell>
                                        <Actions item={it} />
                                    </TableCell>
                                </TableRow>
                            ))}
                            {items.length === 0 && (
                                <TableRow>
                                    <TableCell
                                        colSpan={6}
                                        className="py-6 text-center text-muted-foreground"
                                    >
                                        {t('curatedCollections.tabEmpty', 'Empty')}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </div>
            )}

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
        } catch (err) {
            setLoadErr(err instanceof Error ? err.message : 'load failed');
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
            } catch (err) {
                setLoadErr(err instanceof Error ? err.message : 'load items failed');
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
        if (!window.confirm(t('curatedCollections.confirmDelete', 'Delete this collection?')))
            return;
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
            // A toast rather than window.alert: the alert froze the tab until
            // it was dismissed, and the count is not worth a modal.
            toast.success(
                t('curatedCollections.autoResolveDone', '{{count}} items resolved', {
                    count: resolved,
                }),
            );
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
                setTimeout(tick, POLLING_INTERVAL_MS);
            } else {
                loadCollection();
                loadItems(tabKey);
            }
        };
        const timer = setTimeout(tick, POLLING_INTERVAL_MS);
        return () => {
            cancelled = true;
            clearTimeout(timer);
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [status?.stats?.ai_progress?.running, id]);

    if (!id || loadErr) {
        return (
            <Alert variant="destructive">
                <AlertCircle className="size-4" />
                <AlertDescription>
                    {loadErr ?? t('curatedCollections.invalidId', 'Invalid collection id')}
                </AlertDescription>
            </Alert>
        );
    }
    if (!coll) {
        return (
            <div role="status" className="flex flex-col gap-2 py-6">
                <p className="text-sm text-muted-foreground">{t('loading')}</p>
                <Progress aria-label={t('loading')} />
            </div>
        );
    }

    // The status endpoint knows about progress, the collection row only about
    // the three outcome counts; one type covers both because every field on it
    // is optional.
    const stats: ImportStatusInfo['stats'] = status?.stats ?? coll.import_stats ?? {};
    const importing = (status?.status ?? coll.import_status) === 'importing';
    const processed = stats.processed ?? 0;
    const total = stats.total ?? 0;
    const progressPct = total > 0 ? Math.min(100, Math.round((processed / total) * 100)) : 0;
    const ambiguousCount = stats.ambiguous ?? 0;
    const notFoundCount = stats.not_found ?? 0;
    const aiProgress = status?.stats?.ai_progress;
    const anyJobRunning = autoResolving || aiResolving || aiSearching || importing;

    const statusTabs = [
        { key: 'matched', label: t('curatedCollections.tab.autoMatched', 'Matched') },
        { key: 'ambiguous', label: t('curatedCollections.tab.ambiguous', 'Ambiguous') },
        { key: 'not_found', label: t('curatedCollections.tab.notFound', 'Not found') },
        { key: 'ignored', label: t('curatedCollections.tab.ignored', 'Ignored') },
    ];

    // The panel is identical under every tab — only the fetched items differ —
    // but it lives inside a TabsContent so each tab is properly labelled as the
    // panel's owner.
    const panel = (
        <div className="flex flex-col gap-4">
            {tabKey === 'ambiguous' && (
                <div className="flex flex-wrap gap-2">
                    <Button
                        variant="outline"
                        size="sm"
                        disabled={anyJobRunning || ambiguousCount === 0}
                        onClick={onAutoResolve}
                    >
                        {autoResolving
                            ? t('curatedCollections.autoResolving', 'Resolving…')
                            : t('curatedCollections.autoResolveAll', 'Auto-resolve all ambiguous')}
                    </Button>
                    <Button
                        variant="outline"
                        size="sm"
                        disabled={anyJobRunning || ambiguousCount === 0}
                        onClick={onAIResolve}
                    >
                        {aiResolving
                            ? t('curatedCollections.aiResolving', 'AI…')
                            : t('curatedCollections.aiResolveAll', 'Resolve via AI')}
                    </Button>
                </div>
            )}

            {tabKey === 'not_found' && (
                <div className="flex flex-wrap gap-2">
                    <Button
                        variant="outline"
                        size="sm"
                        disabled={anyJobRunning || notFoundCount === 0}
                        onClick={onAISearchNotFound}
                    >
                        {aiSearching
                            ? t('curatedCollections.aiSearching', 'AI searching…')
                            : t('curatedCollections.aiSearchNotFound', 'Find via AI')}
                    </Button>
                </div>
            )}

            <ItemsTable
                items={items}
                bookInfo={bookInfo}
                onResolve={onResolve}
                onIgnore={onIgnore}
            />
        </div>
    );

    return (
        <div className="flex flex-col gap-4">
            <div className="flex flex-wrap items-center gap-2">
                <h1 className="text-xl font-medium">{coll.name}</h1>
                {coll.is_public ? (
                    <Badge>{t('curatedCollections.public', 'Public')}</Badge>
                ) : (
                    <Badge variant="secondary">{t('curatedCollections.draft', 'Draft')}</Badge>
                )}
                {importing && (
                    <Badge
                        variant="outline"
                        className="border-amber-500/60 text-amber-600 dark:text-amber-400"
                    >
                        {t('curatedCollections.importingChip', 'importing…')}
                    </Badge>
                )}
            </div>

            <Card>
                <CardContent className="flex flex-col gap-3">
                    {importing && total > 0 && (
                        <div className="flex flex-col gap-1">
                            <p className="text-sm text-muted-foreground tabular-nums">
                                {t('curatedCollections.progress', 'Progress')}: {processed} /{' '}
                                {total} ({progressPct}%)
                            </p>
                            <Progress
                                value={progressPct}
                                aria-label={t('curatedCollections.progress', 'Progress')}
                            />
                        </div>
                    )}

                    <div className="flex flex-wrap gap-4 text-sm tabular-nums">
                        <span>
                            {t('curatedCollections.matched', 'Matched')}: {stats.matched ?? 0}
                        </span>
                        <span>
                            {t('curatedCollections.ambiguous', 'Ambiguous')}: {ambiguousCount}
                        </span>
                        <span>
                            {t('curatedCollections.notFound', 'Not found')}: {notFoundCount}
                        </span>
                    </div>

                    {status?.import_error && (
                        <Alert variant="destructive">
                            <AlertCircle className="size-4" />
                            <AlertDescription>{status.import_error}</AlertDescription>
                        </Alert>
                    )}

                    {aiProgress && (
                        <div className="flex flex-col gap-2 rounded-md border border-border p-3">
                            <div className="flex flex-wrap items-center gap-3 text-sm">
                                <span>
                                    {aiProgress.running
                                        ? t('curatedCollections.aiBoxRunning', 'AI resolving…')
                                        : t('curatedCollections.aiBoxIdle', 'AI last run')}
                                </span>
                                <span className="text-muted-foreground tabular-nums">
                                    {aiProgress.processed} / {aiProgress.total}
                                    {' · '}
                                    {t('curatedCollections.aiBoxResolved', 'resolved')}:{' '}
                                    {aiProgress.resolved}
                                </span>
                            </div>

                            {aiProgress.total > 0 && (
                                <Progress
                                    value={Math.min(
                                        100,
                                        Math.round((aiProgress.processed / aiProgress.total) * 100),
                                    )}
                                    aria-label={t(
                                        'curatedCollections.aiBoxRunning',
                                        'AI resolving…',
                                    )}
                                />
                            )}

                            {aiProgress.recent && aiProgress.recent.length > 0 && (
                                // Newest first. p-0 and list-none because
                                // Tailwind runs without preflight, so a bare
                                // list keeps the browser's markers and indent.
                                <ul className="m-0 list-none p-0 font-mono text-xs text-muted-foreground">
                                    {aiProgress.recent
                                        .slice()
                                        .reverse()
                                        .map((d, i) => (
                                            <li key={i} className="py-0.5">
                                                {d.action === 'resolved' ? '✓ ' : '· '}
                                                <strong className="font-semibold text-foreground">
                                                    {d.external_title}
                                                </strong>
                                                {d.action === 'resolved' && d.book_title
                                                    ? ` → #${d.book_id} ${d.book_title}`
                                                    : ` — ${t('curatedCollections.aiSkipped', 'skipped')}`}
                                            </li>
                                        ))}
                                </ul>
                            )}
                        </div>
                    )}

                    <div className="flex flex-wrap gap-2">
                        <Button size="sm" onClick={togglePublish} disabled={importing}>
                            {coll.is_public
                                ? t('curatedCollections.unpublish', 'Unpublish')
                                : t('curatedCollections.publish', 'Publish')}
                        </Button>
                        <Button
                            variant="outline"
                            size="sm"
                            className="text-destructive hover:text-destructive"
                            onClick={onDelete}
                        >
                            {t('curatedCollections.delete', 'Delete')}
                        </Button>
                    </div>
                </CardContent>
            </Card>

            <Card>
                <CardContent>
                    <Tabs value={tabKey} onValueChange={setTabKey}>
                        {/* Wrapping rather than scrolling sideways: four
                            labels do not fit a phone in one row, and a tab
                            that has slid off the edge is one nobody finds. */}
                        <TabsList className="h-auto w-full flex-wrap justify-start self-start sm:w-auto">
                            {statusTabs.map((tab) => (
                                <TabsTrigger key={tab.key} value={tab.key}>
                                    {tab.label}
                                </TabsTrigger>
                            ))}
                        </TabsList>

                        {statusTabs.map((tab) => (
                            <TabsContent key={tab.key} value={tab.key}>
                                {panel}
                            </TabsContent>
                        ))}
                    </Tabs>
                </CardContent>
            </Card>
        </div>
    );
};

export default CuratedCollectionDetail;
