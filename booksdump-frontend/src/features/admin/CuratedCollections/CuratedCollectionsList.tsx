import React, { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link as RouterLink, useParams } from 'react-router';
import { AlertCircle, Trash2 } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Badge } from '@/shared/ui/badge';
import { Button } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/ui/table';

import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import BookPagination from '@/features/catalogue/BookPagination';
import {
    CuratedCollection,
    deleteCuratedCollection,
    listCuratedCollections,
} from '@/features/admin/CuratedCollections/api';
import ImportForm from '@/features/admin/CuratedCollections/ImportForm';
import BatchImportForm from '@/features/admin/CuratedCollections/BatchImportForm';

const PAGE_SIZE = 25;

const CuratedCollectionsList: React.FC = () => {
    const { t } = useTranslation();
    const { page: pageParam } = useParams<{ page?: string }>();
    const page = Math.max(1, parseInt(pageParam ?? '1', 10) || 1);

    // Below this the seven columns stop fitting and each collection becomes a
    // card, the same width the other admin tables change at.
    const isMobile = useMediaQuery('(max-width: 899px)');

    const [rows, setRows] = useState<CuratedCollection[]>([]);
    const [total, setTotal] = useState(0);
    const [loadError, setLoadError] = useState<string | null>(null);

    const refresh = useCallback(async () => {
        try {
            const data = await listCuratedCollections(page, PAGE_SIZE);
            setRows(data.rows);
            setTotal(data.total);
        } catch (err) {
            setLoadError(err instanceof Error ? err.message : 'failed');
        }
    }, [page]);

    useEffect(() => {
        refresh();
    }, [refresh]);

    const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

    const onDelete = async (id: number) => {
        if (!window.confirm(t('curatedCollections.confirmDelete', 'Delete this collection?')))
            return;
        await deleteCuratedCollection(id);
        await refresh();
    };

    return (
        <div className="flex min-h-[calc(100vh-240px)] flex-col gap-4">
            <h1 className="text-xl font-medium">
                {t('curatedCollections.title', 'Curated collections')}
            </h1>

            <ImportForm onCreated={() => refresh()} />
            <BatchImportForm onCreated={() => refresh()} />

            {loadError && (
                <Alert variant="destructive">
                    <AlertCircle className="size-4" />
                    <AlertDescription>{loadError}</AlertDescription>
                </Alert>
            )}

            <Card className="flex flex-1 flex-col">
                <CardContent className="flex flex-1 flex-col">
                    {isMobile ? (
                        /* Seven columns, three of them counts, do not fit a
                           phone. Each collection becomes a card with the counts
                           on one row, where they still read against each
                           other. */
                        <div className="flex flex-col gap-3">
                            {rows.length === 0 ? (
                                <p className="py-6 text-center text-muted-foreground">
                                    {t('curatedCollections.empty', 'No curated collections yet')}
                                </p>
                            ) : (
                                rows.map((row) => (
                                    <div
                                        key={row.id}
                                        className="flex flex-col gap-3 rounded border border-border p-3"
                                    >
                                        <div className="flex min-w-0 items-start justify-between gap-2">
                                            <div className="flex min-w-0 flex-col gap-1">
                                                <RouterLink
                                                    to={`/admin/collections/${row.id}`}
                                                    className="truncate border-b border-border font-medium hover:border-current"
                                                >
                                                    {row.name}
                                                </RouterLink>
                                                <span className="text-xs text-muted-foreground">
                                                    {row.import_status ?? '—'}
                                                </span>
                                            </div>
                                            <div className="flex shrink-0 items-center gap-1">
                                                {row.is_public ? (
                                                    <Badge>
                                                        {t('curatedCollections.public', 'Public')}
                                                    </Badge>
                                                ) : (
                                                    <Badge variant="secondary">
                                                        {t('curatedCollections.draft', 'Draft')}
                                                    </Badge>
                                                )}
                                                <Button
                                                    variant="ghost"
                                                    size="icon-sm"
                                                    onClick={() => onDelete(row.id)}
                                                    title={t('curatedCollections.delete', 'Delete')}
                                                    aria-label={`${t('curatedCollections.delete', 'Delete')}: ${row.name}`}
                                                >
                                                    <Trash2 className="size-4" />
                                                </Button>
                                            </div>
                                        </div>

                                        <div className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
                                            {(
                                                [
                                                    [
                                                        'curatedCollections.matched',
                                                        'Matched',
                                                        row.import_stats?.matched,
                                                    ],
                                                    [
                                                        'curatedCollections.ambiguous',
                                                        'Ambiguous',
                                                        row.import_stats?.ambiguous,
                                                    ],
                                                    [
                                                        'curatedCollections.notFound',
                                                        'Not found',
                                                        row.import_stats?.not_found,
                                                    ],
                                                ] as const
                                            ).map(([key, fallback, value]) => (
                                                <span
                                                    key={key}
                                                    className="flex items-baseline gap-1"
                                                >
                                                    <span className="text-xs text-muted-foreground">
                                                        {t(key, fallback)}
                                                    </span>
                                                    <span className="tabular-nums">
                                                        {value ?? 0}
                                                    </span>
                                                </span>
                                            ))}
                                        </div>
                                    </div>
                                ))
                            )}
                        </div>
                    ) : (
                        <Table>
                            <TableHeader>
                                <TableRow>
                                    <TableHead>{t('curatedCollections.name', 'Name')}</TableHead>
                                    <TableHead>
                                        {t('curatedCollections.status', 'Status')}
                                    </TableHead>
                                    {/* Counts read down a column, so they are right
                                        aligned and set in figures of one width. */}
                                    <TableHead className="text-right">
                                        {t('curatedCollections.matched', 'Matched')}
                                    </TableHead>
                                    <TableHead className="text-right">
                                        {t('curatedCollections.ambiguous', 'Ambiguous')}
                                    </TableHead>
                                    <TableHead className="text-right">
                                        {t('curatedCollections.notFound', 'Not found')}
                                    </TableHead>
                                    <TableHead>
                                        {t('curatedCollections.public', 'Public')}
                                    </TableHead>
                                    {/* The column of delete buttons still needs a
                                        name, or a screen reader announces the cell
                                        without saying which column it is in. */}
                                    <TableHead>
                                        <span className="sr-only">
                                            {t('curatedCollections.delete', 'Delete')}
                                        </span>
                                    </TableHead>
                                </TableRow>
                            </TableHeader>
                            <TableBody>
                                {rows.map((row) => (
                                    <TableRow key={row.id}>
                                        <TableCell>
                                            <RouterLink
                                                to={`/admin/collections/${row.id}`}
                                                className="border-b border-border hover:border-current"
                                            >
                                                {row.name}
                                            </RouterLink>
                                        </TableCell>
                                        <TableCell className="text-muted-foreground">
                                            {row.import_status ?? '—'}
                                        </TableCell>
                                        <TableCell className="text-right tabular-nums">
                                            {row.import_stats?.matched ?? 0}
                                        </TableCell>
                                        <TableCell className="text-right tabular-nums">
                                            {row.import_stats?.ambiguous ?? 0}
                                        </TableCell>
                                        <TableCell className="text-right tabular-nums">
                                            {row.import_stats?.not_found ?? 0}
                                        </TableCell>
                                        <TableCell>
                                            {row.is_public ? (
                                                <Badge>
                                                    {t('curatedCollections.public', 'Public')}
                                                </Badge>
                                            ) : (
                                                <Badge variant="secondary">
                                                    {t('curatedCollections.draft', 'Draft')}
                                                </Badge>
                                            )}
                                        </TableCell>
                                        <TableCell>
                                            <Button
                                                variant="ghost"
                                                size="icon-sm"
                                                onClick={() => onDelete(row.id)}
                                                title={t('curatedCollections.delete', 'Delete')}
                                                aria-label={`${t('curatedCollections.delete', 'Delete')}: ${row.name}`}
                                            >
                                                <Trash2 className="size-4" />
                                            </Button>
                                        </TableCell>
                                    </TableRow>
                                ))}
                                {rows.length === 0 && (
                                    <TableRow>
                                        <TableCell
                                            colSpan={7}
                                            className="py-6 text-center text-muted-foreground"
                                        >
                                            {t(
                                                'curatedCollections.empty',
                                                'No curated collections yet',
                                            )}
                                        </TableCell>
                                    </TableRow>
                                )}
                            </TableBody>
                        </Table>
                    )}

                    {totalPages > 1 && (
                        <div className="mt-auto flex justify-center pt-4">
                            <BookPagination
                                totalPages={totalPages}
                                currentPage={page}
                                baseUrl="/admin/collections/page"
                            />
                        </div>
                    )}
                </CardContent>
            </Card>
        </div>
    );
};

export default CuratedCollectionsList;
