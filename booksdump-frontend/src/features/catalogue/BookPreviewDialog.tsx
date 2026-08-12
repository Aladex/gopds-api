import React, { useEffect, useRef, useState } from 'react';
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
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { READER_TOC_QUERY } from '@/shared/layout/breakpoints';
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

/**
 * The shape of a note's chunk-local anchor, as the server spells it in both
 * the link's href and the note's id: pv<chunk>-note-<key>. Module scope, not
 * the component body: a regex rebuilt every render is a new dependency every
 * render for the effect that uses it.
 */
const NOTE_ANCHOR_PATTERN = /^pv\d+-note-/;

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
export const TEXT_COLUMN_CLASS = cn(
    'max-w-[62ch] flex-1 min-w-0 mx-auto',
    'text-[18px] leading-[1.4]',
    '[&_.portion_p]:indent-[1.5em] [&_.portion_p]:my-0',
    '[&_.portion]:text-justify [&_.portion]:[hyphens:auto]',
);

/**
 * The scrolling work area.
 *
 * The gutter is reserved on both edges rather than left to the scrollbar.
 * Without it the bar takes its width from one side only, and a column that
 * is centred in what remains is no longer centred on the page — measured on
 * the mockup as 24px of margin on one side against 39 on the other. Reserving
 * it on both edges costs the same few pixels whether the content scrolls or
 * not, which is also what stops the text shifting sideways the moment a
 * portion makes the page long enough to need the bar.
 */
export const SCROLL_AREA_CLASS = cn(
    'flex min-h-0 flex-1 gap-6 overflow-y-auto px-6 py-4',
    '[scrollbar-gutter:stable_both-edges]',
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
 * One portion of the book, injected as HTML.
 *
 * The DOM inside a portion is the only place the reader's own state lives —
 * which footnotes they opened, where they scrolled — because React does not
 * own those nodes. Written inline in the parent, this element had its HTML
 * re-written on unrelated re-renders, and re-writing identical HTML still
 * destroys the children and builds new ones: a glance at the table of
 * contents closed every note the reader had opened, and the anchor queue's
 * extra render tore a portion out from under a test that had just found it.
 *
 * Measured, not assumed: putting the element in its own component is what
 * stops the rewrite — moving it back inline fails three tests, while
 * dropping the memo below fails none today. The memo stays because it is the
 * part that *guarantees* the property rather than inheriting it from how
 * React currently reconciles: with props unchanged there is no render at
 * all, so there is nothing to reason about.
 */
const Portion = React.memo(function Portion({ index, html }: { index: number; html: string }) {
    return (
        <div
            data-testid={`preview-portion-${index}`}
            className="portion"
            dangerouslySetInnerHTML={{ __html: html }}
        />
    );
});

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
 *
 * Cancellation alone cannot say "this answer belongs to a request the
 * reader still wants", because a server is free to answer a request whose
 * client has already walked away. So every request is also stamped with a
 * generation number, and an answer is applied only while its stamp is the
 * latest one. The portion index cannot play that role: leaving chunk 3
 * and coming back to it makes a dead request's index match again, while
 * its generation stays behind.
 *
 * The preview channel and the chunk channel carry a counter each: on a
 * book switch the chunk effect fires once more against the not-yet-reset
 * state (a pre-existing extra request, aborted a render later), and a
 * shared counter would let that stray run invalidate the new book's
 * in-flight preview.
 */
export default function BookPreviewDialog({
    open,
    bookId,
    bookLang,
    onClose,
}: BookPreviewDialogProps) {
    const { t } = useTranslation();
    // The one width question this file asks, and it is the reader's own, not
    // the card's. Above it the contents sit in a column beside the text;
    // below, the same data drives a panel over the work area — because a
    // hundred-item list does not fit a phone, and because the column takes
    // its width out of the text's measure. See READER_TOC_QUERY for what
    // was measured.
    const isWide = useMediaQuery(READER_TOC_QUERY);

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

    // The narrow TOC panel. Open state is local; closing it without a
    // selection touches nothing else, which is exactly why the reader's
    // place survives a glance at the contents. `pendingAnchor` is the
    // anchor a TOC entry named; it stays pending across the chunk fetch
    // and clears once the scroll has been aimed.
    const [tocPanelOpen, setTocPanelOpen] = useState(false);
    const [pendingAnchor, setPendingAnchor] = useState<string | null>(null);

    // Monotone stamps of the most recent request on each channel. A response
    // handler applies its payload only while its captured stamp still equals
    // the ref — anything older belongs to a request the reader has left:
    // a portion they navigated away from, or a book they switched away from.
    const previewGeneration = useRef(0);
    const chunkGeneration = useRef(0);

    // The scroll container is needed by the anchor-scroll effect; jsdom does
    // not implement layout, but it does keep scrollTop as a stored property
    // and provides scrollIntoView as a spy-able noop, which is what the
    // panel's preservation and anchor tests rely on.
    const scrollAreaRef = useRef<HTMLDivElement>(null);

    // The text column is also the delegation root for footnote links. The
    // portion HTML arrives via dangerouslySetInnerHTML — React does not own
    // those nodes, so the expansion state lives as attributes on the nodes
    // themselves and one click handler on this stable parent serves every
    // portion that arrives later, without re-attaching per portion.
    const textColumnRef = useRef<HTMLDivElement>(null);

    // Fetch the preview (and the first chunk that comes with it) on open,
    // book change, or first-retry. Any of those transitions aborts the prior
    // request — a stale revision's chunks must never land in the dialog.
    useEffect(() => {
        if (!open || bookId == null) return;
        const controller = new AbortController();
        // Stamping the request makes its answer recognizable as ours even if
        // the server responds to a request we already cancelled.
        const generation = ++previewGeneration.current;

        setFirstPhase('loading');
        setPreview(null);
        setPortions(new Map());
        setCurrentIndex(0);
        setFirstErrorKind(null);
        setChunkFailure(null);
        // A new book means the TOC below belongs to a different text: any
        // open panel and any pending anchor are stale.
        setTocPanelOpen(false);
        setPendingAnchor(null);

        previewClient.getPreview(bookId, controller.signal).then(
            (p) => {
                if (generation !== previewGeneration.current) return;
                if (controller.signal.aborted) return;
                setPreview(p);
                setPortions(new Map([[0, p.first_chunk]]));
                setFirstPhase('ready');
            },
            (err) => {
                // AbortError is an intentional cancel: closing the dialog or
                // switching the book landed here. It is not a failure to show.
                if (generation !== previewGeneration.current) return;
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
        // Same stamp discipline as the preview request: the reader may jump
        // to another portion — and back to this one — before the answer
        // arrives, and only the latest request for an index may write it.
        const generation = ++chunkGeneration.current;
        // Clearing the prior chunk failure here is safe: it was about an
        // earlier index, and the reader is moving forward. We never clear
        // `portions` — see the contract above.
        setChunkFailure(null);

        previewClient
            .getChunk(bookId, currentIndex, preview.revision, controller.signal)
            .then(
                (res) => {
                    if (generation !== chunkGeneration.current) return;
                    if (controller.signal.aborted) return;
                    setPortions((prev) => {
                        const next = new Map(prev);
                        next.set(currentIndex, res.chunk);
                        return next;
                    });
                },
                (err) => {
                    if (generation !== chunkGeneration.current) return;
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

    // A TOC entry opens the portion it names. A failure pinned to another
    // index is left behind; one pinned to this index stays, so the reader
    // still sees why the portion is missing and can retry it.
    const goToChunk = (index: number) => {
        setChunkFailure((prev) => (prev != null && prev.index !== index ? null : prev));
        setCurrentIndex(index);
    };

    const retryFirst = () => setFirstRetry((n) => n + 1);
    const retryChunk = () => setChunkRetry((n) => n + 1);

    const orderedPortions = [...portions.entries()].sort(([a], [b]) => a - b);

    /**
     * The TOC entry the reader is currently under — not the last one they
     * tapped. Reaching chunk 1 through "Next" makes Chapter 2 active without
     * a click.
     *
     * A portion does not always open a chapter, and that is the case this
     * has to get right. The server cuts a long chapter across several
     * portions and lists only real headings, so portions 1..n of one chapter
     * name nothing at all: matching on the portion index alone left the
     * trigger reading "Contents" and no entry marked, in the middle of a
     * chapter the reader was plainly inside. The answer there is the heading
     * that opened the chapter, which is the last one before this portion.
     *
     * The other direction is a portion holding several headings. Then the
     * one that opens it is the honest answer on arrival: the reader is at
     * the top of the portion, and we do not track where they scrolled to.
     * It is an index and not the entry itself because titles repeat — two
     * chapters called "I" are two entries, and only the index tells them
     * apart when one of them has to be marked.
     */
    const tocEntries = preview?.toc ?? [];
    const opensThisPortion = tocEntries.findIndex((item) => item.chunk === currentIndex);
    const activeTocIndex =
        opensThisPortion !== -1
            ? opensThisPortion
            : tocEntries.reduce(
                  (found, item, i) => (item.chunk < currentIndex ? i : found),
                  -1,
              );
    const activeTocItem = activeTocIndex === -1 ? null : tocEntries[activeTocIndex];

    const selectTocItem = (item: { chunk: number; anchor: string }) => {
        // Queue the anchor first: the scroll effect waits for the portion to
        // arrive and clears the queue when it has aimed the scroll.
        setPendingAnchor(item.anchor);
        goToChunk(item.chunk);
        setTocPanelOpen(false);
    };

    // Whether the current portion has settled in DOM. Anchoring runs only
    // then: a portion still in flight has no DOM for an anchor to live in.
    const currentPortionReady = portions.has(currentIndex);

    useEffect(() => {
        if (pendingAnchor == null) return;
        if (!currentPortionReady) return;

        const area = scrollAreaRef.current;
        const anchor = pendingAnchor;
        // The queue is state and not a ref on purpose, at the price of the
        // setState-in-effect this file already carries twice. A ref would
        // leave this effect depending on the portion alone, and a book with
        // several chapters inside one chunk has entries that change the
        // anchor without changing the portion: tapping one of those would
        // queue a scroll nothing ever re-runs to perform.
        // Clear before aiming: if something below throws, the queue is
        // still empty next time, which is better than re-running forever.
        setPendingAnchor(null);
        if (!area) return;

        // The anchor is searched only inside the current portion. An FB2
        // chunk may legitimately share its id space with another chunk's
        // (the server guarantees uniqueness within a chunk, not across
        // them), so a global getElementById could match a portion the
        // reader is not looking at.
        const portionEl = area.querySelector(
            `[data-testid="preview-portion-${currentIndex}"]`,
        );
        const anchorEl = document.getElementById(anchor);
        if (anchorEl && portionEl && portionEl.contains(anchorEl)) {
            anchorEl.scrollIntoView({ block: 'start' });
        } else if (portionEl) {
            // The anchor the server promised is missing from the chunk — a
            // spec violation in the data, but not a reason to throw or to
            // leave the reader looking at the wrong place. The portion's
            // top is the closest honest fallback.
            portionEl.scrollIntoView({ block: 'start' });
        }
    }, [pendingAnchor, currentPortionReady, currentIndex]);

    // Initialise every footnote the server ships: collapse the body and
    // mark the link as expanded=false. The data attribute distinguishes
    // "fresh from the server" from "explicitly opened by the reader", so a
    // portion that arrives later does not re-hide a note the reader has
    // already opened, and opening one note never touches another.
    useEffect(() => {
        const root = textColumnRef.current;
        if (!root) return;
        root.querySelectorAll<HTMLDivElement>(
            '.preview-note:not([data-preview-note-init])',
        ).forEach((note) => {
            note.setAttribute('hidden', '');
            note.setAttribute('data-preview-note-init', '');
        });
        root.querySelectorAll<HTMLAnchorElement>('a[href^="#pv"]').forEach((link) => {
            if (link.hasAttribute('aria-expanded')) return;
            const id = (link.getAttribute('href') ?? '').slice(1);
            if (!NOTE_ANCHOR_PATTERN.test(id)) return;
            link.setAttribute('aria-expanded', 'false');
        });
    }, [portions]);

    // One delegated handler serves every portion that ever lands in the
    // column, including the ones fetched later: the click on a note link
    // bubbles out of the injected HTML to this element, which React owns.
    //
    // It is React's onClick and not an addEventListener in an effect, and
    // that is the whole point. The effect version attached the listener to
    // whatever node the ref held when it ran; the dialog's content is
    // mounted through Radix's presence machinery, which re-creates that
    // node, and the listener stayed behind on the detached one. Every note
    // was collapsed correctly and no click ever opened one — the collapsing
    // effect re-ran on each portion and saw the live node, while the
    // listener's effect had no reason to run again.
    const onTextColumnClick = (event: React.MouseEvent<HTMLDivElement>) => {
        const target = event.target;
        if (!(target instanceof Element)) return;
        const link = target.closest('a');
        if (!link) return;
        const href = link.getAttribute('href') ?? '';
        const id = href.startsWith('#') ? href.slice(1) : '';
        if (!NOTE_ANCHOR_PATTERN.test(id)) return;
        const note = event.currentTarget.querySelector(`[id="${CSS.escape(id)}"]`);
        if (!note) return;
        // Without this the browser hands the fragment to the SPA router: the
        // address changes under the reader and the scroll jumps away.
        event.preventDefault();
        const expanding = note.hasAttribute('hidden');
        if (expanding) {
            note.removeAttribute('hidden');
        } else {
            note.setAttribute('hidden', '');
        }
        // Every link that points at this note, not only the one pressed. A
        // note cited twice in a portion is rendered once and linked twice by
        // design, so setting the state on the pressed link alone left the
        // other one announcing the opposite of what was on the screen.
        event.currentTarget
            .querySelectorAll<HTMLAnchorElement>('a[href^="#pv"]')
            .forEach((candidate) => {
                if ((candidate.getAttribute('href') ?? '').slice(1) !== id) return;
                candidate.setAttribute('aria-expanded', String(expanding));
            });
    };

    return (
        <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
            <DialogContent
                closeLabel={t('previewClose')}
                onEscapeKeyDown={(event) => {
                    // Escape while the TOC panel is open returns the reader
                    // to the text rather than dismissing the dialog. The X
                    // button still closes unconditionally: it lives in the
                    // header, which the panel never covers, and its click
                    // routes through onOpenChange, not this keydown.
                    if (tocPanelOpen) {
                        event.preventDefault();
                        setTocPanelOpen(false);
                    }
                }}
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

                {/*
                 * The work area is wrapped in a relative container so the
                 * narrow TOC panel can overlay exactly this region — the
                 * scroll area plus the footer — without ever covering the
                 * dialog's header or its close button.
                 */}
                <div className="relative flex min-h-0 flex-1 flex-col">
                    {/*
                     * The narrow TOC trigger sits at the top of the work
                     * area as its own row. It opens a panel rather than a
                     * dropdown because a book can carry hundreds of entries
                     * and a list laid over the text is unreadable; the
                     * panel takes the full work area and scrolls itself.
                     */}
                    {!isWide && preview && preview.toc.length > 0 && !tocPanelOpen && (
                        <button
                            type="button"
                            data-testid="preview-toc-trigger"
                            aria-label={t('previewOpenToc')}
                            onClick={() => setTocPanelOpen(true)}
                            className="flex items-center gap-2 border-b border-border px-4 py-2 text-left text-sm hover:bg-accent"
                        >
                            <span className="text-xs text-muted-foreground">
                                {t('previewContents')}
                            </span>
                            <span className="truncate">
                                {activeTocItem?.title ?? t('previewContents')}
                            </span>
                        </button>
                    )}

                    <div
                        ref={scrollAreaRef}
                        data-testid="preview-scroll-area"
                        className={SCROLL_AREA_CLASS}
                    >
                        {/*
                         * Wide layout renders the TOC as a column beside the
                         * text; the narrow layout renders the trigger above
                         * and the panel over the work area, never the
                         * column. Which shape appears is decided once, in
                         * React, by READER_TOC_QUERY. The column carries no
                         * responsive class of its own: a `sm:block` beside
                         * this `isWide` is a second threshold that agrees
                         * only while both literals happen to say 40rem, and
                         * a band where the column and the panel are both
                         * gone leaves the reader no contents at all.
                         */}
                        {isWide && preview && preview.toc.length > 0 && (
                            <nav
                                data-testid="preview-toc"
                                aria-label={t('previewTocLabel')}
                                className="w-56 shrink-0"
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
                                            <button
                                                type="button"
                                                aria-current={
                                                    i === activeTocIndex ? 'page' : undefined
                                                }
                                                className="w-full text-left hover:underline"
                                                onClick={() => selectTocItem(item)}
                                            >
                                                {item.title}
                                            </button>
                                        </li>
                                    ))}
                                </ul>
                            </nav>
                        )}

                        <div
                            ref={textColumnRef}
                            onClick={onTextColumnClick}
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
                                        <Portion key={index} index={index} html={html} />
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

                    {/*
                     * The narrow TOC panel overlays the work area only. Its
                     * Back button returns to the text without changing
                     * anything: the same portion stays mounted, the scroll
                     * area is not touched, so the reader's place is exactly
                     * where they left it.
                     */}
                    {!isWide && tocPanelOpen && preview && preview.toc.length > 0 && (
                        <div
                            data-testid="preview-toc-panel"
                            className="absolute inset-0 z-10 flex flex-col bg-popover"
                        >
                            <div className="flex items-center gap-2 border-b border-border px-3 py-2">
                                <button
                                    type="button"
                                    aria-label={t('previewCloseToc')}
                                    onClick={() => setTocPanelOpen(false)}
                                    className="flex min-h-11 items-center text-sm text-muted-foreground hover:underline"
                                >
                                    {t('previewCloseToc')}
                                </button>
                                <h2 className="text-sm font-medium">
                                    {t('previewTocLabel')}
                                </h2>
                            </div>
                            <nav
                                aria-label={t('previewTocLabel')}
                                className="flex-1 overflow-y-auto px-3 py-2"
                            >
                                <ul className="space-y-1">
                                    {preview.toc.map((item, i) => (
                                        <li
                                            key={`${item.chunk}-${item.anchor}-${i}`}
                                            aria-level={item.depth}
                                        >
                                            <button
                                                type="button"
                                                aria-current={
                                                    i === activeTocIndex ? 'page' : undefined
                                                }
                                                onClick={() => selectTocItem(item)}
                                                style={{
                                                    paddingLeft: `${(item.depth - 1) * 1}rem`,
                                                }}
                                                // A finger's target, not a
                                                // mouse's: measured at 20px
                                                // tall on a phone, under the
                                                // 24 CSS pixels WCAG asks
                                                // for, and these rows are
                                                // the control a reader uses
                                                // most in the panel.
                                                className="flex min-h-11 w-full items-center text-left text-sm hover:underline"
                                            >
                                                {item.title}
                                            </button>
                                        </li>
                                    ))}
                                </ul>
                            </nav>
                        </div>
                    )}
                </div>
            </DialogContent>
        </Dialog>
    );
}
