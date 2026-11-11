import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Dialog, DialogContent } from '@/shared/ui/dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/ui/select';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { ADMIN_TABLE_WIDE_QUERY } from '@/shared/layout/breakpoints';
import { cn } from '@/shared/lib/utils';
import * as adminApi from '@/api/admin';
import type { AuthorReviewListItem, AuthorReviewOrder } from '@/api/admin';
import {
    AuthorReviewDetail,
    KIND_FALLBACKS,
    REVIEW_CURSOR_PARAM,
    REVIEW_DETAIL_ROUTE,
    REVIEW_HISTORY_PARAM,
    REVIEW_ORDER_PARAM,
    REVIEW_STATUS_PARAM,
    ReviewConfirmDialog,
    ReviewCorrectionForm,
    ReviewEvidence,
    isUsableId,
    orderFromParams,
    queueSearchParams,
    statusFromParams,
    useReviewErrorDescriber,
    useReviewItem,
    type ReviewItem,
    type ScopedActionBody,
} from '@/features/admin/AuthorReviewDetail';
import { formatCount } from '@/features/admin/scan/scanUi';

const PAGE_LIMIT = 50;

/**
 * The author review queue as a triage list: every row is the name as the file
 * writes it, the proposal, how many books carry it and the three decisions.
 * The focused row (the first one on load) shows its evidence under it; J/K
 * move the focus, 1/2/3 decide, Enter opens the item's card — a modal over the
 * list on wide screens, the item's own route on narrow ones, where the
 * address carries the queue's filter and order so Back returns to the same
 * view. The list never moves: a decided row greys out in place.
 */

/**
 * The tab decides at fingerprint scope only, so it lists only fingerprint
 * items. A credit-scoped item comes only from a legacy credit override
 * flagged after a schema change; it stays reachable through the admin API.
 */
const isShownInTab = (item: AuthorReviewListItem): boolean => item.scope === 'fingerprint';

/**
 * How many further pages one load reads past pages that hold only hidden
 * items, so such a page never shows an empty list with more to come.
 */
const MAX_HIDDEN_PAGES = 20;

/** How long the focus must rest on a row before its detail is read. */
const REST_MS = 150;

/** Fallbacks for the dynamic status/order keys; the locales carry the real strings. */
const STATUS_VALUE_FALLBACKS = { open: 'Open', closed: 'Closed' } as const;
const ORDER_FALLBACKS: Record<AuthorReviewOrder, string> = {
    credits: 'Most books first',
    created: 'Oldest first',
};

/** A row's decision: the three a person can take from the queue. */
type Decision = 'accept' | 'edit' | 'unresolved';

const DECISIONS: ReadonlyArray<{ decision: Decision; label: string; fallback: string }> = [
    { decision: 'accept', label: 'authorReview.accept', fallback: 'Accept' },
    { decision: 'edit', label: 'authorReview.correct', fallback: 'Correct' },
    { decision: 'unresolved', label: 'authorReview.keepAsInFile', fallback: 'Keep as in the file' },
];

/** How a decided row reads once the server confirmed it. */
const VERDICT_FALLBACKS: Record<ScopedActionBody['type'], string> = {
    accept: 'accepted',
    edit: 'corrected',
    classify: 'kind set',
    unresolved: 'kept as in the file',
};

/** The keys, by physical position: a Russian layout types «о», the key is still J. */
const KEY_ACTIONS: Readonly<Record<string, 'next' | 'previous' | Decision | 'open'>> = {
    KeyJ: 'next',
    KeyK: 'previous',
    Digit1: 'accept',
    Numpad1: 'accept',
    Digit2: 'edit',
    Numpad2: 'edit',
    Digit3: 'unresolved',
    Numpad3: 'unresolved',
    Enter: 'open',
    NumpadEnter: 'open',
};

/**
 * Keys typed here belong to the field or the overlay, never to the queue. A
 * dialog that is closing (Radix keeps it on the page while it fades out, with
 * the focus still trapped in it) no longer counts, so the next key after a
 * confirmation is not swallowed.
 */
const TYPING_TARGETS =
    'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="combobox"], [role="dialog"]:not([data-state="closed"]), [role="alertdialog"]:not([data-state="closed"]), [role="listbox"], [role="menu"]';

/** A Radix dialog that is fading out, focus still trapped inside it. */
const CLOSING_DIALOG =
    '[role="dialog"][data-state="closed"], [role="alertdialog"][data-state="closed"]';

/** Enter on these already does something of its own. */
const ACTIVATABLE = 'button, a[href], summary, [role="button"], [role="combobox"], [role="tab"]';

/**
 * The queue's list state in the address, which is its single source: the
 * queue's own controls push entries, and a browser Back/Forward that changes
 * only the query restores the filter and order the address names. Writes keep
 * every foreign parameter, such as the scan tab's stage.
 */
export function useReviewQueueAddress() {
    const [searchParams, setSearchParams] = useSearchParams();
    const status = statusFromParams(searchParams);
    const order = orderFromParams(searchParams);

    const write = useCallback(
        (nextStatus: 'open' | 'closed', nextOrder: AuthorReviewOrder) => {
            const next = queueSearchParams(nextStatus, undefined, [], nextOrder);
            for (const [key, value] of searchParams.entries()) {
                if (
                    key !== REVIEW_STATUS_PARAM &&
                    key !== REVIEW_CURSOR_PARAM &&
                    key !== REVIEW_HISTORY_PARAM &&
                    key !== REVIEW_ORDER_PARAM
                ) {
                    next.append(key, value);
                }
            }
            setSearchParams(next);
        },
        [searchParams, setSearchParams],
    );

    return {
        status,
        order,
        setStatus: (next: 'open' | 'closed') => write(next, order),
        setOrder: (next: AuthorReviewOrder) => write(status, next),
    };
}

/** The queue's sort and its open/closed pair; the panel puts them in its header. */
export const ReviewQueueTools: React.FC = () => {
    const { t } = useTranslation();
    const { status, order, setStatus, setOrder } = useReviewQueueAddress();
    return (
        <div className="flex flex-wrap items-center gap-2">
            <Select value={order} onValueChange={(value) => setOrder(value as AuthorReviewOrder)}>
                <SelectTrigger aria-label={t('authorReview.sort.label', 'Sort')}>
                    <SelectValue />
                </SelectTrigger>
                <SelectContent>
                    {(['credits', 'created'] as const).map((value) => (
                        <SelectItem key={value} value={value}>
                            {t(`authorReview.sort.${value}`, ORDER_FALLBACKS[value])}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
            <div
                role="group"
                aria-label={t('authorReview.status', 'Status')}
                className="inline-flex rounded-md bg-muted p-0.5"
            >
                {(['open', 'closed'] as const).map((value) => (
                    <Button
                        key={value}
                        variant="ghost"
                        aria-pressed={status === value}
                        onClick={() => setStatus(value)}
                        className={cn(
                            'h-10 px-3 sm:h-8',
                            status === value
                                ? 'bg-card shadow-xs hover:bg-card'
                                : 'text-muted-foreground',
                        )}
                    >
                        {t(`authorReview.statusValue.${value}`, STATUS_VALUE_FALLBACKS[value])}
                    </Button>
                ))}
            </div>
        </div>
    );
};

interface AuthorReviewQueueProps {
    /** The open count the metadata run reports; the list's pages do not carry it. */
    openCount?: number | null;
    /** False when the host renders `ReviewQueueTools` itself, as the panel header does. */
    tools?: boolean;
}

const AuthorReviewQueue: React.FC<AuthorReviewQueueProps> = ({
    openCount = null,
    tools = true,
}) => {
    const { t } = useTranslation();
    const isWide = useMediaQuery(ADMIN_TABLE_WIDE_QUERY);
    const navigate = useNavigate();
    const { status, order } = useReviewQueueAddress();
    const deciding = status === 'open';

    const [items, setItems] = useState<AuthorReviewListItem[]>([]);
    const [nextCursor, setNextCursor] = useState<string | null>(null);
    const [listPhase, setListPhase] = useState<'loading' | 'ready' | 'failed'>('loading');
    const [moreLoading, setMoreLoading] = useState(false);
    const [listErrorText, setListErrorText] = useState<string | null>(null);

    /** The focused row; its evidence is open under it. */
    const [focusedID, setFocusedID] = useState<number | null>(null);
    /** The row the focus rested on long enough for its detail to be read. */
    const [restingID, setRestingID] = useState<number | null>(null);
    /** Rows the server decided while this list was on screen. */
    const [decided, setDecided] = useState<ReadonlyMap<number, ScopedActionBody['type']>>(
        () => new Map(),
    );
    /** A decision asked for before the row's detail had arrived. */
    const queuedRef = useRef<{ itemID: number; decision: Decision } | null>(null);
    /** The inline correction form should take focus once it renders. */
    const [focusForm, setFocusForm] = useState(false);

    /** The item shown in the modal; null while no modal is open. */
    const [modalItemID, setModalItemID] = useState<number | null>(null);
    /** The exact control that opened the modal; it gets keyboard focus back. */
    const modalOpenerRef = useRef<HTMLElement | null>(null);
    /** The stable landing when the opener's row left the list meanwhile. */
    const queueRegionRef = useRef<HTMLDivElement | null>(null);
    const rowRefs = useRef(new Map<number, HTMLLIElement>());
    /** The focused row as last rendered, for callbacks that outlive a render. */
    const focusedIDRef = useRef<number | null>(null);
    /** The row a decision just moved the focus to; the keyboard focus should follow. */
    const followRef = useRef<number | null>(null);

    const listGeneration = useRef(0);
    const describeReviewError = useReviewErrorDescriber();

    /** Reads one page, then past pages that hold only hidden items. */
    const readPage = useCallback(
        async (pageCursor: string | undefined, generation: number) => {
            let data = await adminApi.listAuthorReviewItems({
                status,
                order,
                cursor: pageCursor,
                limit: PAGE_LIMIT,
            });
            let shown = data.items.filter(isShownInTab);
            for (
                let skipped = 0;
                shown.length === 0 && data.next_cursor !== null && skipped < MAX_HIDDEN_PAGES;
                skipped++
            ) {
                if (generation !== listGeneration.current) {
                    return null;
                }
                data = await adminApi.listAuthorReviewItems({
                    status,
                    order,
                    cursor: data.next_cursor,
                    limit: PAGE_LIMIT,
                });
                shown = data.items.filter(isShownInTab);
            }
            return { shown, next: data.next_cursor };
        },
        [status, order],
    );

    /** Reads the first page of the current filter and order; focus lands on its first row. */
    const loadFirstPage = useCallback(() => {
        const generation = ++listGeneration.current;
        setListPhase('loading');
        setListErrorText(null);
        setMoreLoading(false);
        readPage(undefined, generation)
            .then((page) => {
                if (page === null || generation !== listGeneration.current) {
                    return;
                }
                setItems(page.shown);
                setNextCursor(page.next);
                setDecided(new Map());
                queuedRef.current = null;
                setFocusedID(page.shown[0]?.id ?? null);
                setListPhase('ready');
            })
            .catch((error) => {
                if (generation !== listGeneration.current) {
                    return;
                }
                setListErrorText(describeReviewError(error));
                setListPhase('failed');
            });
    }, [readPage, describeReviewError]);

    useEffect(() => {
        loadFirstPage();
    }, [loadFirstPage]);

    const loadMore = () => {
        if (nextCursor === null || moreLoading) {
            return;
        }
        const generation = listGeneration.current;
        setMoreLoading(true);
        readPage(nextCursor, generation)
            .then((page) => {
                if (page === null || generation !== listGeneration.current) {
                    return;
                }
                setItems((rows) => {
                    const known = new Set(rows.map((row) => row.id));
                    return [...rows, ...page.shown.filter((row) => !known.has(row.id))];
                });
                setNextCursor(page.next);
            })
            .catch((error) => {
                if (generation !== listGeneration.current) {
                    return;
                }
                setListErrorText(describeReviewError(error));
            })
            .finally(() => {
                if (generation === listGeneration.current) {
                    setMoreLoading(false);
                }
            });
    };

    // The detail is read once the focus rests: holding J sends no request
    // per row passed over, only for the row it stops on.
    useEffect(() => {
        if (focusedID === null) {
            return;
        }
        const timer = window.setTimeout(() => setRestingID(focusedID), REST_MS);
        return () => window.clearTimeout(timer);
    }, [focusedID]);

    /** Marks a row decided and, if it is still focused, moves on to the next undecided one. */
    const onDecided = useCallback(
        (itemID: number, action: ScopedActionBody['type']) => {
            setDecided((current) => new Map(current).set(itemID, action));
            // Only a row still focused hands the focus on; one left behind
            // with J/K while its request ran stays where the person went.
            if (focusedIDRef.current !== itemID) {
                return;
            }
            const index = items.findIndex((row) => row.id === itemID);
            const next = items.find(
                (row, at) => at > index && row.id !== itemID && !decided.has(row.id),
            );
            const target = next?.id ?? itemID;
            followRef.current = target;
            setFocusedID(target);
        },
        [items, decided],
    );

    const item: ReviewItem = useReviewItem(restingID, { onDecided, restoreFocus: false });

    /**
     * Keyboard focus follows the focused row onto its name, so Enter and a
     * screen reader agree with what the row highlight says. It is only taken
     * from the page itself or from inside the queue, never from elsewhere.
     */
    const followRow = useCallback((itemID: number, force = false) => {
        const active = document.activeElement;
        const free =
            force ||
            active === null ||
            active === document.body ||
            (queueRegionRef.current?.contains(active) ?? false);
        if (!free) {
            return;
        }
        const name = rowRefs.current.get(itemID)?.querySelector<HTMLElement>('[data-open-item]');
        name?.focus({ preventScroll: true });
    }, []);

    /** True from the confirmation's opening until its content has left the page. */
    const confirmShownRef = useRef(false);
    useEffect(() => {
        focusedIDRef.current = focusedID;
        if (item.pendingAction !== null) {
            confirmShownRef.current = true;
        }
    });

    // After a decision the keyboard focus moves on with the row focus. While
    // the confirmation is still on the page (it fades out) it holds the focus
    // trapped, and hands it back as it leaves: that hand-over below does the
    // move then. A decision that settles after the dialog is gone moves here.
    useEffect(() => {
        const target = followRef.current;
        if (
            target === null ||
            item.busy ||
            item.pendingAction !== null ||
            confirmShownRef.current
        ) {
            return;
        }
        followRef.current = null;
        followRow(target);
    }, [focusedID, item.busy, item.pendingAction, followRow]);

    const onConfirmClosed = (event: Event) => {
        confirmShownRef.current = false;
        const target = followRef.current;
        if (target === null) {
            return;
        }
        event.preventDefault();
        followRef.current = null;
        followRow(target, true);
    };
    const focusedRow = items.find((row) => row.id === focusedID) ?? null;
    const focusedDetail = item.detail !== null && item.detail.id === focusedID ? item.detail : null;

    /** Runs a decision on a row whose detail is on screen. */
    const runDecision = useCallback(
        (decision: Decision) => {
            if (decision === 'edit') {
                item.setEditOpen(true);
                setFocusForm(true);
                return;
            }
            item.arm({ type: decision });
        },
        [item],
    );

    /** A decision for a row: now if its detail is here, else once it arrives. */
    const requestDecision = (row: AuthorReviewListItem, decision: Decision) => {
        if (!deciding || decided.has(row.id) || !isUsableId(row.id)) {
            return;
        }
        if (decision === 'accept' && row.proposal_kind === null) {
            return;
        }
        if (row.id === focusedID && focusedDetail !== null && item.phase === 'ready') {
            runDecision(decision);
            return;
        }
        setFocusedID(row.id);
        // The row is chosen, not passed over: read it now.
        setRestingID(row.id);
        queuedRef.current = { itemID: row.id, decision };
    };

    // A queued decision fires once its row's detail has arrived; it is
    // dropped when the focus moved on or the detail could not be read.
    useEffect(() => {
        const queued = queuedRef.current;
        if (queued === null) {
            return;
        }
        if (queued.itemID !== focusedID) {
            queuedRef.current = null;
            return;
        }
        if (item.phase === 'failed' && item.itemID === queued.itemID) {
            queuedRef.current = null;
            return;
        }
        if (focusedDetail === null || item.phase !== 'ready' || item.actionDisabled) {
            return;
        }
        queuedRef.current = null;
        runDecision(queued.decision);
    }, [focusedID, focusedDetail, item.phase, item.itemID, item.actionDisabled, runDecision]);

    const moveFocus = (delta: number) => {
        if (items.length === 0) {
            return;
        }
        const index = items.findIndex((row) => row.id === focusedID);
        const target = items[Math.max(0, Math.min(items.length - 1, index + delta))];
        setFocusedID(target.id);
        followRow(target.id);
        rowRefs.current.get(target.id)?.scrollIntoView({ block: 'nearest' });
    };

    const openModal = (itemID: number, opener: HTMLElement | null) => {
        // The invoking control itself, not document.activeElement: a pointer
        // activation does not necessarily move keyboard focus to the button it
        // clicks, and focus may still sit on an earlier control.
        modalOpenerRef.current = opener;
        setModalItemID(itemID);
    };

    const closeModal = () => {
        setModalItemID(null);
    };

    // Focus goes back to the row's name after the modal closes — not inside
    // the close handler, which Radix's unmount machinery overwrites. If the
    // opener's row left the list while the modal was open, the queue region
    // is the stable place to land.
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
            // A decision in the card moved the row focus on: the keyboard
            // focus lands on that row's name, not on the decided one.
            const focused = focusedIDRef.current;
            const moved =
                opener.hasAttribute('data-open-item') && focused !== null
                    ? rowRefs.current.get(focused)?.querySelector<HTMLElement>('[data-open-item]')
                    : null;
            (moved ?? opener).focus();
            return;
        }
        queueRegionRef.current?.focus();
    }, [modalItemID]);

    const openItem = (itemID: number, opener: HTMLElement | null) => {
        if (!isUsableId(itemID)) {
            return;
        }
        setFocusedID(itemID);
        if (isWide) {
            openModal(itemID, opener);
            return;
        }
        // Narrow: the detail is a screen of its own, and the queue's filter
        // and order travel in the address so Back returns to this view.
        const query = queueSearchParams(status, undefined, [], order).toString();
        navigate(`${REVIEW_DETAIL_ROUTE}/${itemID}${query === '' ? '' : `?${query}`}`);
    };

    const nameButtonOf = (itemID: number) =>
        rowRefs.current.get(itemID)?.querySelector<HTMLElement>('[data-open-item]') ?? null;

    // One listener while the queue is mounted. It reads the physical key, so
    // a Russian layout works, and it stays out of the way of fields, overlays,
    // the correction form and every modifier combination.
    const keysActive =
        listPhase === 'ready' &&
        modalItemID === null &&
        item.pendingAction === null &&
        !item.editOpen;
    const onKeyDown = (event: KeyboardEvent) => {
        if (!keysActive || event.defaultPrevented) {
            return;
        }
        if (event.ctrlKey || event.metaKey || event.altKey) {
            return;
        }
        const target = event.target instanceof Element ? event.target : null;
        if (target !== null && target.closest(TYPING_TARGETS) !== null) {
            return;
        }
        const action = KEY_ACTIONS[event.code];
        if (action === undefined) {
            return;
        }
        if (action === 'next' || action === 'previous') {
            event.preventDefault();
            moveFocus(action === 'next' ? 1 : -1);
            return;
        }
        if (focusedRow === null) {
            return;
        }
        if (action === 'open') {
            // A control does its own Enter, unless it belongs to a dialog on
            // its way out: that Enter is meant for the queue.
            const closing = target?.closest(CLOSING_DIALOG) ?? null;
            if (target !== null && closing === null && target.closest(ACTIVATABLE) !== null) {
                return;
            }
            event.preventDefault();
            openItem(focusedRow.id, nameButtonOf(focusedRow.id));
            return;
        }
        event.preventDefault();
        requestDecision(focusedRow, action);
    };
    const onKeyDownRef = useRef(onKeyDown);
    useEffect(() => {
        onKeyDownRef.current = onKeyDown;
    });
    useEffect(() => {
        const listener = (event: KeyboardEvent) => onKeyDownRef.current(event);
        document.addEventListener('keydown', listener);
        return () => document.removeEventListener('keydown', listener);
    }, []);

    const showKeys = isWide && deciding;
    const viewLabel = (name: string) => `${t('authorReview.view', 'View')}: ${name}`;
    const kindLabel = (row: AuthorReviewListItem) =>
        row.proposal_kind === null
            ? null
            : t(`authorReview.kind.${row.proposal_kind}`, KIND_FALLBACKS[row.proposal_kind]);

    const segment = (row: AuthorReviewListItem, focused: boolean) => (
        <div
            role="group"
            aria-label={t('authorReview.decision', 'Decision')}
            className={cn(
                'overflow-hidden rounded-md border border-border',
                isWide ? 'inline-flex' : 'flex w-full',
            )}
        >
            {DECISIONS.map(({ decision, label, fallback }, index) => {
                const full = t(label, fallback);
                return (
                    <Button
                        key={decision}
                        variant={decision === 'accept' ? 'default' : 'ghost'}
                        size="sm"
                        aria-label={full}
                        disabled={
                            !isUsableId(row.id) ||
                            (decision === 'accept' && row.proposal_kind === null)
                        }
                        onClick={() => requestDecision(row, decision)}
                        className={cn(
                            'rounded-none',
                            index > 0 && 'border-l border-border',
                            isWide ? 'h-8' : 'h-10 min-w-0 flex-1 px-1.5',
                        )}
                    >
                        {decision === 'unresolved'
                            ? t('authorReview.keepShort', 'As in file')
                            : full}
                        {focused && showKeys && (
                            <kbd
                                aria-hidden="true"
                                className="rounded border border-current px-1 py-px font-sans text-[11px] leading-none font-medium opacity-60"
                            >
                                {index + 1}
                            </kbd>
                        )}
                    </Button>
                );
            })}
        </div>
    );

    const verdict = (action: ScopedActionBody['type']) =>
        t(`authorReview.verdict.${action}`, VERDICT_FALLBACKS[action]);

    const evidence = (row: AuthorReviewListItem) => (
        <div className={cn('flex min-w-0 flex-col gap-3 pb-4', isWide ? 'px-5' : 'px-4')}>
            {focusedDetail === null && item.phase !== 'failed' && (
                <p className="text-sm text-muted-foreground">{t('loading', 'Loading...')}</p>
            )}
            {item.itemID === row.id && item.errorText && (
                <Alert variant="destructive">
                    <AlertDescription>{item.errorText}</AlertDescription>
                </Alert>
            )}
            {focusedDetail !== null && item.actionErrorText && (
                <Alert variant="destructive">
                    <AlertDescription>{item.actionErrorText}</AlertDescription>
                </Alert>
            )}
            {focusedDetail !== null && item.notice && !decided.has(row.id) && (
                <p role="status" className="text-sm text-muted-foreground">
                    {item.notice}
                </p>
            )}
            {focusedDetail !== null && <ReviewEvidence detail={focusedDetail} inline />}
            {focusedDetail !== null && deciding && item.editOpen && (
                <ReviewCorrectionForm
                    item={item}
                    autoFocus={focusForm}
                    onCancel={() => {
                        item.setEditOpen(false);
                        setFocusForm(false);
                    }}
                />
            )}
        </div>
    );

    const rows = (
        <ul
            role="list"
            aria-label={t('authorReview.listLabel', 'Review items')}
            className="flex flex-col"
        >
            {items.map((row) => {
                const focused = row.id === focusedID;
                const done = decided.get(row.id);
                const kind = kindLabel(row);
                return (
                    <li
                        key={row.id}
                        ref={(node) => {
                            if (node === null) {
                                rowRefs.current.delete(row.id);
                            } else {
                                rowRefs.current.set(row.id, node);
                            }
                        }}
                        aria-label={row.display_name}
                        aria-current={focused ? 'true' : undefined}
                        aria-disabled={done !== undefined ? 'true' : undefined}
                        onClick={(event) => {
                            if (
                                event.target instanceof Element &&
                                event.target.closest('button, a, input, [role="dialog"]') !== null
                            ) {
                                return;
                            }
                            setFocusedID(row.id);
                        }}
                        className={cn(
                            'border-b border-border/70 last:border-b-0',
                            focused && 'bg-accent shadow-[inset_3px_0_0_var(--primary)]',
                        )}
                    >
                        <div
                            className={cn(
                                'grid items-center',
                                isWide
                                    ? 'min-h-[46px] grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)_4rem_auto] gap-x-4 px-5 py-1.5'
                                    : 'grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-0.5 px-4 pt-3 pb-2',
                                done !== undefined && 'opacity-55',
                            )}
                        >
                            <button
                                type="button"
                                data-open-item
                                aria-label={viewLabel(row.display_name)}
                                disabled={!isUsableId(row.id)}
                                onClick={(event) => openItem(row.id, event.currentTarget)}
                                className={cn(
                                    'min-w-0 cursor-pointer rounded-sm text-left text-sm font-medium outline-none focus-visible:ring-[3px] focus-visible:ring-focus disabled:cursor-default',
                                    isWide ? 'truncate' : 'min-h-10 break-words',
                                )}
                            >
                                {row.source_display_name || row.display_name}
                            </button>
                            <span
                                className={cn(
                                    'text-sm text-muted-foreground',
                                    isWide ? 'truncate' : 'col-span-2 break-words',
                                )}
                            >
                                {kind === null ? (
                                    t('authorReview.noProposal', 'No proposal yet.')
                                ) : (
                                    <>
                                        →{' '}
                                        <span className="font-medium text-foreground">
                                            {row.display_name}
                                        </span>{' '}
                                        · {kind}
                                    </>
                                )}
                            </span>
                            <span
                                className={cn(
                                    'text-right text-sm text-muted-foreground tabular-nums',
                                    !isWide && 'col-start-2 row-start-1',
                                )}
                            >
                                {isWide
                                    ? formatCount(row.credits_count)
                                    : t('authorReview.booksCount', {
                                          defaultValue: '{{count}} books',
                                          count: row.credits_count,
                                      })}
                            </span>
                            {(deciding || done !== undefined) && (
                                <div
                                    className={cn(
                                        'flex justify-end',
                                        !isWide && 'col-span-2 mt-1.5',
                                    )}
                                >
                                    {done !== undefined ? (
                                        <span className="text-sm whitespace-nowrap text-muted-foreground">
                                            {verdict(done)}
                                        </span>
                                    ) : (
                                        segment(row, focused)
                                    )}
                                </div>
                            )}
                        </div>
                        {focused && evidence(row)}
                    </li>
                );
            })}
        </ul>
    );

    return (
        <div
            ref={queueRegionRef}
            role="region"
            aria-label={t('authorReview.title', 'Review queue')}
            tabIndex={-1}
            className="flex min-w-0 flex-col outline-none"
        >
            {tools && (
                <div className="flex justify-end pb-3">
                    <ReviewQueueTools />
                </div>
            )}

            {listPhase === 'loading' && (
                <p className="text-sm text-muted-foreground">{t('loading', 'Loading...')}</p>
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

            {listPhase === 'ready' && items.length > 0 && (
                // Full width inside the panel: the rows run edge to edge.
                <div
                    className={cn(
                        '-mx-4 border-b border-border/70 sm:-mx-5',
                        // In the panel the rows start right under its header.
                        tools ? 'border-t' : '-mt-4',
                    )}
                >
                    {isWide && (
                        <div
                            aria-hidden="true"
                            className="grid h-[34px] grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)_4rem_auto] items-center gap-x-4 border-b border-border/70 px-5 text-xs font-medium text-muted-foreground"
                        >
                            <span>{t('authorReview.sourceDisplay', 'As written')}</span>
                            <span>{t('authorReview.proposal', 'Proposed')}</span>
                            <span className="text-right">
                                {t('authorReview.col.books', 'Books')}
                            </span>
                            <span className="text-right">
                                {deciding ? t('authorReview.decision', 'Decision') : ''}
                            </span>
                        </div>
                    )}
                    {rows}
                </div>
            )}

            {listPhase === 'ready' && items.length > 0 && (
                <div className="flex flex-wrap items-center gap-x-3 gap-y-2 pt-3 text-sm text-muted-foreground">
                    {deciding && openCount !== null && (
                        <span className="tabular-nums">
                            {t('authorReview.shown', {
                                defaultValue: '{{shown}} of {{total}}',
                                shown: formatCount(items.length),
                                total: formatCount(openCount),
                            })}
                        </span>
                    )}
                    <span className="flex-1" />
                    {showKeys && (
                        <span className="inline-flex flex-wrap items-center gap-1 text-xs">
                            <Keycap>J</Keycap>
                            <Keycap>K</Keycap>
                            <span>{t('authorReview.keysHint.move', 'next')}</span>
                            <span aria-hidden="true">·</span>
                            <Keycap>1</Keycap>
                            <Keycap>2</Keycap>
                            <Keycap>3</Keycap>
                            <span>{t('authorReview.keysHint.decide', 'decide')}</span>
                            <span aria-hidden="true">·</span>
                            <Keycap>Enter</Keycap>
                            <span>{t('authorReview.keysHint.open', 'card')}</span>
                        </span>
                    )}
                    {nextCursor !== null && (
                        <Button variant="outline" disabled={moreLoading} onClick={loadMore}>
                            {t('authorReview.more', {
                                defaultValue: 'More {{count}}',
                                count: PAGE_LIMIT,
                            })}
                        </Button>
                    )}
                </div>
            )}

            <ReviewConfirmDialog item={item} onCloseAutoFocus={onConfirmClosed} />

            {/*
              The wide layout's detail: a modal over the list, so the rows
              stay exactly where they were. The content scrolls inside; Esc
              and the close button return focus to the row's name.
            */}
            <Dialog open={modalItemID !== null} onOpenChange={(open) => !open && closeModal()}>
                <DialogContent
                    closeLabel={t('close', 'Close')}
                    aria-labelledby="author-review-detail-heading"
                    aria-describedby={undefined}
                    className="max-h-[85vh] overflow-y-auto sm:max-w-2xl"
                >
                    {modalItemID !== null && (
                        <AuthorReviewDetail
                            key={modalItemID}
                            itemID={modalItemID}
                            onDecided={onDecided}
                        />
                    )}
                </DialogContent>
            </Dialog>
        </div>
    );
};

const Keycap: React.FC<{ children: React.ReactNode }> = ({ children }) => (
    <kbd className="rounded border border-current px-1 py-px font-sans text-[11px] leading-none font-medium opacity-60">
        {children}
    </kbd>
);

export default AuthorReviewQueue;
