import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router';
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
import { formatDate } from '@/shared/lib/formatDate';
import * as adminApi from '@/api/admin';
import type {
    AuthorReviewDetail as AuthorReviewDetailData,
    AuthorReviewEditResult,
    AuthorReviewKind,
    AuthorReviewProposal,
    AuthorReviewScope,
} from '@/api/admin';
import { closedErrorCode } from '@/api/admin';

/**
 * The review item detail. One self-contained component per item id: the queue
 * shows it inside a modal dialog on wide screens, and the narrow layout opens
 * it on its own route (below) so a detail is a place the browser can go back
 * from and a link can point at. Everything an admin decides about an item —
 * the draft, the scope, the confirmations — lives and dies with this
 * component, so a late response for a closed view can install nothing.
 */

/** The route the review detail screen lives at (narrow layout, deep-linkable). */
export const REVIEW_DETAIL_ROUTE = '/admin/author-normalization/review';

/** The queue's list state, kept in the address so Back can restore it. */
export const REVIEW_STATUS_PARAM = 'status';
export const REVIEW_CURSOR_PARAM = 'cursor';
export const REVIEW_HISTORY_PARAM = 'h';

export const queueSearchParams = (
    status: 'open' | 'closed',
    cursor: string | undefined,
    history: string[],
): URLSearchParams => {
    const params = new URLSearchParams();
    params.set(REVIEW_STATUS_PARAM, status);
    if (cursor !== undefined) {
        params.set(REVIEW_CURSOR_PARAM, cursor);
    }
    for (const entry of history) {
        params.append(REVIEW_HISTORY_PARAM, entry);
    }
    return params;
};

export const statusFromParams = (params: URLSearchParams): 'open' | 'closed' =>
    params.get(REVIEW_STATUS_PARAM) === 'closed' ? 'closed' : 'open';

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

/**
 * The id an address may carry: a canonical positive decimal integer that
 * Number represents exactly. Everything else — text, a padded number, a value
 * beyond the safe range — is refused before any request is built, the same
 * rule the server's :id parsing applies.
 */
const parseRouteItemID = (raw: string | undefined): number | null => {
    if (raw === undefined || !/^\d+$/.test(raw)) {
        return null;
    }
    const id = Number(raw);
    if (!isUsableId(id) || String(id) !== raw) {
        return null;
    }
    return id;
};

/** Fallbacks for the dynamic status/scope/kind keys; the locales carry the real strings. */
const SCOPE_VALUE_FALLBACKS = { credit: 'Credit', fingerprint: 'Fingerprint' } as const;
const SCOPE_CHOICE_FALLBACKS = { credit: 'Single credit', fingerprint: 'All credits' } as const;
const KIND_FALLBACKS: Record<AuthorReviewKind, string> = {
    person: 'Person',
    collective: 'Collective',
    unknown: 'Unknown',
    malformed: 'Malformed',
};

const KINDS: AuthorReviewKind[] = ['person', 'collective', 'unknown', 'malformed'];

/** A labelled figure; the value sits in its own node so each number is one text. */
const Stat: React.FC<{ label: React.ReactNode; value: React.ReactNode }> = ({ label, value }) => (
    <div className="flex items-baseline justify-between gap-4 text-sm">
        <span className="text-muted-foreground">{label}</span>
        <span className="text-right">{value}</span>
    </div>
);

/** Renders a closed review error code as its localized text, or the generic failure. */
export const useReviewErrorDescriber = () => {
    const { t } = useTranslation();
    return useCallback(
        (error: unknown): string => {
            const code = closedErrorCode(error);
            const fallback = code !== null ? REVIEW_ERROR_FALLBACKS.get(code) : undefined;
            if (code !== null && fallback !== undefined) {
                return t(`authorReview.errors.${code}`, { defaultValue: fallback });
            }
            return t('authorReview.actionError', { defaultValue: 'Action failed.' });
        },
        [t],
    );
};

/** One review item's full detail: the audit view, the edit form and the scoped actions. */
export const AuthorReviewDetail: React.FC<{ itemID: number }> = ({ itemID }) => {
    const { t } = useTranslation();

    const [detail, setDetail] = useState<AuthorReviewDetailData | null>(null);
    const [detailPhase, setDetailPhase] = useState<'loading' | 'ready' | 'failed'>('loading');
    const [detailErrorText, setDetailErrorText] = useState<string | null>(null);

    const [draft, setDraft] = useState<ReviewEditDraft | null>(null);
    const [formErrorText, setFormErrorText] = useState<string | null>(null);
    const [scopeChoice, setScopeChoice] = useState<AuthorReviewScope | null>(null);
    const [pendingAction, setPendingAction] = useState<PendingAction | null>(null);
    const [actionErrorText, setActionErrorText] = useState<string | null>(null);
    const [notice, setNotice] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);
    const [technicalOpen, setTechnicalOpen] = useState(false);

    const detailGeneration = useRef(0);
    /**
     * False once this view unmounted — a modal closed, a route left. A late
     * response must do nothing at all, not even trigger its refresh.
     */
    const mountedRef = useRef(true);
    useEffect(() => {
        mountedRef.current = true;
        return () => {
            mountedRef.current = false;
        };
    }, []);
    /** The element to return keyboard focus to when the dialog closes. */
    const dialogOpenerRef = useRef<HTMLElement | null>(null);

    const describeReviewError = useReviewErrorDescriber();

    /**
     * Loads the item. `resetDraft` says whether the edit form restarts from
     * the server's proposal — a conflict refresh keeps what the admin typed.
     */
    const fetchDetail = useCallback(
        async (resetDraft: boolean) => {
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
        [describeReviewError, itemID],
    );

    useEffect(() => {
        setScopeChoice(null);
        setFormErrorText(null);
        setActionErrorText(null);
        setNotice(null);
        setTechnicalOpen(false);
        setPendingAction(null);
        fetchDetail(true);
    }, [fetchDetail]);

    const scopeChosen = scopeChoice !== null;
    const scopedActionDisabled = busy || !scopeChosen;
    /** Classify sends the chosen kind, and only three kinds are classifiable. */
    const classifyKind =
        draft !== null && draft.kind !== 'person'
            ? (draft.kind as 'collective' | 'unknown' | 'malformed')
            : null;

    const armScopedAction = (action: ScopedActionBody) => {
        if (scopedActionDisabled) {
            return;
        }
        setActionErrorText(null);
        setNotice(null);
        dialogOpenerRef.current =
            document.activeElement instanceof HTMLElement ? document.activeElement : null;
        setPendingAction({ ...action, itemID });
    };

    const handleSaveEdit = () => {
        if (draft === null || scopedActionDisabled) {
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
        if (pendingAction.itemID !== itemID) {
            setPendingAction(null);
            return;
        }
        const scope = scopeChoice;
        const action: ScopedActionBody = pendingAction;
        const generation = detailGeneration.current;
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
                if (!mountedRef.current || generation !== detailGeneration.current) {
                    return;
                }
                // The response replaces the audit view. The list keeps the
                // prior item until it is fetched again — no optimistic rewrite.
                setDetail(data.item);
            })
            .catch((error) => {
                if (!mountedRef.current || generation !== detailGeneration.current) {
                    return;
                }
                if (closedErrorCode(error) === 'review_conflict') {
                    setNotice(describeReviewError(error));
                    // The draft survives: only a fresh view resets it.
                    fetchDetail(false);
                } else {
                    setActionErrorText(describeReviewError(error));
                }
            })
            .finally(() => {
                if (mountedRef.current) {
                    setBusy(false);
                }
            });
    };

    const handleRetry = () => {
        if (busy || detailPhase !== 'ready') {
            return;
        }
        const generation = detailGeneration.current;
        setBusy(true);
        setActionErrorText(null);
        setNotice(null);
        adminApi
            .retryAuthorReviewNormalization(itemID)
            .then(() => {
                if (mountedRef.current && generation === detailGeneration.current) {
                    return fetchDetail(false);
                }
                return undefined;
            })
            .catch((error) => {
                if (mountedRef.current && generation === detailGeneration.current) {
                    setActionErrorText(describeReviewError(error));
                }
            })
            .finally(() => {
                if (mountedRef.current) {
                    setBusy(false);
                }
            });
    };

    const updateDraft = (patch: Partial<ReviewEditDraft>) => {
        setDraft((current) => (current === null ? current : { ...current, ...patch }));
    };

    return (
        <section aria-labelledby="author-review-detail-heading" className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
                <h3 id="author-review-detail-heading" className="text-base font-medium">
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
                    disabled={busy || detailPhase !== 'ready'}
                    onClick={handleRetry}
                >
                    {t('authorReview.retry', 'Retry normalization')}
                </Button>
            </div>

            {detailPhase === 'loading' && (
                <p className="text-sm text-muted-foreground">{t('loading', 'Loading...')}</p>
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
                        <p className="text-base font-medium break-words">{detail.display_name}</p>
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
                            <h4 id="author-review-source-heading" className="text-sm font-medium">
                                {t('authorReview.source', 'Source')}
                            </h4>
                            <Stat
                                label={t('authorReview.sourceFirst', 'First name')}
                                value={detail.source.first ?? '—'}
                            />
                            <Stat
                                label={t('authorReview.sourceMiddle', 'Middle name')}
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
                                label={t('authorReview.sourceDisplay', 'As written')}
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
                            <h4 id="author-review-proposal-heading" className="text-sm font-medium">
                                {t('authorReview.proposal', 'Proposal')}
                            </h4>
                            {detail.proposal === null ? (
                                <p className="text-sm text-muted-foreground">
                                    {t('authorReview.noProposal', 'No local proposal yet.')}
                                </p>
                            ) : (
                                <>
                                    <Stat
                                        label={t('authorReview.proposalKind', 'Kind')}
                                        value={t(
                                            `authorReview.kind.${detail.proposal.kind}`,
                                            KIND_FALLBACKS[detail.proposal.kind],
                                        )}
                                    />
                                    <Stat
                                        label={t('authorReview.proposalScript', 'Script')}
                                        value={detail.proposal.script}
                                    />
                                    <Stat
                                        label={t('authorReview.proposalMethod', 'Method')}
                                        value={detail.proposal.method}
                                    />
                                    {detail.proposal.quality_flags.length > 0 && (
                                        <div className="flex flex-wrap gap-1">
                                            {detail.proposal.quality_flags.map((flag) => (
                                                <Badge key={flag} variant="secondary">
                                                    {flag}
                                                </Badge>
                                            ))}
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
                        <h4 id="author-review-books-heading" className="text-sm font-medium">
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
                                ? t('authorReview.hideTechnical', 'Hide technical details')
                                : t('authorReview.showTechnical', 'Show technical details')}
                        </Button>
                        {technicalOpen && (
                            <div className="mt-2 flex flex-col gap-1 rounded-lg border border-border p-3">
                                <Stat
                                    label={t('authorReview.fingerprint', 'Source fingerprint')}
                                    value={detail.source_fingerprint}
                                />
                                <Stat
                                    label={t('authorReview.creditId', 'Credit ID')}
                                    value={detail.credit_id ?? '—'}
                                />
                                {detail.proposal !== null && (
                                    <Stat
                                        label={t('authorReview.searchKey', 'Search key')}
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
                        <h4 id="author-review-edit-heading" className="text-sm font-medium">
                            {t('authorReview.edit', 'Edit result')}
                        </h4>
                        {draft !== null && (
                            <>
                                <div className="grid gap-3 sm:grid-cols-2">
                                    <Field
                                        id="author-review-given"
                                        label={t('authorReview.givenName', 'Given name')}
                                    >
                                        <Input
                                            id="author-review-given"
                                            value={draft.given}
                                            onChange={(event) =>
                                                updateDraft({ given: event.target.value })
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
                                                updateDraft({ additional: event.target.value })
                                            }
                                        />
                                    </Field>
                                    <Field
                                        id="author-review-family"
                                        label={t('authorReview.familyName', 'Family name')}
                                    >
                                        <Input
                                            id="author-review-family"
                                            value={draft.family}
                                            onChange={(event) =>
                                                updateDraft({ family: event.target.value })
                                            }
                                        />
                                    </Field>
                                    <Field
                                        id="author-review-nickname"
                                        label={t('authorReview.nickname', 'Nickname')}
                                    >
                                        <Input
                                            id="author-review-nickname"
                                            value={draft.nickname}
                                            onChange={(event) =>
                                                updateDraft({ nickname: event.target.value })
                                            }
                                        />
                                    </Field>
                                    <Field
                                        id="author-review-display"
                                        label={t('authorReview.displayName', 'Display name')}
                                        error={formErrorText ?? undefined}
                                    >
                                        <Input
                                            id="author-review-display"
                                            value={draft.display}
                                            onChange={(event) =>
                                                updateDraft({ display: event.target.value })
                                            }
                                        />
                                    </Field>
                                    <Field
                                        id="author-review-sort"
                                        label={t('authorReview.sortName', 'Sort name')}
                                    >
                                        <Input
                                            id="author-review-sort"
                                            value={draft.sort}
                                            onChange={(event) =>
                                                updateDraft({ sort: event.target.value })
                                            }
                                        />
                                    </Field>
                                </div>
                                {/*
                                  The one kind control: the edit result and the
                                  classify action both send it, so the choice is
                                  made once and nothing repeats it.
                                */}
                                <div
                                    role="group"
                                    aria-label={t('authorReview.resultKind', 'Result kind')}
                                    className="flex flex-wrap gap-2"
                                >
                                    {KINDS.map((kind) => (
                                        <Button
                                            key={kind}
                                            variant={draft.kind === kind ? 'default' : 'outline'}
                                            size="sm"
                                            aria-pressed={draft.kind === kind}
                                            onClick={() => updateDraft({ kind })}
                                        >
                                            {t(`authorReview.kind.${kind}`, KIND_FALLBACKS[kind])}
                                        </Button>
                                    ))}
                                </div>
                                {classifyKind === null && (
                                    <p className="text-xs text-muted-foreground">
                                        {t(
                                            'authorReview.classifyKindHint',
                                            'Classification needs the Collective, Unknown or Malformed kind — pick one of those.',
                                        )}
                                    </p>
                                )}
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
                                {(['credit', 'fingerprint'] as const).map((scope) => (
                                    <Button
                                        key={scope}
                                        variant={scopeChoice === scope ? 'default' : 'outline'}
                                        size="sm"
                                        aria-pressed={scopeChoice === scope}
                                        onClick={() => setScopeChoice(scope)}
                                    >
                                        {t(
                                            `authorReview.scopeChoice.${scope}`,
                                            SCOPE_CHOICE_FALLBACKS[scope],
                                        )}
                                    </Button>
                                ))}
                            </div>
                            {/*
                              What each choice does, next to the choice: the
                              owner asked for the scope to explain itself,
                              including that "all credits" follows the source
                              name into books the library gains later.
                            */}
                            <dl className="flex flex-col gap-1 text-xs text-muted-foreground">
                                <div className="flex flex-col">
                                    <dt className="font-medium">
                                        {t('authorReview.scopeChoice.credit', 'Single credit')}
                                    </dt>
                                    <dd>
                                        {t(
                                            'authorReview.scopeHelp.credit',
                                            'Apply the decision to this single author credit only.',
                                        )}
                                    </dd>
                                </div>
                                <div className="flex flex-col">
                                    <dt className="font-medium">
                                        {t('authorReview.scopeChoice.fingerprint', 'All credits')}
                                    </dt>
                                    <dd>
                                        {t('authorReview.scopeHelp.fingerprint', {
                                            defaultValue:
                                                'Apply the decision to all {{count}} credits of the same source name, including books added to the library later.',
                                            count: detail.credits_count,
                                        })}
                                    </dd>
                                </div>
                            </dl>
                            {!scopeChosen && (
                                <p className="text-xs text-muted-foreground">
                                    {t(
                                        'authorReview.scopeDisabledHint',
                                        'Choose what the action applies to first — the action buttons stay disabled until then.',
                                    )}
                                </p>
                            )}
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
                                disabled={scopedActionDisabled || classifyKind === null}
                                onClick={() => {
                                    if (classifyKind === null || scopedActionDisabled) {
                                        return;
                                    }
                                    armScopedAction({ type: 'classify', kind: classifyKind });
                                }}
                            >
                                {t('authorReview.classify', 'Classify')}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                disabled={scopedActionDisabled}
                                onClick={() => armScopedAction({ type: 'unresolved' })}
                            >
                                {t('authorReview.unresolved', 'Mark unresolved')}
                            </Button>
                        </div>
                    </section>
                </>
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
        </section>
    );
};

/**
 * The detail as a screen of its own: the narrow layout's destination for
 * "View". The queue's filter and page travel in the address, so Back returns
 * to the same page; a deep-link or a reload of the route works standalone
 * and Back lands on the queue's defaults.
 */
const AuthorReviewDetailScreen: React.FC = () => {
    const { t } = useTranslation();
    const { id: rawID } = useParams();
    const [searchParams] = useSearchParams();
    const navigate = useNavigate();
    const itemID = parseRouteItemID(rawID);

    const back = () => {
        const params = queueSearchParams(
            statusFromParams(searchParams),
            searchParams.get(REVIEW_CURSOR_PARAM) ?? undefined,
            searchParams.getAll(REVIEW_HISTORY_PARAM),
        );
        params.set('tab', 'review');
        navigate(`/admin/author-normalization?${params.toString()}`);
    };

    return (
        <div className="flex flex-col gap-4">
            <div>
                <Button variant="outline" size="sm" onClick={back}>
                    {t('authorReview.backToQueue', 'Back to the queue')}
                </Button>
            </div>
            {itemID === null ? (
                <Card>
                    <CardContent>
                        <Alert variant="destructive">
                            <AlertDescription>
                                {t(
                                    'authorReview.invalidItemAddress',
                                    'This review item address is invalid.',
                                )}
                            </AlertDescription>
                        </Alert>
                    </CardContent>
                </Card>
            ) : (
                <Card>
                    <CardContent>
                        <AuthorReviewDetail key={itemID} itemID={itemID} />
                    </CardContent>
                </Card>
            )}
        </div>
    );
};

export default AuthorReviewDetailScreen;
