import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { previewClient, classifyPreviewError } from '@/api/preview';
import type { PreviewErrorKind, PreviewResponse, TocItem } from '@/api/preview';
import { Button } from '@/shared/ui/button';
import { Skeleton } from '@/shared/ui/skeleton';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { READER_TOC_QUERY } from '@/shared/layout/breakpoints';
import { cn } from '@/shared/lib/utils';
import { activeAnchorIndexFor, tocScrollTopFor } from '@/features/catalogue/tocScroll';

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
 * What the book itself looks like — headings, verse, quotations, footnotes —
 * is not here but in the stylesheet, under `.portion`. That markup comes from
 * the server, so it is styled by element and by its own class names; a dozen
 * such selectors written as arbitrary variants in this string would be
 * unreadable, and the first two written that way had already gone wrong
 * (every <p> took a paragraph indent, including lines of verse).
 */
export const TEXT_COLUMN_CLASS = cn(
    'max-w-[62ch] flex-1 min-w-0 mx-auto',
    'text-[18px] leading-[1.4]',
);

/**
 * The contents column beside the text on a wide layout.
 *
 * Sticky, and self-start with it: a flex child is stretched to the row's
 * height by default, which leaves sticky nothing to do — the box already
 * spans the whole scrolled length. Without this the column simply rides up
 * with the text, which is what it did. It was not a long-book problem:
 * scrolling the first chapter was enough to lose the contents entirely,
 * whatever the book.
 *
 * It also gets a scroll of its own, for when the book has more chapters than
 * the column is tall. `max-h-full` resolves against the work area's height,
 * which is the box sticky holds it inside.
 */
export const TOC_COLUMN_CLASS = cn(
    'w-56 shrink-0 py-4 pl-6',
    // Its own scroll, and it works because the column is a sibling of the
    // text's scroller rather than a passenger inside it. It was inside, held
    // by `sticky` with `max-h-full`, and that max-height resolves against the
    // containing block — which is as tall as the whole book. Measured on a
    // 300-chapter book: a 7196px column inside a 682px work area, no scroll
    // of its own, and every chapter past the first few unreachable.
    'overflow-y-auto',
);

/**
 * The row that holds the contents beside the text. It scrolls nothing itself
 * — that is what gives both children a height to be bounded by.
 */
const WORK_ROW_CLASS = cn('flex min-h-0 flex-1 gap-6');

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
    'min-h-0 flex-1 overflow-y-auto px-6 py-4',
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
 * The contents list itself — one component for the wide column and the
 * narrow panel, which used to be two hand-copied lists until they drifted:
 * the column had lost `aria-level` and carried its hierarchy by indentation
 * alone, which a screen reader cannot see. Indentation is presentation;
 * `aria-level` on the row is the meaning.
 *
 * `aria-current` is compared by list position, never by title: two chapters
 * may both be called "I", and only the index tells them apart when one of
 * them has to be marked.
 *
 * The one difference between the two homes is a parameter, not a branch.
 * Panel rows take `touchRows` for a 44px finger target — the panel is the
 * control a phone reader uses most, and its rows were measured at 20px
 * tall, under the 24 CSS pixels WCAG asks for — while the mouse-driven
 * column needs no such slack. Font size is shared rather than branched:
 * `text-sm` sits on the row and the button inherits it, so the computed
 * size is unchanged from when each list spelled the class out in its own
 * place.
 */
function TocList({
    entries,
    activeIndex,
    onSelect,
    touchRows = false,
}: {
    entries: TocItem[];
    activeIndex: number;
    onSelect: (item: TocItem) => void;
    /** Panel only: the row is a finger target, not a mouse target. */
    touchRows?: boolean;
}) {
    return (
        <ul className="space-y-1">
            {entries.map((item, i) => (
                <li
                    key={`${item.chunk}-${item.anchor}-${i}`}
                    aria-level={item.depth}
                    className="text-sm"
                >
                    <button
                        type="button"
                        aria-current={i === activeIndex ? 'page' : undefined}
                        onClick={() => onSelect(item)}
                        style={{ paddingInlineStart: `${(item.depth - 1) * 1 + 0.5}rem` }}
                        className={cn(
                            'w-full text-left hover:underline',
                            // The rail is always there and usually invisible:
                            // colouring a transparent border rather than adding
                            // one keeps the text from shifting sideways as the
                            // reader moves from chapter to chapter.
                            'border-l-2 border-transparent',
                            touchRows && 'flex min-h-11 items-center',
                            i === activeIndex && 'border-l-primary bg-accent font-medium',
                        )}
                    >
                        {item.title}
                    </button>
                </li>
            ))}
        </ul>
    );
}

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
    /**
     * How far the book has been asked for, and where the loaded run begins.
     *
     * These used to be one number called `currentIndex`, which meant both
     * "the portion the reader is on" and "the portion to fetch". Continuous
     * scrolling separates them: the reader's place is now read off the
     * scroll (see `measuredActive`), while `frontier` is only ever the
     * highest portion requested.
     *
     * `runStart` exists because a contents entry can jump past portions
     * nobody fetched. Portions render stacked in index order, so a jump from
     * chapter 2 to chapter 6 used to leave 0, 1, 5 adjacent in one column —
     * the reader saw chapter 2 run straight into chapter 6 with nothing
     * saying three chapters were missing. A jump now starts a fresh run at
     * the chapter asked for, and the run stays contiguous from there.
     */
    const [frontier, setFrontier] = useState(0);
    const [runStart, setRunStart] = useState(0);

    /**
     * The contents entry the reader is under, measured from the text rather
     * than inferred from the loader: scrolling into chapter 3 marks chapter 3
     * without anything being clicked. -1 means above the first heading.
     */
    const [measuredActive, setMeasuredActive] = useState(-1);
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
    // place survives a glance at the contents.
    const [tocPanelOpen, setTocPanelOpen] = useState(false);

    /**
     * Where the reader asked to be taken, queued until the portion is in the
     * document. Only a contents entry queues anything now — reading on scrolls
     * nowhere by design — and `anchor` is the heading that entry named. The
     * portion's own top is the fallback for when the book does not contain
     * the anchor it promised.
     */
    const [pendingScroll, setPendingScroll] = useState<{
        index: number;
        anchor: string | null;
    } | null>(null);

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

    // The contents column, so the marked chapter can be kept in view inside
    // its own scroll.
    const tocColumnRef = useRef<HTMLElement>(null);

    // The element that asks for the next portion by coming into view.
    const loadMoreRef = useRef<HTMLDivElement>(null);

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
        setFrontier(0);
        setRunStart(0);
        setMeasuredActive(-1);
        setFirstErrorKind(null);
        setChunkFailure(null);
        // A new book means the TOC below belongs to a different text: any
        // open panel and any queued destination are stale.
        setTocPanelOpen(false);
        setPendingScroll(null);

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
        frontier > 0 &&
        !portions.has(frontier);

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
            .getChunk(bookId, frontier, preview.revision, controller.signal)
            .then(
                (res) => {
                    if (generation !== chunkGeneration.current) return;
                    if (controller.signal.aborted) return;
                    setPortions((prev) => {
                        const next = new Map(prev);
                        next.set(frontier, res.chunk);
                        return next;
                    });
                },
                (err) => {
                    if (generation !== chunkGeneration.current) return;
                    if (controller.signal.aborted) return;
                    if (err instanceof DOMException && err.name === 'AbortError') return;
                    setChunkFailure({ index: frontier, kind: classifyPreviewError(err).kind });
                },
            );

        return () => controller.abort();
    }, [needsChunkFetch, frontier, preview, bookId, chunkRetry]);

    // The frontier is the last portion of the book: there is nothing further
    // to fetch. It was called `isFirstChunkLast` when the first chunk was the
    // only one a fresh dialog had.
    const frontierIsLast =
        preview != null && frontier >= preview.chunk_count - 1;
    const awaitingChunk =
        firstPhase === 'ready' &&
        preview != null &&
        frontier > 0 &&
        !portions.has(frontier) &&
        chunkFailure?.index !== frontier;

    /**
     * Open the chapter a contents entry names.
     *
     * Already loaded — the reader is moving inside the run they are reading —
     * and nothing is refetched: the anchor scroll below does the work. Not
     * loaded, and the run restarts at that chapter, because portions render
     * stacked in index order and a jump would otherwise splice chapter 6
     * onto the end of chapter 2 with nothing to say what is missing.
     */
    const selectTocItem = (item: { chunk: number; anchor: string }) => {
        setPendingScroll({ index: item.chunk, anchor: item.anchor });
        if (!portions.has(item.chunk)) {
            // The opening portion is never fetched — it arrives inside the
            // preview — so a run that starts at 0 has to be seeded from what
            // is already in hand. Without this, jumping deep into the book
            // and then back to chapter 1 left the reader with an empty page
            // that could never fill: the loader refuses index 0, and nothing
            // else puts the first portion back.
            const seeded = new Map<number, string>();
            if (item.chunk === 0 && preview != null) {
                seeded.set(0, preview.first_chunk);
            }
            setPortions(seeded);
            setMeasuredActive(-1);
            setRunStart(item.chunk);
            setFrontier(item.chunk);
            setChunkFailure(null);
            // Ask again even when the frontier is unchanged. Returning to a
            // chapter whose fetch had failed cleared the error and showed the
            // skeleton, but no request followed: the effect's inputs were all
            // exactly as they had been. This is the input that says "again".
            setChunkRetry((n) => n + 1);
        }
        setTocPanelOpen(false);
    };

    const retryFirst = () => setFirstRetry((n) => n + 1);
    const retryChunk = () => setChunkRetry((n) => n + 1);

    const orderedPortions = [...portions.entries()].sort(([a], [b]) => a - b);

    // Memoised because the anchor observer depends on it: `preview?.toc ?? []`
    // is a new array on every render, which would tear the observer down and
    // build it again each time anything at all changed.
    const tocEntries = useMemo(() => preview?.toc ?? [], [preview]);

    /**
     * The contents entry the reader is under, read off the scroll rather than
     * off the loader.
     *
     * It used to be derived from the portion index, so it only ever moved
     * when a button was pressed or an entry was clicked: a reader who simply
     * scrolled into chapter 3 was still shown chapter 1 as current. Now it is
     * the last anchor they have scrolled past, which is right in both of the
     * awkward cases at once — a chapter cut across several portions keeps its
     * own heading marked all the way through, and a portion holding several
     * chapters marks whichever of them the reader has actually reached.
     *
     * Before anything has been scrolled past, the answer is the first entry
     * of the loaded run: at the top of the book that is chapter 1, and after
     * a jump it is the chapter jumped to.
     *
     * It is an index and not the entry itself because titles repeat — two
     * chapters called "I" are two entries, and only the index tells them
     * apart when one of them has to be marked.
     */
    const activeTocIndex =
        measuredActive !== -1
            ? measuredActive
            : tocEntries.findIndex((item) => item.chunk >= runStart);
    const activeTocItem = activeTocIndex === -1 ? null : tocEntries[activeTocIndex];

    /**
     * Whether there is more book to fetch and nothing in the way of fetching
     * it. The sentinel below is rendered only then, which is what keeps the
     * loading rule structural: it cannot fire while a portion is in flight,
     * because there is nothing to fire on.
     */
    const canLoadMore =
        firstPhase === 'ready' &&
        preview != null &&
        frontier < preview.chunk_count - 1 &&
        // The portion at the frontier is in hand: not still coming, and not
        // failed. Both of those are the same condition — a fetch that has
        // not landed leaves nothing in the map — and spelling them out
        // separately as `!awaitingChunk && chunkFailure == null` added two
        // clauses that could not fail on their own. Mutation testing found
        // them: removing the failure clause changed no test, because it
        // never decided anything.
        portions.has(frontier);

    // Reading is continuous: the next portion is fetched as the reader nears
    // the end of the loaded text, and there is no button. The sentinel sits
    // below the text and the margin fetches a screenful early, so the join
    // arrives before the reader does.
    useEffect(() => {
        const area = scrollAreaRef.current;
        const sentinel = loadMoreRef.current;
        if (!area || !sentinel) return;

        const observer = new IntersectionObserver(
            (entries) => {
                if (!entries.some((entry) => entry.isIntersecting)) return;
                setFrontier((f) => f + 1);
            },
            { root: area, rootMargin: '0px 0px 800px 0px' },
        );
        observer.observe(sentinel);
        return () => observer.disconnect();
    }, [canLoadMore, frontier]);

    /**
     * Which chapter the reader is under, re-measured as they scroll.
     *
     * This was an IntersectionObserver over the anchors, and the browser threw
     * it out: dragging the scrollbar from the end of the book to the start
     * moves an anchor from above the viewport to below it without crossing
     * any edge, so no event arrives and the highlight keeps a chapter the
     * reader left thousands of pixels ago. Measured in Chrome, it sat on
     * chapter 7 all the way back to the top. The jsdom test passed
     * throughout — the crossings it fed the component were the ones the
     * component expected, which is no test at all.
     *
     * Positions cannot drift that way. The anchors live in HTML the server
     * rendered and React does not own, so they are looked up afresh on each
     * measurement rather than held from a previous render.
     */
    useEffect(() => {
        const area = scrollAreaRef.current;
        if (!area || tocEntries.length === 0) return;

        // Looked up once per set of portions, not once per frame. A book of
        // several hundred chapters with a few portions loaded means most of
        // these lookups find nothing, and repeating them on every scroll
        // frame is hundreds of fruitless walks of the DOM per second.
        const anchors = tocEntries.map((item) =>
            area.querySelector(`[id="${CSS.escape(item.anchor)}"]`),
        );

        let queued = 0;
        const measure = () => {
            queued = 0;
            const edge = area.getBoundingClientRect().top;
            const tops = anchors.map((anchor) =>
                // An anchor from a portion that is not loaded cannot have
                // been passed; a number below the edge says exactly that.
                anchor ? anchor.getBoundingClientRect().top : Number.POSITIVE_INFINITY,
            );
            setMeasuredActive(activeAnchorIndexFor(tops, edge));
        };

        const remeasure = () => {
            // One measurement per frame however fast the wheel turns.
            if (queued) return;
            queued = requestAnimationFrame(measure);
        };

        measure();
        area.addEventListener('scroll', remeasure, { passive: true });
        // Positions move without anyone scrolling: the window is resized, a
        // picture finishes loading, the layout switches between the column
        // and the panel. Measuring only on scroll leaves the highlight
        // pointing at wherever the text used to be.
        window.addEventListener('resize', remeasure);
        return () => {
            area.removeEventListener('scroll', remeasure);
            window.removeEventListener('resize', remeasure);
            if (queued) cancelAnimationFrame(queued);
        };
    }, [tocEntries, portions, isWide]);

    /**
     * Keep the marked chapter visible inside the contents column.
     *
     * Where to scroll to is decided by `tocScrollTopFor`, which is a plain
     * function so the arithmetic can be tested; this effect is only the
     * wiring. `scrollIntoView` is deliberately not used: it scrolls every
     * scrollable ancestor, and the nearest one here is the reading area — so
     * bringing a contents entry into view would yank the book out from under
     * the reader.
     */
    useEffect(() => {
        const column = tocColumnRef.current;
        if (!column) return;
        const marked = column.querySelector<HTMLElement>('[aria-current="page"]');
        if (!marked) return;

        const top = tocScrollTopFor({
            // Offsets inside the column, measured against the column. The
            // first version subtracted `column.offsetTop` from this, which
            // reads as a sensible "make it relative" until you notice that
            // the column is sticky and therefore is itself the offset parent
            // of everything in it — so the entry's offset was already
            // relative and the subtraction took the column's own place in the
            // book off it. Measured in Chrome at the last chapter: -38724,
            // clamped to 0, and the column never moved.
            top: marked.offsetTop,
            height: marked.offsetHeight,
            viewTop: column.scrollTop,
            viewHeight: column.clientHeight,
        });
        if (top !== null) column.scrollTop = top;
        // isWide as well as the marked entry: the column does not exist on a
        // narrow layout, so it is mounted fresh — scrolled to the top — when
        // the reader widens the window, and would sit there showing chapter 1
        // until they happened to cross into another chapter.
    }, [activeTocIndex, isWide]);

    // Whether the portion the reader is waiting for has settled in DOM.
    // Scrolling runs only then: a portion still in flight has nothing to
    // scroll to.
    const pendingPortionReady = pendingScroll != null && portions.has(pendingScroll.index);

    useEffect(() => {
        if (pendingScroll == null) return;
        if (!pendingPortionReady) return;

        const area = scrollAreaRef.current;
        const {index, anchor} = pendingScroll;
        // The queue is state and not a ref on purpose, at the price of the
        // setState-in-effect this file already carries twice. A ref would
        // leave this effect depending on the portion alone, and a book with
        // several chapters inside one chunk has entries that change the
        // anchor without changing the portion: tapping one of those would
        // queue a scroll nothing ever re-runs to perform.
        // Clear before aiming: if something below throws, the queue is
        // still empty next time, which is better than re-running forever.
        setPendingScroll(null);
        if (!area) return;

        // The anchor is searched only inside the portion that named it. An
        // FB2 chunk may legitimately share its id space with another chunk's
        // (the server guarantees uniqueness within a chunk, not across
        // them), so a global getElementById could match a portion the
        // reader is not looking at.
        const portionEl = area.querySelector(
            `[data-testid="preview-portion-${index}"]`,
        );
        const anchorEl = anchor == null ? null : document.getElementById(anchor);
        if (anchorEl && portionEl && portionEl.contains(anchorEl)) {
            anchorEl.scrollIntoView({ block: 'start' });
        } else if (portionEl) {
            // The anchor the server promised is missing from the chunk: a
            // spec violation in the data, and not a reason to throw or to
            // leave the reader looking at the page they were already on.
            portionEl.scrollIntoView({ block: 'start' });
        }
    }, [pendingScroll, pendingPortionReady]);

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

                    <div className={WORK_ROW_CLASS}>
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
                                ref={tocColumnRef}
                                data-testid="preview-toc"
                                aria-label={t('previewTocLabel')}
                                className={TOC_COLUMN_CLASS}
                            >
                                <TocList
                                    entries={preview.toc}
                                    activeIndex={activeTocIndex}
                                    onSelect={selectTocItem}
                                />
                            </nav>
                        )}

                        {/*
                          * The text scrolls; the contents column beside it does
                          * not ride along. The ref stays on this element, so
                          * everything measured against "the scroll area" — the
                          * anchors, the sentinel's root, the reader's place —
                          * still means the text the reader is reading.
                          */}
                        <div
                            ref={scrollAreaRef}
                            data-testid="preview-scroll-area"
                            className={SCROLL_AREA_CLASS}
                        >
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

                                    {chunkFailure && chunkFailure.index === frontier && (
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

                                    {/*
                                     * The foot of the text says one of three
                                     * things, and it has to say one of them.
                                     * With no button left, a fetch that
                                     * failed silently would read as the end
                                     * of the book, and a book that ended
                                     * would read as a fetch still coming.
                                     * The failure and the spinner are above;
                                     * this is the other two.
                                     */}
                                    {canLoadMore && (
                                        <div
                                            ref={loadMoreRef}
                                            data-testid="preview-load-more"
                                            aria-hidden="true"
                                            className="h-px"
                                        />
                                    )}

                                    {frontierIsLast && portions.has(frontier) && (
                                        <p
                                            data-testid="preview-end-of-book"
                                            className="mt-8 border-t border-border pt-4 text-center text-sm text-muted-foreground"
                                        >
                                            {t('previewEndOfBook')}
                                        </p>
                                    )}
                                </>
                            )}
                            </div>
                        </div>
                    </div>

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
                                <TocList
                                    entries={preview.toc}
                                    activeIndex={activeTocIndex}
                                    onSelect={selectTocItem}
                                    touchRows
                                />
                            </nav>
                        </div>
                    )}
                </div>
            </DialogContent>
        </Dialog>
    );
}
