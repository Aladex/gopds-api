import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
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
import { cn } from '@/shared/lib/utils';
import * as adminApi from '@/api/admin';
import type {
    AuthorReviewDetail as AuthorReviewDetailData,
    AuthorReviewEditResult,
    AuthorReviewKind,
    AuthorReviewOrder,
    AuthorReviewProposal,
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
export const REVIEW_DETAIL_ROUTE = '/admin/book-scanning/authors';

/** The scanning section's tab the review queue lives in (?tab=authors). */
export const AUTHORS_TAB = 'authors';

/** The queue's list state, kept in the address so Back can restore it. */
export const REVIEW_STATUS_PARAM = 'status';
export const REVIEW_CURSOR_PARAM = 'cursor';
export const REVIEW_HISTORY_PARAM = 'h';

/** The queue's sort order; the default stays out of the address. */
export const REVIEW_ORDER_PARAM = 'order';
export const DEFAULT_REVIEW_ORDER: AuthorReviewOrder = 'credits';

export const queueSearchParams = (
    status: 'open' | 'closed',
    cursor: string | undefined,
    history: string[],
    order: AuthorReviewOrder = DEFAULT_REVIEW_ORDER,
): URLSearchParams => {
    const params = new URLSearchParams();
    params.set(REVIEW_STATUS_PARAM, status);
    if (order !== DEFAULT_REVIEW_ORDER) {
        params.set(REVIEW_ORDER_PARAM, order);
    }
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

/** Anything but an explicit `created` is the default, heaviest-first order. */
export const orderFromParams = (params: URLSearchParams): AuthorReviewOrder =>
    params.get(REVIEW_ORDER_PARAM) === 'created' ? 'created' : DEFAULT_REVIEW_ORDER;

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
export type ScopedActionBody =
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
export const isUsableId = (id: number): boolean => Number.isSafeInteger(id) && id > 0;

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

/** The only scope the scanning section's review decides at. */
export const FINGERPRINT_SCOPE = 'fingerprint' as const;

/** Fallbacks for the dynamic status/scope/kind keys; the locales carry the real strings. */
export const KIND_FALLBACKS: Record<AuthorReviewKind, string> = {
    person: 'Person',
    collective: 'Collective',
    unknown: 'Unknown',
    malformed: 'Malformed',
};

const KINDS: AuthorReviewKind[] = ['person', 'collective', 'unknown', 'malformed'];

/**
 * A labelled figure; the value sits in its own node so each number is one
 * text. A changed value is marked, so the eye finds what the proposal moved.
 */
const Stat: React.FC<{ label: React.ReactNode; value: React.ReactNode; changed?: boolean }> = ({
    label,
    value,
    changed = false,
}) => (
    <div className="flex items-baseline justify-between gap-4 text-sm">
        <span className="shrink-0 text-muted-foreground">{label}</span>
        <span
            data-changed={changed ? 'true' : undefined}
            className={cn(
                'min-w-0 text-right [overflow-wrap:anywhere]',
                changed && '-mx-1 rounded bg-amber-500/15 px-1',
            )}
        >
            {value}
        </span>
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

export type ReviewItemPhase = 'idle' | 'loading' | 'ready' | 'failed';

/** Everything one review item's views need: its data, its draft, its decision flow. */
export interface ReviewItem {
    itemID: number | null;
    detail: AuthorReviewDetailData | null;
    phase: ReviewItemPhase;
    errorText: string | null;
    draft: ReviewEditDraft | null;
    updateDraft: (patch: Partial<ReviewEditDraft>) => void;
    editOpen: boolean;
    setEditOpen: React.Dispatch<React.SetStateAction<boolean>>;
    formErrorText: string | null;
    pendingAction: PendingAction | null;
    /** Opens the confirmation dialog for a decision; false when it cannot be armed now. */
    arm: (action: ScopedActionBody) => boolean;
    confirm: () => void;
    cancel: () => void;
    /** Arms the correction form's decision: a corrected name, or a kind. */
    saveCorrection: () => void;
    busy: boolean;
    notice: string | null;
    actionErrorText: string | null;
    /** True while the item cannot be decided: loading, failed, a request in flight. */
    actionDisabled: boolean;
}

export interface ReviewItemOptions {
    /** Called once the server confirmed a decision on the item. */
    onDecided?: (itemID: number, action: ScopedActionBody['type']) => void;
    /**
     * Whether the item hands the keyboard focus back to the control that
     * armed the dialog (default). The queue moves the focus itself.
     */
    restoreFocus?: boolean;
}

/**
 * One review item's state, for the modal or route detail and for the queue's
 * inline evidence alike: loads the item, keeps the correction draft, and runs
 * a decision through the confirmation dialog. A null id loads nothing. When
 * the id changes, everything restarts for the new item, and a late answer
 * that belongs to the previous one installs nothing.
 */
export function useReviewItem(itemID: number | null, options: ReviewItemOptions = {}): ReviewItem {
    const { t } = useTranslation();

    /** The loaded detail, tagged with the id it was read for. */
    const [loaded, setLoaded] = useState<{
        forID: number;
        detail: AuthorReviewDetailData;
    } | null>(null);
    const [detailPhase, setDetailPhase] = useState<ReviewItemPhase>('idle');
    const [detailErrorText, setDetailErrorText] = useState<string | null>(null);

    const [draft, setDraft] = useState<ReviewEditDraft | null>(null);
    const [editOpen, setEditOpen] = useState(false);
    const [formErrorText, setFormErrorText] = useState<string | null>(null);
    const [pendingAction, setPendingAction] = useState<PendingAction | null>(null);
    const [actionErrorText, setActionErrorText] = useState<string | null>(null);
    const [notice, setNotice] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);

    const onDecidedRef = useRef(options.onDecided);
    useEffect(() => {
        onDecidedRef.current = options.onDecided;
    });

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
     * Loads the item. `resetDraft` says whether the correction form restarts
     * from the server's proposal — a conflict refresh keeps what was typed.
     */
    const fetchDetail = useCallback(
        async (resetDraft: boolean) => {
            const generation = ++detailGeneration.current;
            if (itemID === null || !isUsableId(itemID)) {
                setDetailPhase('idle');
                return;
            }
            setDetailPhase('loading');
            setDetailErrorText(null);
            try {
                const data = await adminApi.getAuthorReviewItem(itemID);
                if (generation !== detailGeneration.current) {
                    return;
                }
                setLoaded({ forID: itemID, detail: data.item });
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
        setFormErrorText(null);
        setActionErrorText(null);
        setNotice(null);
        setEditOpen(false);
        setPendingAction(null);
        fetchDetail(true);
    }, [fetchDetail]);

    // A detail left over from the previous id is never this item's.
    const current = loaded !== null && loaded.forID === itemID ? loaded.detail : null;
    const actionDisabled = busy || detailPhase !== 'ready' || current === null;

    const arm = (action: ScopedActionBody): boolean => {
        if (actionDisabled || itemID === null || current === null) {
            return false;
        }
        if (current.scope !== FINGERPRINT_SCOPE) {
            return false;
        }
        if (action.type === 'accept' && current.proposal === null) {
            return false;
        }
        setActionErrorText(null);
        setNotice(null);
        dialogOpenerRef.current =
            document.activeElement instanceof HTMLElement ? document.activeElement : null;
        setPendingAction({ ...action, itemID });
        return true;
    };

    /** A person is a corrected name; the other kinds classify the source as it is. */
    const saveCorrection = () => {
        if (draft === null || actionDisabled) {
            return;
        }
        if (draft.kind !== 'person') {
            setFormErrorText(null);
            arm({ type: 'classify', kind: draft.kind });
            return;
        }
        const built = buildEditResult(draft);
        if (built.result === undefined) {
            setFormErrorText(t('authorReview.editDisplayRequired', 'Display name is required.'));
            return;
        }
        setFormErrorText(null);
        arm({ type: 'edit', result: built.result });
    };

    const cancel = () => {
        setPendingAction(null);
    };

    // Focus is handed back after the commit, not inside the close handler:
    // the dialog's focus machinery runs while unmounting its content and
    // lands on <body>, overwriting anything focused earlier. Confirm also
    // disables the opener for the duration of the request, so a target that
    // cannot take focus yet is kept and restored once the request settles.
    const restoreFocus = options.restoreFocus ?? true;
    useEffect(() => {
        if (!restoreFocus || pendingAction !== null || busy) {
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
    }, [restoreFocus, pendingAction, busy]);

    const confirm = () => {
        if (pendingAction === null || current === null || itemID === null) {
            return;
        }
        // The confirmed target must still be the item the dialog was armed for.
        if (pendingAction.itemID !== itemID) {
            setPendingAction(null);
            return;
        }
        // The tab decides at fingerprint scope only.
        if (current.scope !== FINGERPRINT_SCOPE) {
            setPendingAction(null);
            return;
        }
        const target = itemID;
        const scope = FINGERPRINT_SCOPE;
        const action: ScopedActionBody = pendingAction;
        const generation = detailGeneration.current;
        setPendingAction(null);
        setBusy(true);
        setActionErrorText(null);
        setNotice(null);
        const request =
            action.type === 'accept'
                ? adminApi.acceptAuthorReviewItem(target, { scope })
                : action.type === 'unresolved'
                  ? adminApi.markAuthorReviewUnresolved(target, { scope })
                  : action.type === 'classify'
                    ? adminApi.classifyAuthorReviewItem(target, { scope, kind: action.kind })
                    : adminApi.editAuthorReviewItem(target, { scope, result: action.result });
        request
            .then((data) => {
                if (!mountedRef.current) {
                    return;
                }
                // The server decided the item, whatever this view moved on to.
                onDecidedRef.current?.(target, action.type);
                if (generation !== detailGeneration.current) {
                    return;
                }
                setLoaded({ forID: target, detail: data.item });
                setEditOpen(false);
                setNotice(t('authorReview.decided', 'Saved.'));
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

    const updateDraft = useCallback((patch: Partial<ReviewEditDraft>) => {
        setDraft((value) => (value === null ? value : { ...value, ...patch }));
    }, []);

    return {
        itemID,
        detail: current,
        phase: detailPhase,
        errorText: detailErrorText,
        draft,
        updateDraft,
        editOpen,
        setEditOpen,
        formErrorText,
        pendingAction,
        arm,
        confirm,
        cancel,
        saveCorrection,
        busy,
        notice,
        actionErrorText,
        actionDisabled,
    };
}

/** The proposal's name parts, each beside the file's part it replaces. */
const PROPOSAL_PARTS = [
    ['given_name', 'first', 'authorReview.givenName', 'Given name'],
    ['additional_names', 'middle', 'authorReview.additionalNames', 'Additional names'],
    ['family_name', 'last', 'authorReview.familyName', 'Family name'],
    ['nickname', 'nickname', 'authorReview.nickname', 'Nickname'],
] as const;

/**
 * What an operator decides from: the name as the file has it, the proposal
 * with the fields it changes marked, and the books the name appears in. The
 * detail screen and the queue's focused row show the same evidence; the
 * inline one also counts the books and says what a decision covers.
 */
export const ReviewEvidence: React.FC<{ detail: AuthorReviewDetailData; inline?: boolean }> = ({
    detail,
    inline = false,
}) => {
    const { t } = useTranslation();
    const ids = useId();
    const proposal = detail.proposal;
    const more = detail.credits_count - detail.linked_books.length;
    return (
        <>
            <div
                className={cn('grid gap-3', inline ? 'min-[900px]:grid-cols-3' : 'md:grid-cols-2')}
            >
                <section
                    aria-labelledby={`${ids}-source`}
                    className="flex min-w-0 flex-col gap-2 rounded-lg border border-border bg-card p-3"
                >
                    <h4 id={`${ids}-source`} className="text-sm font-medium">
                        {t('authorReview.source', 'In the file')}
                    </h4>
                    <Stat
                        label={t('authorReview.sourceDisplay', 'As written')}
                        value={detail.source.display}
                    />
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
                </section>

                <section
                    aria-labelledby={`${ids}-proposal`}
                    className="flex min-w-0 flex-col gap-2 rounded-lg border border-border bg-card p-3"
                >
                    <h4 id={`${ids}-proposal`} className="text-sm font-medium">
                        {t('authorReview.proposal', 'Proposed')}
                    </h4>
                    {proposal === null ? (
                        <p className="text-sm text-muted-foreground">
                            {t('authorReview.noProposal', 'No proposal yet.')}
                        </p>
                    ) : (
                        <>
                            <Stat
                                label={t('authorReview.displayName', 'Display name')}
                                value={proposal.display_name}
                                changed={proposal.display_name !== detail.source.display}
                            />
                            {PROPOSAL_PARTS.map(([part, sourcePart, key, fallback]) =>
                                // Only the parts the proposal moves are worth a line.
                                (proposal[part] ?? null) !== (detail.source[sourcePart] ?? null) ? (
                                    <Stat
                                        key={part}
                                        label={t(key, fallback)}
                                        value={proposal[part] ?? '—'}
                                        changed
                                    />
                                ) : null,
                            )}
                            <Stat
                                label={t('authorReview.sortName', 'Sort name')}
                                value={proposal.sort_name ?? '—'}
                            />
                            <Stat
                                label={t('authorReview.proposalKind', 'Kind')}
                                value={t(
                                    `authorReview.kind.${proposal.kind}`,
                                    KIND_FALLBACKS[proposal.kind],
                                )}
                            />
                        </>
                    )}
                </section>

                {inline && (
                    <section
                        aria-labelledby={`${ids}-books`}
                        className="flex min-w-0 flex-col gap-1 rounded-lg border border-border bg-card p-3"
                    >
                        <h4 id={`${ids}-books`} className="text-sm font-medium">
                            {t('authorReview.linkedBooksCount', {
                                defaultValue: 'Books · {{count}}',
                                count: detail.credits_count,
                            })}
                        </h4>
                        <BookList detail={detail} />
                        {more > 0 && (
                            <p className="text-sm text-muted-foreground">
                                {t('authorReview.booksMore', {
                                    defaultValue: '{{count}} more',
                                    count: more,
                                })}
                            </p>
                        )}
                        <p className="mt-1 text-xs text-muted-foreground">
                            {t('authorReview.scopeLine', {
                                defaultValue:
                                    'The decision covers all {{count}} credits with this name',
                                count: detail.credits_count,
                            })}
                        </p>
                    </section>
                )}
            </div>

            {!inline && (
                <section aria-labelledby={`${ids}-books`} className="flex flex-col gap-1">
                    <h4 id={`${ids}-books`} className="text-sm font-medium">
                        {t('authorReview.linkedBooks', 'Books')}
                    </h4>
                    <BookList detail={detail} />
                </section>
            )}
        </>
    );
};

const BookList: React.FC<{ detail: AuthorReviewDetailData }> = ({ detail }) => {
    const { t } = useTranslation();
    return detail.linked_books.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('authorReview.noLinkedBooks', 'None.')}</p>
    ) : (
        <ul className="ml-4 list-disc text-sm break-words">
            {detail.linked_books.map((book) => (
                <li key={book.id}>{book.title}</li>
            ))}
        </ul>
    );
};

/**
 * The correction: what the name is, and for a person its corrected parts.
 * Saving arms the same confirmation dialog as the other decisions. Inline in
 * the queue it takes focus on its first field and can be put away again.
 */
export const ReviewCorrectionForm: React.FC<{
    item: ReviewItem;
    autoFocus?: boolean;
    onCancel?: () => void;
}> = ({ item, autoFocus = false, onCancel }) => {
    const { t } = useTranslation();
    const ids = useId();
    const formRef = useRef<HTMLElement | null>(null);
    const { draft, updateDraft, formErrorText, actionDisabled } = item;

    useEffect(() => {
        if (!autoFocus) {
            return;
        }
        // The first name field, or the kind choice when the kind has none.
        const form = formRef.current;
        (
            form?.querySelector<HTMLElement>('input') ?? form?.querySelector<HTMLElement>('button')
        )?.focus();
    }, [autoFocus]);

    if (draft === null) {
        return null;
    }

    const nameField = (key: keyof Omit<ReviewEditDraft, 'kind'>, label: string) => {
        const id = `${ids}-${key}`;
        return (
            <Field
                id={id}
                label={label}
                error={key === 'display' ? (formErrorText ?? undefined) : undefined}
            >
                <Input
                    id={id}
                    value={draft[key]}
                    onChange={(event) => updateDraft({ [key]: event.target.value })}
                />
            </Field>
        );
    };

    return (
        <section
            ref={formRef}
            aria-labelledby={`${ids}-heading`}
            className="flex flex-col gap-3 rounded-lg border border-border bg-card p-3"
            onKeyDown={(event) => {
                if (onCancel !== undefined && event.key === 'Escape') {
                    event.stopPropagation();
                    onCancel();
                }
            }}
        >
            <h4 id={`${ids}-heading`} className="text-sm font-medium">
                {t('authorReview.correctTitle', 'Correction')}
            </h4>
            <div
                role="group"
                aria-label={t('authorReview.resultKind', 'What this is')}
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
            {draft.kind === 'person' && (
                <div className="grid gap-3 sm:grid-cols-2">
                    {nameField('given', t('authorReview.givenName', 'Given name'))}
                    {nameField('additional', t('authorReview.additionalNames', 'Additional names'))}
                    {nameField('family', t('authorReview.familyName', 'Family name'))}
                    {nameField('nickname', t('authorReview.nickname', 'Nickname'))}
                    {nameField('display', t('authorReview.displayName', 'Display name'))}
                    {nameField('sort', t('authorReview.sortName', 'Sort name'))}
                </div>
            )}
            {draft.kind !== 'person' && (
                <p className="text-xs text-muted-foreground">
                    {t(
                        'authorReview.kindWithoutName',
                        'Not a person: the name stays as in the file and is marked with this kind.',
                    )}
                </p>
            )}
            <div className="flex flex-wrap gap-2">
                <Button size="sm" disabled={actionDisabled} onClick={item.saveCorrection}>
                    {t('authorReview.saveCorrection', 'Save correction')}
                </Button>
                {onCancel !== undefined && (
                    <Button variant="ghost" size="sm" onClick={onCancel}>
                        {t('cancel', 'Cancel')}
                    </Button>
                )}
            </div>
        </section>
    );
};

/**
 * The confirmation every decision passes through. Its confirm button takes
 * the focus, so a decision from the keyboard is the key, then Enter.
 */
export const ReviewConfirmDialog: React.FC<{
    item: ReviewItem;
    /** Radix's focus hand-back as the dialog's content leaves the page. */
    onCloseAutoFocus?: (event: Event) => void;
}> = ({ item, onCloseAutoFocus }) => {
    const { t } = useTranslation();
    const confirmRef = useRef<HTMLButtonElement | null>(null);
    const { detail, pendingAction } = item;
    return (
        <Dialog open={pendingAction !== null} onOpenChange={(open) => !open && item.cancel()}>
            <DialogContent
                closeLabel={t('close', 'Close')}
                onCloseAutoFocus={onCloseAutoFocus}
                onOpenAutoFocus={(event) => {
                    event.preventDefault();
                    confirmRef.current?.focus();
                }}
            >
                <DialogHeader>
                    <DialogTitle>
                        {t('authorReview.confirmTitle', 'Confirm the decision')}
                    </DialogTitle>
                    <DialogDescription>
                        {detail !== null &&
                        pendingAction !== null &&
                        detail.id === pendingAction.itemID
                            ? t('authorReview.confirmFingerprint', {
                                  defaultValue:
                                      'The decision applies to all {{count}} credits with this name in the file, books added later included.',
                                  count: detail.credits_count,
                              })
                            : null}
                    </DialogDescription>
                </DialogHeader>
                <DialogFooter>
                    <Button variant="ghost" onClick={item.cancel}>
                        {t('cancel', 'Cancel')}
                    </Button>
                    <Button ref={confirmRef} onClick={item.confirm}>
                        {t('authorReview.confirm', 'Confirm')}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};

/**
 * One review item, as an operator needs it: the name as the file has it, the
 * proposed name, the books it appears in, and three decisions — accept the
 * proposal, correct it, or leave the name as the file has it. A decision
 * always applies to every credit with the source name, later books included
 * (fingerprint scope); there is no scope to choose. A legacy credit-scoped
 * item is shown read-only. The kinds other than a person are a field of the
 * correction form.
 */
export const AuthorReviewDetail: React.FC<{
    itemID: number;
    onDecided?: ReviewItemOptions['onDecided'];
}> = ({ itemID, onDecided }) => {
    const { t } = useTranslation();
    const item = useReviewItem(itemID, { onDecided });
    const { detail, phase, draft, editOpen, actionDisabled } = item;

    return (
        <section
            aria-labelledby="author-review-detail-heading"
            className="flex min-w-0 flex-col gap-3"
        >
            {/* Room for the modal's close control in the top-right corner. */}
            <div className="flex flex-wrap items-center gap-2 pr-12">
                <h3 id="author-review-detail-heading" className="text-base font-medium">
                    {t('authorReview.detailTitle', 'Review item')}
                </h3>
            </div>

            {phase === 'loading' && (
                <p className="text-sm text-muted-foreground">{t('loading', 'Loading...')}</p>
            )}
            {item.errorText && (
                <Alert variant="destructive">
                    <AlertDescription>{item.errorText}</AlertDescription>
                </Alert>
            )}
            {item.actionErrorText && (
                <Alert variant="destructive">
                    <AlertDescription>{item.actionErrorText}</AlertDescription>
                </Alert>
            )}
            {item.notice && (
                <p role="status" className="text-sm text-muted-foreground">
                    {item.notice}
                </p>
            )}

            {detail !== null && phase === 'ready' && (
                <>
                    <div className="flex flex-col gap-1">
                        <p className="text-base font-medium break-words">{detail.display_name}</p>
                        <p className="text-sm text-muted-foreground">
                            {t('authorReview.creditsLine', {
                                defaultValue: 'Appears in {{count}} credits',
                                count: detail.credits_count,
                            })}
                        </p>
                    </div>

                    <ReviewEvidence detail={detail} />

                    {detail.scope !== FINGERPRINT_SCOPE && (
                        <p role="note" className="text-sm text-muted-foreground">
                            {t(
                                'authorReview.creditReadOnly',
                                'This item concerns a single book, not every book with this name. It cannot be decided here.',
                            )}
                        </p>
                    )}

                    {detail.scope === FINGERPRINT_SCOPE && (
                        <div className="flex flex-wrap gap-2">
                            <Button
                                size="sm"
                                disabled={actionDisabled || detail.proposal === null}
                                onClick={() => item.arm({ type: 'accept' })}
                            >
                                {t('authorReview.accept', 'Accept')}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                disabled={actionDisabled || draft === null}
                                aria-expanded={editOpen}
                                onClick={() => item.setEditOpen((open) => !open)}
                            >
                                {t('authorReview.correct', 'Correct')}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                disabled={actionDisabled}
                                onClick={() => item.arm({ type: 'unresolved' })}
                            >
                                {t('authorReview.keepAsInFile', 'Keep as in the file')}
                            </Button>
                        </div>
                    )}

                    {detail.scope === FINGERPRINT_SCOPE && editOpen && (
                        <ReviewCorrectionForm item={item} />
                    )}
                </>
            )}

            <ReviewConfirmDialog item={item} />
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
            orderFromParams(searchParams),
        );
        params.set('tab', AUTHORS_TAB);
        navigate(`/admin/book-scanning?${params.toString()}`);
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
