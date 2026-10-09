import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import { Dialog, DialogContent } from '@/shared/ui/dialog';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/ui/table';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { ADMIN_TABLE_WIDE_QUERY } from '@/shared/layout/breakpoints';
import { formatDate } from '@/shared/lib/formatDate';
import * as adminApi from '@/api/admin';
import type { AuthorReviewListItem } from '@/api/admin';
import {
    AuthorReviewDetail,
    REVIEW_CURSOR_PARAM,
    REVIEW_DETAIL_ROUTE,
    REVIEW_HISTORY_PARAM,
    REVIEW_STATUS_PARAM,
    queueSearchParams,
    statusFromParams,
    useReviewErrorDescriber,
} from '@/features/admin/AuthorReviewDetail';

const PAGE_LIMIT = 50;

/**
 * The review queue. Wide screens open an item in a modal dialog over the
 * list; narrow screens navigate to the item's own route, where the address
 * carries the queue's filter and page so Back returns to the same view. The
 * list itself never moves: whatever opens, these rows stay in place.
 */

/** Only ids JSON parsing has not rounded may ever reach a route. */
const isUsableId = (id: number): boolean => Number.isSafeInteger(id) && id > 0;

/** Fallbacks for the dynamic status/scope keys; the locales carry the real strings. */
const STATUS_VALUE_FALLBACKS = { open: 'Open', closed: 'Closed' } as const;
const SCOPE_VALUE_FALLBACKS = { credit: 'Credit', fingerprint: 'Fingerprint' } as const;

const AuthorReviewQueue: React.FC = () => {
    const { t } = useTranslation();
    const isWide = useMediaQuery(ADMIN_TABLE_WIDE_QUERY);
    const navigate = useNavigate();
    const [searchParams, setSearchParams] = useSearchParams();

    /*
      The address is the single source of the queue's list state. It is read
      here — not copied into state — so every way the query can change answers
      alike: the queue's own buttons push entries, and a browser Back/Forward
      that changes only the query (the queue stays mounted, initializers would
      not rerun) restores the filter and page the address names.
    */
    const statusFilter = statusFromParams(searchParams);
    const cursor = searchParams.get(REVIEW_CURSOR_PARAM) ?? undefined;
    const cursorHistory = searchParams.getAll(REVIEW_HISTORY_PARAM);
    const [items, setItems] = useState<AuthorReviewListItem[]>([]);
    const [nextCursor, setNextCursor] = useState<string | null>(null);
    const [listPhase, setListPhase] = useState<'loading' | 'ready' | 'failed'>('loading');
    const [listErrorText, setListErrorText] = useState<string | null>(null);

    /** The item shown in the modal; null while no modal is open. */
    const [modalItemID, setModalItemID] = useState<number | null>(null);
    /** The exact control that opened the modal; it gets keyboard focus back. */
    const modalOpenerRef = useRef<HTMLElement | null>(null);
    /** The stable landing when the opener's row left the list meanwhile. */
    const queueRegionRef = useRef<HTMLDivElement | null>(null);

    const listGeneration = useRef(0);

    /**
     * Writes the queue's own state into the address (preserving any foreign
     * parameter, such as the screen's tab), so the browser's Back and the
     * detail screen's Back button both return to the same filter and page.
     */
    const syncQueueParams = useCallback(
        (status: 'open' | 'closed', pageCursor: string | undefined, history: string[]) => {
            const next = queueSearchParams(status, pageCursor, history);
            for (const [key, value] of searchParams.entries()) {
                if (
                    key !== REVIEW_STATUS_PARAM &&
                    key !== REVIEW_CURSOR_PARAM &&
                    key !== REVIEW_HISTORY_PARAM
                ) {
                    next.append(key, value);
                }
            }
            setSearchParams(next);
        },
        [searchParams, setSearchParams],
    );

    const describeReviewError = useReviewErrorDescriber();

    const fetchList = useCallback(
        async (status: 'open' | 'closed', pageCursor: string | undefined) => {
            const generation = ++listGeneration.current;
            setListPhase('loading');
            setListErrorText(null);
            try {
                const data = await adminApi.listAuthorReviewItems({
                    status,
                    cursor: pageCursor,
                    limit: PAGE_LIMIT,
                });
                if (generation !== listGeneration.current) {
                    return;
                }
                setItems(data.items);
                setNextCursor(data.next_cursor);
                setListPhase('ready');
            } catch (error) {
                if (generation !== listGeneration.current) {
                    return;
                }
                setListErrorText(describeReviewError(error));
                setListPhase('failed');
            }
        },
        [describeReviewError],
    );

    useEffect(() => {
        fetchList(statusFilter, cursor);
    }, [statusFilter, cursor, fetchList]);

    const changeStatus = (status: 'open' | 'closed') => {
        syncQueueParams(status, undefined, []);
    };

    const goNextPage = () => {
        if (nextCursor === null) {
            return;
        }
        syncQueueParams(statusFilter, nextCursor, [...cursorHistory, cursor ?? '']);
    };

    const goPreviousPage = () => {
        if (cursorHistory.length === 0) {
            return;
        }
        const history = cursorHistory.slice(0, -1);
        const target = cursorHistory[cursorHistory.length - 1];
        syncQueueParams(statusFilter, target === '' ? undefined : target, history);
    };

    const openModal = (itemID: number, opener: HTMLElement) => {
        // The invoking control itself, not document.activeElement: a pointer
        // activation does not necessarily move keyboard focus to the button it
        // clicks, and focus may still sit on an earlier control.
        modalOpenerRef.current = opener;
        setModalItemID(itemID);
    };

    const closeModal = () => {
        setModalItemID(null);
    };

    // Focus goes back to the row's View button after the modal closes — not
    // inside the close handler, which Radix's unmount machinery overwrites.
    // If the opener's row left the list while the modal was open, the queue
    // region is the stable place to land.
    useEffect(() => {
        if (modalItemID !== null) {
            return;
        }
        const opener = modalOpenerRef.current;
        if (opener === null) {
            return;
        }
        modalOpenerRef.current = null;
        if (document.contains(opener)) {
            opener.focus();
            return;
        }
        queueRegionRef.current?.focus();
    }, [modalItemID]);

    const selectItem = (itemID: number, opener: HTMLElement) => {
        if (!isUsableId(itemID)) {
            return;
        }
        if (isWide) {
            openModal(itemID, opener);
            return;
        }
        // Narrow: the detail is a screen of its own, and the queue's filter
        // and page travel in the address so Back returns to this view.
        const params = queueSearchParams(statusFilter, cursor, cursorHistory);
        const query = params.toString();
        navigate(`${REVIEW_DETAIL_ROUTE}/${itemID}${query === '' ? '' : `?${query}`}`);
    };

    const viewLabel = (name: string) => `${t('authorReview.view', 'View')}: ${name}`;

    const rowActions = (item: AuthorReviewListItem) => (
        <Button
            variant="outline"
            size="sm"
            disabled={!isUsableId(item.id)}
            aria-label={viewLabel(item.display_name)}
            onClick={(event) => selectItem(item.id, event.currentTarget)}
        >
            {t('authorReview.view', 'View')}
        </Button>
    );

    return (
        <div
            ref={queueRegionRef}
            role="region"
            aria-label={t('authorReview.title', 'Review queue')}
            tabIndex={-1}
            className="flex flex-col gap-4 outline-none"
        >
            <Card>
                <CardContent className="flex flex-col gap-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                        <h3 className="text-base font-medium">
                            {t('authorReview.title', 'Review queue')}
                        </h3>
                        <div
                            role="group"
                            aria-label={t('authorReview.status', 'Status')}
                            className="flex gap-2"
                        >
                            {(['open', 'closed'] as const).map((status) => (
                                <Button
                                    key={status}
                                    variant={statusFilter === status ? 'default' : 'outline'}
                                    size="sm"
                                    aria-pressed={statusFilter === status}
                                    onClick={() => changeStatus(status)}
                                >
                                    {t(
                                        `authorReview.statusValue.${status}`,
                                        STATUS_VALUE_FALLBACKS[status],
                                    )}
                                </Button>
                            ))}
                        </div>
                    </div>

                    {listPhase === 'loading' && (
                        <p className="text-sm text-muted-foreground">
                            {t('loading', 'Loading...')}
                        </p>
                    )}
                    {listPhase === 'ready' && items.length === 0 && (
                        <p className="text-sm text-muted-foreground">
                            {t('authorReview.empty', 'No review items.')}
                        </p>
                    )}
                    {listErrorText && (
                        <Alert variant="destructive">
                            <AlertDescription>{listErrorText}</AlertDescription>
                        </Alert>
                    )}

                    {listPhase === 'ready' && items.length > 0 && isWide && (
                        <div className="rounded border border-border">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>
                                            {t('authorReview.col.displayName', 'Display name')}
                                        </TableHead>
                                        <TableHead>
                                            {t('authorReview.col.reason', 'Reason')}
                                        </TableHead>
                                        <TableHead>
                                            {t('authorReview.col.class', 'Class')}
                                        </TableHead>
                                        <TableHead>
                                            {t('authorReview.col.scope', 'Scope')}
                                        </TableHead>
                                        <TableHead className="text-right">
                                            {t('authorReview.col.credits', 'Credits')}
                                        </TableHead>
                                        <TableHead>
                                            {t('authorReview.col.created', 'Created')}
                                        </TableHead>
                                        <TableHead>
                                            <span className="sr-only">
                                                {t('actions', 'Actions')}
                                            </span>
                                        </TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {items.map((item) => (
                                        <TableRow key={item.id}>
                                            <TableCell className="font-medium">
                                                {item.display_name}
                                            </TableCell>
                                            <TableCell>{item.reason}</TableCell>
                                            <TableCell>{item.decision_class}</TableCell>
                                            <TableCell>
                                                {t(
                                                    `authorReview.scopeValue.${item.scope}`,
                                                    SCOPE_VALUE_FALLBACKS[item.scope],
                                                )}
                                            </TableCell>
                                            <TableCell className="text-right tabular-nums">
                                                {item.credits_count}
                                            </TableCell>
                                            <TableCell className="whitespace-nowrap">
                                                {formatDate(item.created_at)}
                                            </TableCell>
                                            <TableCell>{rowActions(item)}</TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </div>
                    )}

                    {listPhase === 'ready' && items.length > 0 && !isWide && (
                        <div
                            role="list"
                            aria-label={t('authorReview.listLabel', 'Review items')}
                            className="flex flex-col gap-3"
                        >
                            {items.map((item) => (
                                <div
                                    key={item.id}
                                    role="listitem"
                                    aria-label={item.display_name}
                                    className="flex flex-col gap-2 rounded-lg border border-border p-3"
                                >
                                    <p className="text-sm font-semibold break-words">
                                        {item.display_name}
                                    </p>
                                    <div className="flex flex-col gap-0.5 text-xs text-muted-foreground">
                                        <span>
                                            {t('authorReview.col.reason', 'Reason')}: {item.reason}
                                        </span>
                                        <span>
                                            {t('authorReview.col.scope', 'Scope')}:{' '}
                                            {t(
                                                `authorReview.scopeValue.${item.scope}`,
                                                SCOPE_VALUE_FALLBACKS[item.scope],
                                            )}
                                        </span>
                                        <span className="tabular-nums">
                                            {t('authorReview.col.credits', 'Credits')}:{' '}
                                            {item.credits_count}
                                        </span>
                                    </div>
                                    {rowActions(item)}
                                </div>
                            ))}
                        </div>
                    )}

                    <div className="flex justify-between gap-2">
                        <Button
                            variant="outline"
                            size="sm"
                            disabled={cursorHistory.length === 0 || listPhase === 'loading'}
                            onClick={goPreviousPage}
                        >
                            {t('authorReview.previousPage', 'Previous page')}
                        </Button>
                        <Button
                            variant="outline"
                            size="sm"
                            disabled={nextCursor === null || listPhase === 'loading'}
                            onClick={goNextPage}
                        >
                            {t('authorReview.nextPage', 'Next page')}
                        </Button>
                    </div>
                </CardContent>
            </Card>

            {/*
              The wide layout's detail: a modal over the list, so the rows
              stay exactly where they were. The content scrolls inside; Esc
              and the close button return focus to the row's View button.
            */}
            <Dialog open={modalItemID !== null} onOpenChange={(open) => !open && closeModal()}>
                <DialogContent
                    closeLabel={t('close', 'Close')}
                    aria-labelledby="author-review-detail-heading"
                    aria-describedby={undefined}
                    className="max-h-[85vh] overflow-y-auto sm:max-w-2xl"
                >
                    {modalItemID !== null && (
                        <AuthorReviewDetail key={modalItemID} itemID={modalItemID} />
                    )}
                </DialogContent>
            </Dialog>
        </div>
    );
};

export default AuthorReviewQueue;
