import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { previewClient, classifyPreviewError } from '@/api/preview';
import type { PreviewErrorKind, PreviewResponse } from '@/api/preview';
import { Button } from '@/shared/ui/button';
import { Skeleton } from '@/shared/ui/skeleton';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { cn } from '@/shared/lib/utils';

type FirstPhase = 'loading' | 'ready' | 'first-error';

interface ChunkFailure {
    index: number;
    kind: PreviewErrorKind;
}

interface BookPreviewDialogProps {
    open: boolean;
    bookId: number | null;
    /** BCP-47-ish language code from the book record, for the text column's `lang`. */
    bookLang?: string;
    onClose: () => void;
}

const ERROR_KEY: Record<PreviewErrorKind, string> = {
    notFound: 'previewErrorNotFound',
    permanent: 'previewErrorPermanent',
    gone: 'previewErrorGone',
    retryable: 'previewErrorRetryable',
    unknown: 'previewErrorUnknown',
};

/**
 * The text column's typography is the spec, not decoration: 62-character
 * measure, 18px body, 1.4 line-height. The class is built once so loading
 * and ready render against the same contract — the dialog does not reflow
 * when text arrives, because the column was always this wide.
 *
 * Paragraphs inside an FB2 portion take a red shift (first-line indent) with
 * no blank line between them, mirroring print. Justification is paired with
 * hyphenation so word spacing does not tear the right margin.
 */
const TEXT_COLUMN_CLASS = cn(
    'max-w-[62ch] flex-1 min-w-0',
    'text-[18px] leading-[1.4]',
    '[&_.portion_p]:indent-[1.5em] [&_.portion_p]:my-0',
    '[&_.portion]:text-justify [&_.portion]:[hyphens:auto]',
);

/**
 * The dialog's own width: never wider than 1180px, never wider than the
 * viewport minus its margins. The cap matters because the text column lives
 * inside it at 62ch and a wider shell would put slack on the side of the
 * measure that the reader's eye reads as wasted paper.
 */
const DIALOG_WIDTH_CLASS = cn(
    'w-[calc(100%-1rem)]',
    'sm:max-w-[1180px]',
);

/**
 * The book preview fetches one chunk at a time, and the chunks a reader has
 * already seen must stay on screen when the next one fails — losing them
 * means losing the reader's place, which is the one thing this dialog must
 * not do. The state below is shaped around that rule: portions accumulate
 * and are never cleared by a failure on a later index.
 *
 * Two AbortControllers live the lifetime of (a) the current preview and
 * (b) the current chunk. Closing the dialog or switching books cancels
 * both; the fetch handlers refuse to write state after an abort, so a
 * slow rejection that lands after close cannot reseed the dialog with a
 * book the reader already left.
 */
export default function BookPreviewDialog({
    open,
    bookId,
    bookLang,
    onClose,
}: BookPreviewDialogProps) {
    const { t } = useTranslation();

    const [preview, setPreview] = useState<PreviewResponse | null>(null);
    const [portions, setPortions] = useState<Map<number, string>>(new Map());
    const [currentIndex, setCurrentIndex] = useState(0);
    const [firstPhase, setFirstPhase] = useState<FirstPhase>('loading');
    const [firstErrorKind, setFirstErrorKind] = useState<PreviewErrorKind | null>(null);
    const [chunkFailure, setChunkFailure] = useState<ChunkFailure | null>(null);

    // `firstRetry` re-fires the preview effect when the reader asks to try
    // again after a retryable failure; `chunkRetry` does the same for an
    // individual chunk. Without these the effect would see the same deps and
    // never re-run, and Retry would be a dead button.
    const [firstRetry, setFirstRetry] = useState(0);
    const [chunkRetry, setChunkRetry] = useState(0);

    // Fetch the preview (and the first chunk that comes with it) on open,
    // book change, or first-retry. Any of those transitions aborts the prior
    // request — a stale revision's chunks must never land in the dialog.
    useEffect(() => {
        if (!open || bookId == null) return;
        const controller = new AbortController();

        setFirstPhase('loading');
        setPreview(null);
        setPortions(new Map());
        setCurrentIndex(0);
        setFirstErrorKind(null);
        setChunkFailure(null);

        previewClient.getPreview(bookId, controller.signal).then(
            (p) => {
                if (controller.signal.aborted) return;
                setPreview(p);
                setPortions(new Map([[0, p.first_chunk]]));
                setFirstPhase('ready');
            },
            (err) => {
                // AbortError is an intentional cancel: closing the dialog or
                // switching the book landed here. It is not a failure to show.
                if (controller.signal.aborted) return;
                if (err instanceof DOMException && err.name === 'AbortError') return;
                setFirstErrorKind(classifyPreviewError(err).kind);
                setFirstPhase('first-error');
            },
        );

        return () => controller.abort();
    }, [open, bookId, firstRetry]);

    // Whether the chunk effect needs to run. Deriving this from `portions`
    // rather than reading it through a ref keeps the rules-of-react happy and
    // still lets the effect skip without `portions` in its deps — the boolean
    // is the dependency, and it flips when portions does.
    const needsChunkFetch =
        open &&
        firstPhase === 'ready' &&
        preview != null &&
        currentIndex > 0 &&
        !portions.has(currentIndex);

    // Fetch subsequent chunks when navigation reaches them. The same abort
    // discipline applies: closing the dialog or moving past the chunk
    // cancels the request, and the handler ignores a rejection that arrives
    // after the controller has aborted.
    useEffect(() => {
        if (!needsChunkFetch || bookId == null || preview == null) return;

        const controller = new AbortController();
        // Clearing the prior chunk failure here is safe: it was about an
        // earlier index, and the reader is moving forward. We never clear
        // `portions` — see the contract above.
        setChunkFailure(null);

        previewClient
            .getChunk(bookId, currentIndex, preview.revision, controller.signal)
            .then(
                (res) => {
                    if (controller.signal.aborted) return;
                    setPortions((prev) => {
                        const next = new Map(prev);
                        next.set(currentIndex, res.chunk);
                        return next;
                    });
                },
                (err) => {
                    if (controller.signal.aborted) return;
                    if (err instanceof DOMException && err.name === 'AbortError') return;
                    setChunkFailure({ index: currentIndex, kind: classifyPreviewError(err).kind });
                },
            );

        return () => controller.abort();
    }, [needsChunkFetch, currentIndex, preview, bookId, chunkRetry]);

    const isFirstChunkLast =
        preview != null && currentIndex >= preview.chunk_count - 1;
    const awaitingChunk =
        firstPhase === 'ready' &&
        preview != null &&
        currentIndex > 0 &&
        !portions.has(currentIndex) &&
        chunkFailure?.index !== currentIndex;

    const goNext = () => {
        if (awaitingChunk) return;
        setChunkFailure(null);
        setCurrentIndex((i) => i + 1);
    };

    const retryFirst = () => setFirstRetry((n) => n + 1);
    const retryChunk = () => setChunkRetry((n) => n + 1);

    const orderedPortions = [...portions.entries()].sort(([a], [b]) => a - b);

    return (
        <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
            <DialogContent
                closeLabel={t('previewClose')}
                className={cn(
                    'flex max-h-[90vh] flex-col gap-0 p-0',
                    DIALOG_WIDTH_CLASS,
                )}
            >
                <DialogHeader className="border-b border-border px-6 py-4 pr-12">
                    <DialogTitle>{t('previewTitle')}</DialogTitle>
                    <DialogDescription className="sr-only">
                        {t('previewDescription')}
                    </DialogDescription>
                    {/*
                     * The notice lives in the header, not in the scroll area,
                     * so a reader sees it without scrolling past the book's
                     * text — which is the only place it would do them any
                     * good.
                     */}
                    <p
                        data-testid="preview-no-progress-notice"
                        className="mt-1 text-xs text-muted-foreground"
                    >
                        {t('previewNoProgressSaved')}
                    </p>
                </DialogHeader>

                <div
                    data-testid="preview-scroll-area"
                    className="flex min-h-0 flex-1 gap-6 overflow-y-auto px-6 py-4"
                >
                    {preview && preview.toc.length > 0 && (
                        <nav
                            data-testid="preview-toc"
                            aria-label={t('previewTocLabel')}
                            className="hidden w-56 shrink-0 sm:block"
                        >
                            <ul className="space-y-1">
                                {preview.toc.map((item, i) => (
                                    <li
                                        key={`${item.chunk}-${item.anchor}-${i}`}
                                        style={{
                                            paddingLeft: `${(item.depth - 1) * 1}rem`,
                                        }}
                                        className="text-sm"
                                    >
                                        {item.title}
                                    </li>
                                ))}
                            </ul>
                        </nav>
                    )}

                    <div
                        data-testid="preview-text-column"
                        lang={bookLang}
                        className={TEXT_COLUMN_CLASS}
                    >
                        {firstPhase === 'loading' && (
                            <div role="status" aria-live="polite" className="space-y-3">
                                <Skeleton className="h-4 w-full" />
                                <Skeleton className="h-4 w-full" />
                                <Skeleton className="h-4 w-3/4" />
                                <span className="sr-only">{t('previewLoading')}</span>
                            </div>
                        )}

                        {firstPhase === 'first-error' && firstErrorKind != null && (
                            <div role="alert" className="text-sm text-destructive">
                                <p>{t(ERROR_KEY[firstErrorKind])}</p>
                                {firstErrorKind === 'retryable' && (
                                    <Button
                                        type="button"
                                        variant="outline"
                                        size="sm"
                                        className="mt-3"
                                        onClick={retryFirst}
                                    >
                                        {t('previewRetry')}
                                    </Button>
                                )}
                            </div>
                        )}

                        {firstPhase === 'ready' && (
                            <>
                                {/*
                                 * Phase 2 invariant: every byte of this HTML
                                 * was built by our FB2 parser from a file a
                                 * librarian uploaded. No reader input reaches
                                 * this string — it is not a value the reader
                                 * can influence — so dangerouslySetInnerHTML
                                 * is the honest name for what is otherwise an
                                 * innerHTML assignment. Sanitising it would
                                 * imply there is something to filter, and
                                 * there is not.
                                 */}
                                {orderedPortions.map(([index, html]) => (
                                    <div
                                        key={index}
                                        data-testid={`preview-portion-${index}`}
                                        className="portion"
                                        dangerouslySetInnerHTML={{ __html: html }}
                                    />
                                ))}

                                {chunkFailure && chunkFailure.index === currentIndex && (
                                    <div
                                        role="alert"
                                        className="mt-4 text-sm text-destructive"
                                    >
                                        <p>{t(ERROR_KEY[chunkFailure.kind])}</p>
                                        {chunkFailure.kind === 'retryable' && (
                                            <Button
                                                type="button"
                                                variant="outline"
                                                size="sm"
                                                className="mt-3"
                                                onClick={retryChunk}
                                            >
                                                {t('previewRetry')}
                                            </Button>
                                        )}
                                    </div>
                                )}

                                {awaitingChunk && (
                                    <div
                                        role="status"
                                        aria-live="polite"
                                        className="mt-4 space-y-2"
                                    >
                                        <Skeleton className="h-4 w-full" />
                                        <Skeleton className="h-4 w-3/4" />
                                        <span className="sr-only">
                                            {t('previewLoading')}
                                        </span>
                                    </div>
                                )}
                            </>
                        )}
                    </div>
                </div>

                {firstPhase === 'ready' && (
                    <DialogFooter className="border-t border-border px-6 py-3">
                        {isFirstChunkLast ? (
                            <span
                                data-testid="preview-end-of-book"
                                className="text-sm text-muted-foreground"
                            >
                                {t('previewEndOfBook')}
                            </span>
                        ) : (
                            <Button
                                type="button"
                                variant="outline"
                                onClick={goNext}
                                disabled={awaitingChunk}
                            >
                                {t('previewNext')}
                            </Button>
                        )}
                    </DialogFooter>
                )}
            </DialogContent>
        </Dialog>
    );
}
