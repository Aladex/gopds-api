import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Badge } from '@/shared/ui/badge';
import { Button } from '@/shared/ui/button';
import { Card, CardContent } from '@/shared/ui/card';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { Field } from '@/shared/ui/field';
import { Input } from '@/shared/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/ui/table';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { ADMIN_TABLE_WIDE_QUERY } from '@/shared/layout/breakpoints';
import { formatDate } from '@/shared/lib/formatDate';
import * as adminApi from '@/api/admin';
import type {
    AuthorReviewDetail,
    AuthorReviewEditResult,
    AuthorReviewKind,
    AuthorReviewListItem,
    AuthorReviewProposal,
    AuthorReviewScope,
} from '@/api/admin';
import { closedErrorCode } from '@/api/admin';

const PAGE_LIMIT = 50;

/**
 * The closed review error codes, with the English the locales replace. A Map
 * so that only its own entries are ever found; everything else — undocumented
 * code, inherited-object key, garbage body — renders as the generic message.
 */
const REVIEW_ERROR_FALLBACKS = new Map([
    [
        'review_conflict',
        'Another admin changed this review item. It has been refreshed; your draft is preserved.',
    ],
]);

/** The scoped action bodies the confirmation dialog can be holding. */
type ScopedActionBody =
    | { type: 'accept' }
    | { type: 'unresolved' }
    | { type: 'classify'; kind: 'collective' | 'unknown' | 'malformed' }
    | { type: 'edit'; result: AuthorReviewEditResult };

/** A body plus the item it was armed for, so a stale dialog can never fire. */
type PendingAction = ScopedActionBody & { itemID: number };

export interface ReviewEditDraft {
    given: string;
    additional: string;
    family: string;
    nickname: string;
    display: string;
    sort: string;
    kind: AuthorReviewKind;
}

/** The edit form's starting point: the server's proposal, or an empty sheet. */
export function draftFromProposal(proposal: AuthorReviewProposal | null): ReviewEditDraft {
    return {
        given: proposal?.given_name ?? '',
        additional: proposal?.additional_names ?? '',
        family: proposal?.family_name ?? '',
        nickname: proposal?.nickname ?? '',
        display: proposal?.display_name ?? '',
        sort: proposal?.sort_name ?? '',
        kind: proposal?.kind ?? 'unknown',
    };
}

const trimmedOrNullOr = (value: string): string | null => {
    const trimmed = value.trim();
    return trimmed === '' ? null : trimmed;
};

/**
 * Builds the edit payload: values trimmed, empty optionals sent as null, the
 * display name required — the server would reject the same omissions.
 */
export function buildEditResult(draft: ReviewEditDraft): {
    result?: AuthorReviewEditResult;
    error?: 'display_required';
} {
    const display = draft.display.trim();
    if (display === '') {
        return { error: 'display_required' };
    }
    return {
        result: {
            given_name: trimmedOrNullOr(draft.given),
            additional_names: trimmedOrNullOr(draft.additional),
            family_name: trimmedOrNullOr(draft.family),
            nickname: trimmedOrNullOr(draft.nickname),
            display_name: display,
            sort_name: trimmedOrNullOr(draft.sort),
            kind: draft.kind,
        },
    };
}

/** Only ids JSON parsing has not rounded may ever reach a route. */
const isUsableId = (id: number): boolean => Number.isSafeInteger(id) && id > 0;

/** Fallbacks for the dynamic status/scope/kind keys; the locales carry the real strings. */
const STATUS_VALUE_FALLBACKS = { open: 'Open', closed: 'Closed' } as const;
const SCOPE_VALUE_FALLBACKS = { credit: 'Credit', fingerprint: 'Fingerprint' } as const;
const SCOPE_CHOICE_FALLBACKS = { credit: 'Single credit', fingerprint: 'All credits' } as const;
const KIND_FALLBACKS: Record<AuthorReviewKind, string> = {
    person: 'Person',
    collective: 'Collective',
    unknown: 'Unknown',
    malformed: 'Malformed',
};

const KINDS: AuthorReviewKind[] = ['person', 'collective', 'unknown', 'malformed'];
const CLASSIFY_KINDS = ['collective', 'unknown', 'malformed'] as const;

/** A labelled figure; the value sits in its own node so each number is one text. */
const Stat: React.FC<{ label: React.ReactNode; value: React.ReactNode }> = ({ label, value }) => (
    <div className="flex items-baseline justify-between gap-4 text-sm">
        <span className="text-muted-foreground">{label}</span>
        <span className="text-right">{value}</span>
    </div>
);

const AuthorReviewQueue: React.FC = () => {
    const { t } = useTranslation();
    const isWide = useMediaQuery(ADMIN_TABLE_WIDE_QUERY);

    const [statusFilter, setStatusFilter] = useState<'open' | 'closed'>('open');
    const [cursor, setCursor] = useState<string | undefined>(undefined);
    const [cursorHistory, setCursorHistory] = useState<string[]>([]);
    const [items, setItems] = useState<AuthorReviewListItem[]>([]);
    const [nextCursor, setNextCursor] = useState<string | null>(null);
    const [listPhase, setListPhase] = useState<'loading' | 'ready' | 'failed'>('loading');
    const [listErrorText, setListErrorText] = useState<string | null>(null);

    const [selectedId, setSelectedId] = useState<number | null>(null);
    const [detail, setDetail] = useState<AuthorReviewDetail | null>(null);
    const [detailPhase, setDetailPhase] = useState<'idle' | 'loading' | 'ready' | 'failed'>('idle');
    const [detailErrorText, setDetailErrorText] = useState<string | null>(null);

    const [draft, setDraft] = useState<ReviewEditDraft | null>(null);
    const [formErrorText, setFormErrorText] = useState<string | null>(null);
    const [scopeChoice, setScopeChoice] = useState<AuthorReviewScope | null>(null);
    const [classifyKind, setClassifyKind] = useState<(typeof CLASSIFY_KINDS)[number] | null>(null);
    const [pendingAction, setPendingAction] = useState<PendingAction | null>(null);
    const [actionErrorText, setActionErrorText] = useState<string | null>(null);
    const [notice, setNotice] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);
    const [technicalOpen, setTechnicalOpen] = useState(false);

    const listGeneration = useRef(0);
    const detailGeneration = useRef(0);
    /**
     * Bumped on every selection. Actions stamp it when they start and discard
     * their results if a newer selection has happened since: a late response
     * for A must never install A's detail, notice or refresh over B.
     */
    const selectionGeneration = useRef(0);
    /** The element to return keyboard focus to when the dialog closes. */
    const dialogOpenerRef = useRef<HTMLElement | null>(null);

    const describeReviewError = useCallback(
        (error: unknown): string => {
            const code = closedErrorCode(error);
            const fallback = code !== null ? REVIEW_ERROR_FALLBACKS.get(code) : undefined;
            if (code !== null && fallback !== undefined) {
                return t(`authorReview.errors.${code}`, fallback);
            }
            return t('authorReview.actionError', 'Action failed.');
        },
        [t],
    );

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

    /**
     * Loads one detail. `resetDraft` says whether the edit form restarts from
     * the server's proposal — a conflict refresh keeps what the admin typed.
     */
    const fetchDetail = useCallback(
        async (itemID: number, resetDraft: boolean) => {
            if (!isUsableId(itemID)) {
                return;
            }
            const generation = ++detailGeneration.current;
            setDetailPhase('loading');
            setDetailErrorText(null);
            try {
                const data = await adminApi.getAuthorReviewItem(itemID);
                if (generation !== detailGeneration.current) {
                    return;
                }
                setDetail(data.item);
                if (resetDraft) {
                    setDraft(draftFromProposal(data.item.proposal));
                }
                setDetailPhase('ready');
            } catch (error) {
                if (generation !== detailGeneration.current) {
                    return;
                }
                setDetailErrorText(describeReviewError(error));
                setDetailPhase('failed');
            }
        },
        [describeReviewError],
    );

    const selectItem = (itemID: number) => {
        if (!isUsableId(itemID)) {
            return;
        }
        selectionGeneration.current += 1;
        setSelectedId(itemID);
        setScopeChoice(null);
        setClassifyKind(null);
        setFormErrorText(null);
        setActionErrorText(null);
        setNotice(null);
        setTechnicalOpen(false);
        setPendingAction(null);
        fetchDetail(itemID, true);
    };

    const changeStatus = (status: 'open' | 'closed') => {
        setStatusFilter(status);
        setCursor(undefined);
        setCursorHistory([]);
    };

    const goNextPage = () => {
        if (nextCursor === null) {
            return;
        }
        setCursorHistory((history) => [...history, cursor ?? '']);
        setCursor(nextCursor);
    };

    const goPreviousPage = () => {
        setCursorHistory((history) => {
            if (history.length === 0) {
                return history;
            }
            const target = history[history.length - 1];
            setCursor(target === '' ? undefined : target);
            return history.slice(0, -1);
        });
    };

    const scopeChosen = scopeChoice !== null;
    const scopedActionDisabled = busy || !scopeChosen || selectedId === null;

    const armScopedAction = (action: ScopedActionBody) => {
        if (scopedActionDisabled || selectedId === null) {
            return;
        }
        setActionErrorText(null);
        setNotice(null);
        dialogOpenerRef.current =
            document.activeElement instanceof HTMLElement ? document.activeElement : null;
        setPendingAction({ ...action, itemID: selectedId });
    };

    const handleSaveEdit = () => {
        if (draft === null || scopedActionDisabled || selectedId === null) {
            return;
        }
        const built = buildEditResult(draft);
        if (built.result === undefined) {
            setFormErrorText(t('authorReview.editDisplayRequired', 'Display name is required.'));
            return;
        }
        setFormErrorText(null);
        armScopedAction({ type: 'edit', result: built.result });
    };

    const closeDialog = () => {
        setPendingAction(null);
    };

    // Focus is handed back after the commit, not inside the close handler:
    // the dialog's focus machinery runs while unmounting its content and
    // lands on <body>, overwriting anything focused earlier. BooksList does
    // the same for the reader dialog. Confirm additionally disables the
    // opener for the duration of the request, so a target that cannot take
    // focus yet is kept and restored once the request settles.
    useEffect(() => {
        if (pendingAction !== null || busy) {
            return;
        }
        const opener = dialogOpenerRef.current;
        if (opener === null) {
            return;
        }
        if (document.contains(opener) && !opener.hasAttribute('disabled')) {
            dialogOpenerRef.current = null;
            opener.focus();
        }
    }, [pendingAction, busy]);

    const confirmPending = () => {
        if (pendingAction === null || scopeChoice === null) {
            return;
        }
        // The confirmed target must still be the item the dialog was armed for.
        if (pendingAction.itemID !== selectedId) {
            setPendingAction(null);
            return;
        }
        const itemID = selectedId;
        const scope = scopeChoice;
        const action: ScopedActionBody = pendingAction;
        const generation = selectionGeneration.current;
        setPendingAction(null);
        setBusy(true);
        setActionErrorText(null);
        setNotice(null);
        const request =
            action.type === 'accept'
                ? adminApi.acceptAuthorReviewItem(itemID, { scope })
                : action.type === 'unresolved'
                  ? adminApi.markAuthorReviewUnresolved(itemID, { scope })
                  : action.type === 'classify'
                    ? adminApi.classifyAuthorReviewItem(itemID, { scope, kind: action.kind })
                    : adminApi.editAuthorReviewItem(itemID, { scope, result: action.result });
        request
            .then((data) => {
                // A response for a superseded selection belongs to nobody here.
                if (generation !== selectionGeneration.current) {
                    return;
                }
                // The response replaces the audit view. The list keeps the
                // prior item until it is fetched again — no optimistic rewrite.
                const item = (data as { item?: AuthorReviewDetail }).item;
                if (item !== undefined) {
                    setDetail(item);
                }
            })
            .catch((error) => {
                if (generation !== selectionGeneration.current) {
                    return;
                }
                if (closedErrorCode(error) === 'review_conflict') {
                    setNotice(describeReviewError(error));
                    // The draft survives: only a fresh selection resets it.
                    fetchDetail(itemID, false);
                } else {
                    setActionErrorText(describeReviewError(error));
                }
            })
            .finally(() => setBusy(false));
    };

    const handleRetry = () => {
        if (selectedId === null || busy || detailPhase !== 'ready') {
            return;
        }
        const itemID = selectedId;
        const generation = selectionGeneration.current;
        setBusy(true);
        setActionErrorText(null);
        setNotice(null);
        adminApi
            .retryAuthorReviewNormalization(itemID)
            .then(() => {
                if (generation === selectionGeneration.current) {
                    return fetchDetail(itemID, false);
                }
                return undefined;
            })
            .catch((error) => {
                if (generation === selectionGeneration.current) {
                    setActionErrorText(describeReviewError(error));
                }
            })
            .finally(() => setBusy(false));
    };

    const updateDraft = (patch: Partial<ReviewEditDraft>) => {
        setDraft((current) => (current === null ? current : { ...current, ...patch }));
    };

    const viewLabel = (name: string) => `${t('authorReview.view', 'View')}: ${name}`;

    const rowActions = (item: AuthorReviewListItem) => (
        <Button
            variant="outline"
            size="sm"
            disabled={!isUsableId(item.id)}
            aria-label={viewLabel(item.display_name)}
            onClick={() => selectItem(item.id)}
        >
            {t('authorReview.view', 'View')}
        </Button>
    );

    return (
        <div className="flex flex-col gap-4">
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

            {detailPhase !== 'idle' && (
                <Card>
                    <CardContent className="flex flex-col gap-3">
                        <section
                            aria-labelledby="author-review-detail-heading"
                            className="flex flex-col gap-3"
                        >
                            <div className="flex flex-wrap items-center gap-2">
                                <h3
                                    id="author-review-detail-heading"
                                    className="text-base font-medium"
                                >
                                    {t('authorReview.detailTitle', 'Review item')}
                                </h3>
                                {detail !== null && (
                                    <Badge>
                                        {t(
                                            `authorReview.scopeValue.${detail.scope}`,
                                            SCOPE_VALUE_FALLBACKS[detail.scope],
                                        )}
                                    </Badge>
                                )}
                                <Button
                                    variant="outline"
                                    size="sm"
                                    className="ml-auto"
                                    disabled={
                                        busy || selectedId === null || detailPhase !== 'ready'
                                    }
                                    onClick={handleRetry}
                                >
                                    {t('authorReview.retry', 'Retry normalization')}
                                </Button>
                            </div>

                            {detailPhase === 'loading' && (
                                <p className="text-sm text-muted-foreground">
                                    {t('loading', 'Loading...')}
                                </p>
                            )}
                            {detailErrorText && (
                                <Alert variant="destructive">
                                    <AlertDescription>{detailErrorText}</AlertDescription>
                                </Alert>
                            )}
                            {actionErrorText && (
                                <Alert variant="destructive">
                                    <AlertDescription>{actionErrorText}</AlertDescription>
                                </Alert>
                            )}
                            {notice && (
                                <p role="status" className="text-sm text-muted-foreground">
                                    {notice}
                                </p>
                            )}

                            {detail !== null && detailPhase === 'ready' && (
                                <>
                                    <div className="flex flex-col gap-1">
                                        <p className="text-base font-medium break-words">
                                            {detail.display_name}
                                        </p>
                                        <Stat
                                            label={t('authorReview.col.reason', 'Reason')}
                                            value={detail.reason}
                                        />
                                        <Stat
                                            label={t('authorReview.col.class', 'Class')}
                                            value={detail.decision_class}
                                        />
                                        <Stat
                                            label={t('authorReview.col.credits', 'Credits')}
                                            value={detail.credits_count}
                                        />
                                        <Stat
                                            label={t('authorReview.createdLabel', 'Created')}
                                            value={formatDate(detail.created_at)}
                                        />
                                    </div>

                                    <div className="grid gap-3 md:grid-cols-2">
                                        <section
                                            aria-labelledby="author-review-source-heading"
                                            className="flex flex-col gap-2 rounded-lg border border-border p-3"
                                        >
                                            <h4
                                                id="author-review-source-heading"
                                                className="text-sm font-medium"
                                            >
                                                {t('authorReview.source', 'Source')}
                                            </h4>
                                            <Stat
                                                label={t('authorReview.sourceFirst', 'First name')}
                                                value={detail.source.first ?? '—'}
                                            />
                                            <Stat
                                                label={t(
                                                    'authorReview.sourceMiddle',
                                                    'Middle name',
                                                )}
                                                value={detail.source.middle ?? '—'}
                                            />
                                            <Stat
                                                label={t('authorReview.sourceLast', 'Last name')}
                                                value={detail.source.last ?? '—'}
                                            />
                                            <Stat
                                                label={t('authorReview.sourceNickname', 'Nickname')}
                                                value={detail.source.nickname ?? '—'}
                                            />
                                            <Stat
                                                label={t(
                                                    'authorReview.sourceDisplay',
                                                    'As written',
                                                )}
                                                value={detail.source.display}
                                            />
                                            {detail.source.flags.length > 0 && (
                                                <div className="flex flex-wrap gap-1">
                                                    {detail.source.flags.map((flag) => (
                                                        <Badge key={flag} variant="secondary">
                                                            {flag}
                                                        </Badge>
                                                    ))}
                                                </div>
                                            )}
                                        </section>

                                        <section
                                            aria-labelledby="author-review-proposal-heading"
                                            className="flex flex-col gap-2 rounded-lg border border-border p-3"
                                        >
                                            <h4
                                                id="author-review-proposal-heading"
                                                className="text-sm font-medium"
                                            >
                                                {t('authorReview.proposal', 'Proposal')}
                                            </h4>
                                            {detail.proposal === null ? (
                                                <p className="text-sm text-muted-foreground">
                                                    {t(
                                                        'authorReview.noProposal',
                                                        'No local proposal yet.',
                                                    )}
                                                </p>
                                            ) : (
                                                <>
                                                    <Stat
                                                        label={t(
                                                            'authorReview.proposalKind',
                                                            'Kind',
                                                        )}
                                                        value={t(
                                                            `authorReview.kind.${detail.proposal.kind}`,
                                                            KIND_FALLBACKS[detail.proposal.kind],
                                                        )}
                                                    />
                                                    <Stat
                                                        label={t(
                                                            'authorReview.proposalScript',
                                                            'Script',
                                                        )}
                                                        value={detail.proposal.script}
                                                    />
                                                    <Stat
                                                        label={t(
                                                            'authorReview.proposalMethod',
                                                            'Method',
                                                        )}
                                                        value={detail.proposal.method}
                                                    />
                                                    {detail.proposal.quality_flags.length > 0 && (
                                                        <div className="flex flex-wrap gap-1">
                                                            {detail.proposal.quality_flags.map(
                                                                (flag) => (
                                                                    <Badge
                                                                        key={flag}
                                                                        variant="secondary"
                                                                    >
                                                                        {flag}
                                                                    </Badge>
                                                                ),
                                                            )}
                                                        </div>
                                                    )}
                                                </>
                                            )}
                                        </section>
                                    </div>

                                    <section
                                        aria-labelledby="author-review-books-heading"
                                        className="flex flex-col gap-1"
                                    >
                                        <h4
                                            id="author-review-books-heading"
                                            className="text-sm font-medium"
                                        >
                                            {t('authorReview.linkedBooks', 'Linked books')}
                                        </h4>
                                        {detail.linked_books.length === 0 ? (
                                            <p className="text-sm text-muted-foreground">
                                                {t('authorReview.noLinkedBooks', 'None.')}
                                            </p>
                                        ) : (
                                            <ul className="ml-4 list-disc text-sm">
                                                {detail.linked_books.map((book) => (
                                                    <li key={book.id}>{book.title}</li>
                                                ))}
                                            </ul>
                                        )}
                                    </section>

                                    <div>
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            aria-expanded={technicalOpen}
                                            onClick={() => setTechnicalOpen((open) => !open)}
                                        >
                                            {technicalOpen
                                                ? t(
                                                      'authorReview.hideTechnical',
                                                      'Hide technical details',
                                                  )
                                                : t(
                                                      'authorReview.showTechnical',
                                                      'Show technical details',
                                                  )}
                                        </Button>
                                        {technicalOpen && (
                                            <div className="mt-2 flex flex-col gap-1 rounded-lg border border-border p-3">
                                                <Stat
                                                    label={t(
                                                        'authorReview.fingerprint',
                                                        'Source fingerprint',
                                                    )}
                                                    value={detail.source_fingerprint}
                                                />
                                                <Stat
                                                    label={t('authorReview.creditId', 'Credit ID')}
                                                    value={detail.credit_id ?? '—'}
                                                />
                                                {detail.proposal !== null && (
                                                    <Stat
                                                        label={t(
                                                            'authorReview.searchKey',
                                                            'Search key',
                                                        )}
                                                        value={detail.proposal.search_key}
                                                    />
                                                )}
                                            </div>
                                        )}
                                    </div>

                                    <section
                                        aria-labelledby="author-review-edit-heading"
                                        className="flex flex-col gap-3 rounded-lg border border-border p-3"
                                    >
                                        <h4
                                            id="author-review-edit-heading"
                                            className="text-sm font-medium"
                                        >
                                            {t('authorReview.edit', 'Edit result')}
                                        </h4>
                                        {draft !== null && (
                                            <>
                                                <div className="grid gap-3 sm:grid-cols-2">
                                                    <Field
                                                        id="author-review-given"
                                                        label={t(
                                                            'authorReview.givenName',
                                                            'Given name',
                                                        )}
                                                    >
                                                        <Input
                                                            id="author-review-given"
                                                            value={draft.given}
                                                            onChange={(event) =>
                                                                updateDraft({
                                                                    given: event.target.value,
                                                                })
                                                            }
                                                        />
                                                    </Field>
                                                    <Field
                                                        id="author-review-additional"
                                                        label={t(
                                                            'authorReview.additionalNames',
                                                            'Additional names',
                                                        )}
                                                    >
                                                        <Input
                                                            id="author-review-additional"
                                                            value={draft.additional}
                                                            onChange={(event) =>
                                                                updateDraft({
                                                                    additional: event.target.value,
                                                                })
                                                            }
                                                        />
                                                    </Field>
                                                    <Field
                                                        id="author-review-family"
                                                        label={t(
                                                            'authorReview.familyName',
                                                            'Family name',
                                                        )}
                                                    >
                                                        <Input
                                                            id="author-review-family"
                                                            value={draft.family}
                                                            onChange={(event) =>
                                                                updateDraft({
                                                                    family: event.target.value,
                                                                })
                                                            }
                                                        />
                                                    </Field>
                                                    <Field
                                                        id="author-review-nickname"
                                                        label={t(
                                                            'authorReview.nickname',
                                                            'Nickname',
                                                        )}
                                                    >
                                                        <Input
                                                            id="author-review-nickname"
                                                            value={draft.nickname}
                                                            onChange={(event) =>
                                                                updateDraft({
                                                                    nickname: event.target.value,
                                                                })
                                                            }
                                                        />
                                                    </Field>
                                                    <Field
                                                        id="author-review-display"
                                                        label={t(
                                                            'authorReview.displayName',
                                                            'Display name',
                                                        )}
                                                        error={formErrorText ?? undefined}
                                                    >
                                                        <Input
                                                            id="author-review-display"
                                                            value={draft.display}
                                                            onChange={(event) =>
                                                                updateDraft({
                                                                    display: event.target.value,
                                                                })
                                                            }
                                                        />
                                                    </Field>
                                                    <Field
                                                        id="author-review-sort"
                                                        label={t(
                                                            'authorReview.sortName',
                                                            'Sort name',
                                                        )}
                                                    >
                                                        <Input
                                                            id="author-review-sort"
                                                            value={draft.sort}
                                                            onChange={(event) =>
                                                                updateDraft({
                                                                    sort: event.target.value,
                                                                })
                                                            }
                                                        />
                                                    </Field>
                                                </div>
                                                <div
                                                    role="group"
                                                    aria-label={t(
                                                        'authorReview.resultKind',
                                                        'Result kind',
                                                    )}
                                                    className="flex flex-wrap gap-2"
                                                >
                                                    {KINDS.map((kind) => (
                                                        <Button
                                                            key={kind}
                                                            variant={
                                                                draft.kind === kind
                                                                    ? 'default'
                                                                    : 'outline'
                                                            }
                                                            size="sm"
                                                            aria-pressed={draft.kind === kind}
                                                            onClick={() => updateDraft({ kind })}
                                                        >
                                                            {t(
                                                                `authorReview.kind.${kind}`,
                                                                KIND_FALLBACKS[kind],
                                                            )}
                                                        </Button>
                                                    ))}
                                                </div>
                                            </>
                                        )}
                                    </section>

                                    <section className="flex flex-col gap-3">
                                        <div className="flex flex-col gap-2">
                                            <div
                                                role="group"
                                                aria-label={t('authorReview.scope', 'Action scope')}
                                                className="flex flex-wrap gap-2"
                                            >
                                                {(['credit', 'fingerprint'] as const).map(
                                                    (scope) => (
                                                        <Button
                                                            key={scope}
                                                            variant={
                                                                scopeChoice === scope
                                                                    ? 'default'
                                                                    : 'outline'
                                                            }
                                                            size="sm"
                                                            aria-pressed={scopeChoice === scope}
                                                            onClick={() => setScopeChoice(scope)}
                                                        >
                                                            {t(
                                                                `authorReview.scopeChoice.${scope}`,
                                                                SCOPE_CHOICE_FALLBACKS[scope],
                                                            )}
                                                        </Button>
                                                    ),
                                                )}
                                            </div>
                                            <p className="text-xs text-muted-foreground">
                                                {t(
                                                    'authorReview.scopeHint',
                                                    'Choose what the action applies to — the scope is never chosen for you.',
                                                )}
                                            </p>
                                        </div>

                                        <div className="flex flex-wrap gap-2">
                                            <Button
                                                size="sm"
                                                disabled={scopedActionDisabled}
                                                onClick={() => armScopedAction({ type: 'accept' })}
                                            >
                                                {t('authorReview.accept', 'Accept')}
                                            </Button>
                                            <Button
                                                variant="outline"
                                                size="sm"
                                                disabled={scopedActionDisabled || draft === null}
                                                onClick={handleSaveEdit}
                                            >
                                                {t('authorReview.saveEdit', 'Save edit')}
                                            </Button>
                                            <Button
                                                variant="outline"
                                                size="sm"
                                                disabled={scopedActionDisabled || busy}
                                                onClick={() => {
                                                    if (
                                                        classifyKind === null ||
                                                        scopedActionDisabled
                                                    ) {
                                                        return;
                                                    }
                                                    armScopedAction({
                                                        type: 'classify',
                                                        kind: classifyKind,
                                                    });
                                                }}
                                            >
                                                {t('authorReview.classify', 'Classify')}
                                            </Button>
                                            <Button
                                                variant="outline"
                                                size="sm"
                                                disabled={scopedActionDisabled}
                                                onClick={() =>
                                                    armScopedAction({ type: 'unresolved' })
                                                }
                                            >
                                                {t('authorReview.unresolved', 'Mark unresolved')}
                                            </Button>
                                        </div>

                                        <div
                                            role="group"
                                            aria-label={t(
                                                'authorReview.classifyKind',
                                                'Classify kind',
                                            )}
                                            className="flex flex-wrap gap-2"
                                        >
                                            {CLASSIFY_KINDS.map((kind) => (
                                                <Button
                                                    key={kind}
                                                    variant={
                                                        classifyKind === kind
                                                            ? 'default'
                                                            : 'outline'
                                                    }
                                                    size="sm"
                                                    aria-pressed={classifyKind === kind}
                                                    onClick={() => setClassifyKind(kind)}
                                                >
                                                    {t(
                                                        `authorReview.kind.${kind}`,
                                                        KIND_FALLBACKS[kind],
                                                    )}
                                                </Button>
                                            ))}
                                        </div>
                                    </section>
                                </>
                            )}
                        </section>
                    </CardContent>
                </Card>
            )}

            <Dialog open={pendingAction !== null} onOpenChange={(open) => !open && closeDialog()}>
                <DialogContent closeLabel={t('close', 'Close')}>
                    <DialogHeader>
                        <DialogTitle>
                            {t('authorReview.confirmTitle', 'Confirm the action')}
                        </DialogTitle>
                        <DialogDescription>
                            {scopeChoice === 'credit'
                                ? t(
                                      'authorReview.confirmCredit',
                                      'Apply this action to this single credit?',
                                  )
                                : detail !== null &&
                                    pendingAction !== null &&
                                    detail.id === pendingAction.itemID
                                  ? t('authorReview.confirmFingerprint', {
                                        defaultValue:
                                            'Apply this action to all {{count}} credits of this fingerprint?',
                                        count: detail.credits_count,
                                    })
                                  : null}
                        </DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button variant="ghost" onClick={closeDialog}>
                            {t('cancel', 'Cancel')}
                        </Button>
                        <Button onClick={confirmPending}>
                            {t('authorReview.confirm', 'Confirm')}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
};

export default AuthorReviewQueue;
