import React from 'react';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import BookPreviewDialog from '@/features/catalogue/BookPreviewDialog';
import * as previewApi from '@/api/preview';
import { ApiError } from '@/api/errors';
import type { ChunkResponse, PreviewResponse } from '@/api/preview';

// The dialog's job is to ferry a reader through a book one chunk at a time
// without losing their place when something fails: the first request may
// refuse, the next chunk may refuse, the reader may close the tab mid-fetch,
// or the book may simply end. Each of those is a different state, and each
// state is asserted here on its real DOM rather than on the mock's call log —
// except where the call itself is the contract (no second request for the
// first chunk, an AbortSignal that actually aborts).

vi.mock('@/api/preview', async () => {
    const actual = await vi.importActual<typeof import('@/api/preview')>('@/api/preview');
    return {
        ...actual,
        previewClient: {
            getPreview: vi.fn(),
            getChunk: vi.fn(),
            getImage: vi.fn(),
        },
    };
});

// Identity translation: every test searches by the same key the component
// renders, so the strings stay draft and the assertions stay honest.
const translate = (key: string) => key;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translate }) }));

// The dialog's TOC panel is the narrow-layout counterpart of the wide TOC
// column, and the switch between them is the one width question this file is
// allowed to ask — through CARD_WIDE_QUERY, like every other component. jsdom
// has no viewport so useMediaQuery would always report false (narrow); the
// existing 25 tests assume the wide column is rendered, so the default here
// is wide. Narrow-panel tests flip `matches.current` to false in their own
// beforeEach.
const matches = { current: true };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => matches.current,
    default: () => matches.current,
}));

const getPreview = vi.mocked(previewApi.previewClient.getPreview);
const getChunk = vi.mocked(previewApi.previewClient.getChunk);

function makePreview(over: Partial<PreviewResponse> = {}): PreviewResponse {
    return {
        revision: 'rev-1',
        chunk_count: 1,
        toc: [{ title: 'Chapter 1', depth: 1, chunk: 0, anchor: 'c1' }],
        images: [],
        first_chunk: '<p data-testid="first-portion">first content</p>',
        ...over,
    };
}

/**
 * Deferred lets a test hold a request open until it chooses to resolve it,
 * which is what testing the loading state actually requires: an assertion
 * made while the promise is pending proves the skeleton was drawn for the
 * right reason, not because React happened to render before the microtask.
 */
function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (error: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return { promise, resolve, reject };
}

/**
 * The double must honour the AbortSignal the way the browser's fetch does:
 * reject with AbortError when the controller aborts. A double that ignores
 * the signal would only prove the double's behaviour, not the component's —
 * the contract being tested is that the component actually calls .abort(),
 * and the only honest witness is a signal whose .aborted flips to true in
 * response.
 *
 * The producer still resolves on its own, so tests that don't care about
 * cancellation just see a normal value. The signal is the last positional
 * arg for every previewClient method (bookId[, chunk, revision], signal),
 * so we read it off the back rather than declaring each signature.
 */
function signalAware<T>(producer: () => T | Promise<T>) {
    return (...args: unknown[]): Promise<T> => {
        const signal = args[args.length - 1];
        return new Promise<T>((resolve, reject) => {
            const onAbort = () => reject(new DOMException('Aborted', 'AbortError'));
            if (signal instanceof AbortSignal) {
                if (signal.aborted) {
                    onAbort();
                    return;
                }
                signal.addEventListener('abort', onAbort, { once: true });
            }
            Promise.resolve(producer()).then(resolve, reject);
        });
    };
}

function renderDialog(props: Partial<React.ComponentProps<typeof BookPreviewDialog>> = {}) {
    const onClose = vi.fn();
    const view = render(
        <BookPreviewDialog
            open
            bookId={12}
            bookLang="ru"
            onClose={onClose}
            {...props}
        />,
    );
    return { ...view, onClose };
}

beforeEach(() => {
    getPreview.mockReset();
    getChunk.mockReset();
    matches.current = true;
});

describe('BookPreviewDialog — states', () => {
    it('1. loading: skeleton occupies the same 62ch column the text will fill', async () => {
        const d = deferred<PreviewResponse>();
        getPreview.mockImplementation((_id, signal) => {
            signal?.addEventListener(
                'abort',
                () => d.reject(new DOMException('Aborted', 'AbortError')),
                { once: true },
            );
            return d.promise;
        });

        renderDialog();

        const loadingColumn = await screen.findByTestId('preview-text-column');
        const loadingClass = loadingColumn.className;
        expect(loadingClass).toContain('max-w-[62ch]');
        expect(within(loadingColumn).getByRole('status')).toBeInTheDocument();

        d.resolve(makePreview());

        await waitFor(() =>
            expect(screen.queryByRole('status')).not.toBeInTheDocument(),
        );
        // Same className before and after: the dialog does not reflow when the
        // text arrives. jsdom has no layout, so the class is the contract.
        expect(screen.getByTestId('preview-text-column').className).toBe(loadingClass);
    });

    it('2. ready: first chunk comes from the preview, with no second request', async () => {
        getPreview.mockImplementation(signalAware(() => makePreview()));

        renderDialog();

        expect(await screen.findByTestId('first-portion')).toBeInTheDocument();
        expect(getChunk).not.toHaveBeenCalled();
        // The TOC is shown alongside the first chunk — same render, no extra fetch.
        expect(screen.getByTestId('preview-toc')).toBeInTheDocument();
    });

    it('3a. first-request permanent failure: empty state, no retry offered', async () => {
        getPreview.mockImplementation(signalAware(() => Promise.reject(new ApiError('bad', 400))));

        renderDialog();

        expect(await screen.findByText('previewErrorPermanent')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'previewRetry' })).not.toBeInTheDocument();
        // No portion is rendered either — there is nothing to show.
        expect(screen.queryByTestId('preview-portion-0')).not.toBeInTheDocument();
    });

    it('3b. first-request retryable failure: retry offered, retry refetches', async () => {
        getPreview.mockImplementation(signalAware(() => Promise.reject(new ApiError('boom', 0))));

        renderDialog();

        const retry = await screen.findByRole('button', { name: 'previewRetry' });
        expect(screen.getByText('previewErrorRetryable')).toBeInTheDocument();

        getPreview.mockImplementation(signalAware(() => makePreview()));
        await userEvent.click(retry);

        expect(await screen.findByTestId('first-portion')).toBeInTheDocument();
        expect(getPreview).toHaveBeenCalledTimes(2);
    });

    it('4. next-chunk failure: previously shown text stays on screen', async () => {
        getPreview.mockImplementation(
            signalAware(() =>
                makePreview({
                    chunk_count: 2,
                    first_chunk: '<p data-testid="first-portion">first content here</p>',
                }),
            ),
        );
        getChunk.mockImplementation(signalAware(() => Promise.reject(new ApiError('boom', 0))));

        renderDialog();

        await screen.findByTestId('first-portion');
        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));

        // The critical assertion: the prior portion is still in the document
        // after the next one refused to load.
        expect(await screen.findByText('previewErrorRetryable')).toBeInTheDocument();
        expect(screen.getByTestId('first-portion')).toBeInTheDocument();
    });

    it('5. end of book: no Next button, an end-of-book plaque instead', async () => {
        getPreview.mockImplementation(signalAware(() => makePreview({ chunk_count: 1 })));

        renderDialog();

        await screen.findByTestId('first-portion');
        expect(screen.queryByRole('button', { name: 'previewNext' })).not.toBeInTheDocument();
        expect(screen.getByTestId('preview-end-of-book')).toBeInTheDocument();
    });

    it('6. no TOC: the TOC column is not rendered at all', async () => {
        getPreview.mockImplementation(signalAware(() => makePreview({ toc: [] })));

        renderDialog();

        await screen.findByTestId('first-portion');
        // Not "rendered empty": the node is absent.
        expect(screen.queryByTestId('preview-toc')).not.toBeInTheDocument();
    });
});

describe('BookPreviewDialog — layout contract', () => {
    it('marks the text column with the book language and the agreed typography', async () => {
        getPreview.mockImplementation(signalAware(() => makePreview()));

        renderDialog({ bookLang: 'ru' });

        const column = await screen.findByTestId('preview-text-column');
        expect(column).toHaveAttribute('lang', 'ru');
        expect(column.className).toContain('max-w-[62ch]');
        expect(column.className).toContain('text-[18px]');
        expect(column.className).toContain('leading-[1.4]');
    });

    it('shows the no-progress notice outside the scrolling text region', async () => {
        getPreview.mockImplementation(signalAware(() => makePreview()));

        renderDialog();

        const notice = await screen.findByTestId('preview-no-progress-notice');
        const scrollArea = screen.getByTestId('preview-scroll-area');
        // The notice must be visible without scrolling past the book's text,
        // which means it lives in the dialog chrome, not in the scroll area.
        expect(scrollArea.contains(notice)).toBe(false);
    });
});

describe('BookPreviewDialog — cancellation', () => {
    it('aborts the in-flight preview request when the dialog closes', async () => {
        let captured: AbortSignal | undefined;
        getPreview.mockImplementation((_id, signal) => {
            captured = signal;
            return new Promise<PreviewResponse>(() => {});
        });

        const view = renderDialog();

        await waitFor(() => expect(captured).toBeDefined());
        expect(captured!.aborted).toBe(false);

        view.rerender(
            <BookPreviewDialog open={false} bookId={12} bookLang="ru" onClose={vi.fn()} />,
        );

        await waitFor(() => expect(captured!.aborted).toBe(true));
    });

    it('aborts the in-flight preview request when the book changes', async () => {
        // Capture every signal the mock sees — on a book change the effect
        // re-runs and re-calls getPreview, so a single `captured` would be
        // overwritten by the new (non-aborted) signal before the assertion.
        const signals: AbortSignal[] = [];
        getPreview.mockImplementation((_id, signal) => {
            if (signal) signals.push(signal);
            return new Promise<PreviewResponse>(() => {});
        });

        const view = renderDialog({ bookId: 12 });

        await waitFor(() => expect(signals.length).toBe(1));
        expect(signals[0].aborted).toBe(false);

        view.rerender(
            <BookPreviewDialog open bookId={13} bookLang="ru" onClose={vi.fn()} />,
        );

        // The new request fires for the new book; the previous request's
        // signal must have flipped to aborted in between.
        await waitFor(() => expect(signals.length).toBe(2));
        expect(signals[0].aborted).toBe(true);
        expect(signals[1].aborted).toBe(false);
    });

    it('aborts the in-flight chunk request when the dialog closes', async () => {
        getPreview.mockImplementation(
            signalAware(() => makePreview({ chunk_count: 5 })),
        );
        let captured: AbortSignal | undefined;
        getChunk.mockImplementation((_id, _n, _rev, signal) => {
            captured = signal;
            return new Promise(() => {});
        });

        const view = renderDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));
        await waitFor(() => expect(captured).toBeDefined());
        expect(captured!.aborted).toBe(false);

        view.rerender(
            <BookPreviewDialog open={false} bookId={12} bookLang="ru" onClose={vi.fn()} />,
        );

        await waitFor(() => expect(captured!.aborted).toBe(true));
    });
});

describe('BookPreviewDialog — mutation guards', () => {
    // Each guard names a specific change that would silently regress the
    // contract if it slipped through. They are explicit and pointed — a real
    // mutation testing run would catch the same regressions, but having them
    // in the suite means a human reader sees the invariant next to the code.

    it('m1: clearing portions on next-chunk failure fails — prior portion must stay', async () => {
        getPreview.mockImplementation(
            signalAware(() =>
                makePreview({
                    chunk_count: 2,
                    first_chunk: '<p data-testid="first-portion">first content here</p>',
                }),
            ),
        );
        getChunk.mockImplementation(signalAware(() => Promise.reject(new ApiError('boom', 0))));

        renderDialog();

        await screen.findByTestId('first-portion');
        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));

        expect(await screen.findByText('previewErrorRetryable')).toBeInTheDocument();
        // Direct, redundant re-assertion: the prior portion node is in the
        // document. If anyone wires the chunk-failure path to clear portions,
        // this is the canary that fails first.
        expect(screen.getByTestId('first-portion')).toBeInTheDocument();
    });

    it('m2: fetching the first chunk separately fails — getChunk is never called for index 0', async () => {
        getPreview.mockImplementation(signalAware(() => makePreview()));

        renderDialog();

        await screen.findByTestId('first-portion');
        expect(getChunk).not.toHaveBeenCalled();
    });

    it('m3: rendering an empty TOC column fails — the TOC node is absent when toc is empty', async () => {
        getPreview.mockImplementation(signalAware(() => makePreview({ toc: [] })));

        renderDialog();

        await screen.findByTestId('first-portion');
        expect(screen.queryByTestId('preview-toc')).not.toBeInTheDocument();
    });

    it('m4: not forwarding the AbortSignal fails — closing flips signal.aborted to true', async () => {
        let captured: AbortSignal | undefined;
        getPreview.mockImplementation((_id, signal) => {
            captured = signal;
            return new Promise<PreviewResponse>(() => {});
        });

        const view = renderDialog();
        await waitFor(() => expect(captured).toBeDefined());

        view.rerender(
            <BookPreviewDialog open={false} bookId={12} bookLang="ru" onClose={vi.fn()} />,
        );

        await waitFor(() => expect(captured!.aborted).toBe(true));
    });

    it('m5: showing Retry on permanent failure fails — no retry button when kind is permanent', async () => {
        getPreview.mockImplementation(signalAware(() => Promise.reject(new ApiError('bad', 400))));

        renderDialog();

        expect(await screen.findByText('previewErrorPermanent')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'previewRetry' })).not.toBeInTheDocument();
    });
});

describe('BookPreviewDialog — request ordering', () => {
    // Three chapters on chunks 0, 1, 2 give the reader somewhere to jump to.
    const TOC3 = [
        { title: 'Chapter 1', depth: 1, chunk: 0, anchor: 'c1' },
        { title: 'Chapter 2', depth: 1, chunk: 1, anchor: 'c2' },
        { title: 'Chapter 3', depth: 1, chunk: 2, anchor: 'c3' },
    ];

    interface CapturedChunk {
        index: number;
        revision: string;
        signal: AbortSignal;
        d: {
            promise: Promise<ChunkResponse>;
            resolve: (value: ChunkResponse) => void;
            reject: (error: unknown) => void;
        };
    }

    interface CapturedPreview {
        bookId: number;
        signal: AbortSignal;
        d: {
            promise: Promise<PreviewResponse>;
            resolve: (value: PreviewResponse) => void;
            reject: (error: unknown) => void;
        };
    }

    // Every chunk request is held open until the test resolves it by hand,
    // so the order in which answers arrive is chosen here, not by the
    // scheduler. That is what makes the race assertions deterministic.
    function captureChunkCalls(): CapturedChunk[] {
        const calls: CapturedChunk[] = [];
        getChunk.mockImplementation((_id, index, revision, signal) => {
            const d = deferred<ChunkResponse>();
            calls.push({ index, revision, signal: signal as AbortSignal, d });
            return d.promise;
        });
        return calls;
    }

    function capturePreviewCalls(): CapturedPreview[] {
        const calls: CapturedPreview[] = [];
        getPreview.mockImplementation((id, signal) => {
            const d = deferred<PreviewResponse>();
            calls.push({ bookId: id, signal: signal as AbortSignal, d });
            return d.promise;
        });
        return calls;
    }

    // Flush the promise the test just settled plus the state update it may
    // have triggered, so a following absence assertion is not a race against
    // the microtask queue.
    const flush = () => act(async () => {});

    function renderThreeChapterDialog(chunkCalls?: CapturedChunk[]) {
        getPreview.mockImplementation(
            signalAware(() => makePreview({ chunk_count: 3, toc: TOC3 })),
        );
        const calls = chunkCalls ?? captureChunkCalls();
        renderDialog();
        return calls;
    }

    it('opens the portion named by a TOC entry', async () => {
        const calls = renderThreeChapterDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByRole('button', { name: 'Chapter 3' }));

        await waitFor(() => expect(calls.length).toBe(1));
        expect(calls[0].index).toBe(2);
        expect(calls[0].revision).toBe('rev-1');

        calls[0].d.resolve({ chunk: '<p data-testid="portion-of-ch3">chapter three text</p>' });

        expect(await screen.findByTestId('portion-of-ch3')).toBeInTheDocument();
        // The portion the reader came from is not torn down.
        expect(screen.getByTestId('first-portion')).toBeInTheDocument();
    });

    it('race: a slower earlier answer never displaces the portion the reader moved to', async () => {
        const calls = renderThreeChapterDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));
        await waitFor(() => expect(calls.length).toBe(1)); // request A, chunk 1

        await userEvent.click(screen.getByRole('button', { name: 'Chapter 3' }));
        await waitFor(() => expect(calls.length).toBe(2)); // request B, chunk 2

        // B answers first: the reader sees the portion they asked for last.
        calls[1].d.resolve({ chunk: '<p data-testid="portion-b">portion two</p>' });
        expect(await screen.findByTestId('portion-b')).toBeInTheDocument();

        // A answers late. It must not appear — the reader left chunk 1 behind.
        calls[0].d.resolve({ chunk: '<p data-testid="portion-a">stale one</p>' });
        await flush();
        expect(screen.queryByTestId('portion-a')).not.toBeInTheDocument();
        expect(screen.queryByTestId('preview-portion-1')).not.toBeInTheDocument();
        expect(screen.getByTestId('portion-b')).toBeInTheDocument();
    });

    it('a superseded request stays stale even when the reader returns to the same portion', async () => {
        const calls = renderThreeChapterDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));
        await waitFor(() => expect(calls.length).toBe(1)); // A, chunk 1

        await userEvent.click(screen.getByRole('button', { name: 'Chapter 3' }));
        await waitFor(() => expect(calls.length).toBe(2)); // B, chunk 2

        await userEvent.click(screen.getByRole('button', { name: 'Chapter 2' }));
        await waitFor(() => expect(calls.length).toBe(3)); // C, chunk 1 again

        // A answers late. Its index matches the current one again — but it is
        // not the request that is currently open for chunk 1, so its content
        // must not land.
        calls[0].d.resolve({ chunk: '<p data-testid="portion-a">stale one</p>' });
        await flush();
        expect(screen.queryByTestId('portion-a')).not.toBeInTheDocument();

        // C is the live request for chunk 1; only its answer may appear.
        calls[2].d.resolve({ chunk: '<p data-testid="portion-c">fresh one</p>' });
        expect(await screen.findByTestId('portion-c')).toBeInTheDocument();
        expect(screen.queryByTestId('portion-a')).not.toBeInTheDocument();
    });

    it('superseding a request aborts it for real — the late answer dies on the signal, not on luck', async () => {
        const calls = renderThreeChapterDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));
        await waitFor(() => expect(calls.length).toBe(1));

        await userEvent.click(screen.getByRole('button', { name: 'Chapter 3' }));
        await waitFor(() => expect(calls.length).toBe(2));

        // The cancellation is real: the superseded request's signal fired.
        expect(calls[0].signal.aborted).toBe(true);
        expect(calls[1].signal.aborted).toBe(false);

        // Even if the server had already sent the bytes, nothing is applied.
        calls[0].d.resolve({ chunk: '<p data-testid="portion-a">stale one</p>' });
        await flush();
        expect(screen.queryByTestId('portion-a')).not.toBeInTheDocument();
        expect(screen.queryByTestId('preview-portion-1')).not.toBeInTheDocument();
    });

    it('a late rejection from a superseded request surfaces no error and keeps the shown portion', async () => {
        const calls = renderThreeChapterDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));
        await waitFor(() => expect(calls.length).toBe(1));

        await userEvent.click(screen.getByRole('button', { name: 'Chapter 3' }));
        await waitFor(() => expect(calls.length).toBe(2));

        calls[1].d.resolve({ chunk: '<p data-testid="portion-b">portion two</p>' });
        await screen.findByTestId('portion-b');

        // The superseded request fails late. No error may be shown for a
        // portion the reader is no longer looking at.
        calls[0].d.reject(new ApiError('boom', 0));
        await flush();
        expect(screen.queryByText('previewErrorRetryable')).not.toBeInTheDocument();
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        expect(screen.getByTestId('portion-b')).toBeInTheDocument();
    });

    it('a book switch starts from a clean slate and the previous book\'s late answers never land', async () => {
        const previews = capturePreviewCalls();
        const calls = captureChunkCalls();

        const view = renderDialog({ bookId: 12 });
        previews[0].d.resolve(
            makePreview({
                revision: 'rev-12',
                chunk_count: 3,
                toc: TOC3,
                first_chunk: '<p data-testid="book-twelve">twelve</p>',
            }),
        );
        await screen.findByTestId('book-twelve');

        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));
        await waitFor(() => expect(calls.length).toBe(1)); // chunk request, book 12

        view.rerender(
            <BookPreviewDialog open bookId={13} bookLang="ru" onClose={vi.fn()} />,
        );

        // Clean slate: the old portion disappears at once and the new book's
        // preview is requested. Both of book 12's requests are aborted.
        await waitFor(() => {
            expect(previews.length).toBe(2);
            expect(screen.queryByTestId('book-twelve')).not.toBeInTheDocument();
        });
        expect(previews[0].signal.aborted).toBe(true);
        expect(calls[0].signal.aborted).toBe(true);

        // Book 12's chunk answers late: it belongs to a book the reader left.
        calls[0].d.resolve({ chunk: '<p data-testid="twelve-late">late chunk</p>' });
        await flush();
        expect(screen.queryByTestId('twelve-late')).not.toBeInTheDocument();

        previews[1].d.resolve(
            makePreview({
                revision: 'rev-13',
                chunk_count: 1,
                toc: [{ title: 'New Chapter', depth: 1, chunk: 0, anchor: 'n1' }],
                first_chunk: '<p data-testid="book-thirteen">thirteen</p>',
            }),
        );
        expect(await screen.findByTestId('book-thirteen')).toBeInTheDocument();
        expect(screen.queryByTestId('book-twelve')).not.toBeInTheDocument();
        expect(screen.queryByTestId('twelve-late')).not.toBeInTheDocument();
        expect(screen.queryByTestId('preview-portion-1')).not.toBeInTheDocument();
    });

    it('a late preview response from the previous book never lands in the new one', async () => {
        const previews = capturePreviewCalls();

        const view = renderDialog({ bookId: 12 });
        await waitFor(() => expect(previews.length).toBe(1));

        view.rerender(
            <BookPreviewDialog open bookId={13} bookLang="ru" onClose={vi.fn()} />,
        );
        await waitFor(() => expect(previews.length).toBe(2));
        expect(previews[0].signal.aborted).toBe(true);

        // The old book's preview resolves only after the switch.
        previews[0].d.resolve(
            makePreview({ first_chunk: '<p data-testid="book-twelve">twelve</p>' }),
        );
        await flush();
        expect(screen.queryByTestId('book-twelve')).not.toBeInTheDocument();

        previews[1].d.resolve(
            makePreview({ first_chunk: '<p data-testid="book-thirteen">thirteen</p>' }),
        );
        expect(await screen.findByTestId('book-thirteen')).toBeInTheDocument();
        expect(screen.queryByTestId('book-twelve')).not.toBeInTheDocument();
    });


    // Aborting does not un-queue an answer that has already arrived: in a
    // browser a response can land in the microtask queue with the abort right
    // behind it. Every other race test here uses a double that rejects the
    // moment the signal fires, so none of them exercises that.
    //
    // This one resolves the superseded request successfully and asserts what
    // the reader sees. It passes with the generation stamp removed, and that
    // is worth saying plainly rather than dressing it up: the safety is
    // structural, not guarded. Portions are stored by their own index and the
    // view reads the index the reader is on, so a late answer lands in a slot
    // nobody is looking at. The stamp is a second line, and the test that
    // fails without it is the one about returning to the same portion.
    it('a superseded request that resolves anyway never displaces the shown portion', async () => {
        const first = deferred<{ chunk: string }>();
        const second = deferred<{ chunk: string }>();

        getPreview.mockImplementation(
            signalAware(() =>
                makePreview({
                    chunk_count: 3,
                    first_chunk: '<p>start</p>',
                    toc: [
                        { title: 'Chapter 1', depth: 1, chunk: 0, anchor: 'c1' },
                        { title: 'Chapter 3', depth: 1, chunk: 2, anchor: 'c3' },
                    ],
                }),
            ),
        );
        // Deliberately blind to the signal: the answer arrives regardless.
        getChunk.mockImplementationOnce(() => first.promise)
            .mockImplementationOnce(() => second.promise);

        renderDialog();
        await screen.findByText('start');

        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));
        await userEvent.click(screen.getByRole('button', { name: 'Chapter 3' }));

        // The reader is waiting on the second request; the first now answers.
        second.resolve({ chunk: '<p data-testid="wanted">third</p>' });
        await screen.findByTestId('wanted');
        first.resolve({ chunk: '<p data-testid="stale">second</p>' });
        await Promise.resolve();

        expect(screen.queryByTestId('stale')).toBeNull();
        expect(screen.getByTestId('wanted')).toBeInTheDocument();
    });

});

describe('BookPreviewDialog — narrow TOC panel', () => {
    // The wide layout ships the TOC as a column beside the text. The narrow
    // layout cannot — a hundred-item list laid over the text is unreadable —
    // so the same data drives a panel that opens over the work area. Opening
    // the panel must be cheap (the reader's place survives), Escape must
    // dismiss the panel before the dialog, the active entry must track the
    // portion on screen, and choosing an entry must scroll to its anchor.

    beforeEach(() => {
        // Top-level beforeEach sets wide; flip to narrow for this block.
        matches.current = false;
    });

    it('renders the trigger instead of the column, and opens the panel on click', async () => {
        getPreview.mockImplementation(signalAware(() => makePreview()));
        renderDialog();
        await screen.findByTestId('first-portion');

        // On narrow: no column, but a row that opens the panel.
        expect(screen.queryByTestId('preview-toc')).not.toBeInTheDocument();
        expect(screen.getByTestId('preview-toc-trigger')).toBeInTheDocument();
        expect(screen.queryByTestId('preview-toc-panel')).not.toBeInTheDocument();

        await userEvent.click(screen.getByTestId('preview-toc-trigger'));
        expect(await screen.findByTestId('preview-toc-panel')).toBeInTheDocument();
    });

    it('mutation #1: closing the panel without a selection keeps the same portion and its scroll', async () => {
        // The second chunk holds the place the reader must come back to, and
        // a third exists so that "Next" from there still has somewhere to go:
        // what Next asks the server for is one of the few things that tells
        // the reader's position apart from a silent reset to zero.
        getPreview.mockImplementation(
            signalAware(() =>
                makePreview({
                    chunk_count: 3,
                    toc: [
                        { title: 'Chapter 1', depth: 1, chunk: 0, anchor: 'c1' },
                        { title: 'Chapter 2', depth: 1, chunk: 1, anchor: 'c2' },
                    ],
                    first_chunk: '<p data-testid="first-portion">first content</p>',
                }),
            ),
        );
        getChunk.mockImplementation(
            signalAware(() => ({ chunk: '<p data-testid="portion-1-html">second</p>' })),
        );

        renderDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));
        expect(await screen.findByTestId('portion-1-html')).toBeInTheDocument();
        // After the first chunk fetch, getChunk has been called once. Any
        // further call would mean the dialog re-fetched on panel open —
        // i.e. it dropped the portion it already had.
        expect(getChunk).toHaveBeenCalledTimes(1);

        // Pretend the reader scrolled within the work area. In jsdom
        // scrollTop is a stored property — the only thing that can change it
        // is the component itself re-mounting the node or assigning to it.
        const scrollArea = screen.getByTestId('preview-scroll-area');
        scrollArea.scrollTop = 200;

        await userEvent.click(screen.getByTestId('preview-toc-trigger'));
        expect(await screen.findByTestId('preview-toc-panel')).toBeInTheDocument();

        // Close the panel via Back — no selection.
        await userEvent.click(screen.getByRole('button', { name: 'previewCloseToc' }));
        await waitFor(() =>
            expect(screen.queryByTestId('preview-toc-panel')).not.toBeInTheDocument(),
        );

        // Nothing was dropped and nothing re-mounted. The scroll area is
        // asked for again rather than reused from the variable above: a
        // re-created node leaves the old one detached with its scrollTop
        // intact, so `scrollArea.scrollTop` would still read 200 while the
        // reader's actual work area had just been rebuilt at the top. Node
        // identity is the assertion that catches that; the offset alone
        // cannot, and did not — keying this div on the panel's open state
        // passed the whole file.
        const sameArea = screen.getByTestId('preview-scroll-area');
        expect(sameArea).toBe(scrollArea);
        expect(sameArea.scrollTop).toBe(200);
        expect(screen.getByTestId('portion-1-html')).toBeInTheDocument();
        expect(getChunk).toHaveBeenCalledTimes(1);

        // The three assertions above are necessary and not sufficient, and
        // the difference matters enough to spell out. Portions render
        // stacked — the reader scrolls through one growing column — so
        // chunk 1's node stays in the document no matter which portion the
        // dialog thinks the reader is on. Adding `setCurrentIndex(0)` to
        // this Back button passed all three: the node was there, the fetch
        // count was one, and the scroll offset was untouched, while the
        // reader had silently been sent back to the start of the book.
        //
        // Where the position is actually observable is where it is used. Two
        // places, and deliberately not three: the entry marked current is
        // read from the same value as the trigger's label, so asserting both
        // would be one fact counted twice — and with the panel closed the
        // list is not in the document to ask anyway (that assertion lives in
        // the active-item test below, where the panel is open).
        expect(screen.getByTestId('preview-toc-trigger')).toHaveTextContent('Chapter 2');

        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));
        await waitFor(() => expect(getChunk).toHaveBeenCalledTimes(2));
        expect(getChunk.mock.calls[1][1]).toBe(2);
    });

    it('mutation #2: Escape dismisses the panel, not the dialog', async () => {
        getPreview.mockImplementation(signalAware(() => makePreview()));

        const { onClose } = renderDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByTestId('preview-toc-trigger'));
        await screen.findByTestId('preview-toc-panel');

        // Escape on the panel must dismiss the panel only.
        await userEvent.keyboard('{Escape}');

        await waitFor(() =>
            expect(screen.queryByTestId('preview-toc-panel')).not.toBeInTheDocument(),
        );
        expect(onClose).not.toHaveBeenCalled();
        expect(screen.getByTestId('first-portion')).toBeInTheDocument();
    });

    it('mutation #3: the active item tracks the shown portion, not the last TOC click', async () => {
        // Two TOC entries on different chunks. The reader reaches chunk 1
        // through Next — never by clicking Chapter 2 in the TOC — and the
        // panel re-opens with Chapter 2 marked active. Last-click tracking
        // would leave Chapter 1 marked.
        const chunkDeferred = deferred<{ chunk: string }>();
        getPreview.mockImplementation(
            signalAware(() =>
                makePreview({
                    chunk_count: 2,
                    toc: [
                        { title: 'Chapter 1', depth: 1, chunk: 0, anchor: 'c1' },
                        { title: 'Chapter 2', depth: 1, chunk: 1, anchor: 'c2' },
                    ],
                    first_chunk: '<p data-testid="first-portion">first</p>',
                }),
            ),
        );
        getChunk.mockImplementation(() => chunkDeferred.promise);

        renderDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByTestId('preview-toc-trigger'));
        let panel = await screen.findByTestId('preview-toc-panel');
        expect(within(panel).getByRole('button', { name: 'Chapter 1' }))
            .toHaveAttribute('aria-current', 'page');
        expect(within(panel).getByRole('button', { name: 'Chapter 2' }))
            .not.toHaveAttribute('aria-current');

        // Close the panel without ever clicking Chapter 2.
        await userEvent.click(screen.getByRole('button', { name: 'previewCloseToc' }));
        await waitFor(() => expect(screen.queryByTestId('preview-toc-panel')).not.toBeInTheDocument());

        // Advance via Next only.
        await userEvent.click(screen.getByRole('button', { name: 'previewNext' }));
        chunkDeferred.resolve({ chunk: '<p data-testid="portion-1-html">second</p>' });
        await screen.findByTestId('portion-1-html');

        // Re-open: Chapter 2 is now active, even though it was never clicked.
        await userEvent.click(screen.getByTestId('preview-toc-trigger'));
        panel = await screen.findByTestId('preview-toc-panel');
        expect(within(panel).getByRole('button', { name: 'Chapter 2' }))
            .toHaveAttribute('aria-current', 'page');
        expect(within(panel).getByRole('button', { name: 'Chapter 1' }))
            .not.toHaveAttribute('aria-current');
    });

    it('mutation #4: nested entries expose depth so a reader can see the hierarchy', async () => {
        getPreview.mockImplementation(
            signalAware(() =>
                makePreview({
                    toc: [
                        { title: 'Part', depth: 1, chunk: 0, anchor: 'p' },
                        { title: 'Subpart', depth: 2, chunk: 0, anchor: 's' },
                    ],
                }),
            ),
        );
        renderDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByTestId('preview-toc-trigger'));
        const panel = await screen.findByTestId('preview-toc-panel');

        // aria-level is the cheapest stable signal jsdom can read; a class or
        // padding would also do, but aria-level is the one screen readers use.
        const partRow = within(panel).getByRole('button', { name: 'Part' }).closest('li')!;
        const subpartRow = within(panel).getByRole('button', { name: 'Subpart' }).closest('li')!;
        expect(partRow).toHaveAttribute('aria-level', '1');
        expect(subpartRow).toHaveAttribute('aria-level', '2');
    });

    it('wide layout renders the column and renders no panel or trigger', async () => {
        // The top-level beforeEach defaults to wide; override the narrow
        // setting from this describe's own beforeEach.
        matches.current = true;

        getPreview.mockImplementation(signalAware(() => makePreview()));
        renderDialog();
        await screen.findByTestId('first-portion');

        expect(screen.getByTestId('preview-toc')).toBeInTheDocument();
        expect(screen.queryByTestId('preview-toc-trigger')).not.toBeInTheDocument();
        expect(screen.queryByTestId('preview-toc-panel')).not.toBeInTheDocument();
    });

    it('selecting an entry closes the panel and scrolls the anchor into view', async () => {
        // The anchor is an id on an element inside the rendered HTML. The
        // server guarantees the anchor exists in the chunk the TOC names, so
        // the honest test puts one there and observes scrollIntoView called
        // on exactly that element.
        getPreview.mockImplementation(
            signalAware(() =>
                makePreview({
                    chunk_count: 2,
                    toc: [
                        { title: 'Chapter 1', depth: 1, chunk: 0, anchor: 'c1' },
                        { title: 'Chapter 2', depth: 1, chunk: 1, anchor: 'c2' },
                    ],
                    first_chunk: '<p data-testid="first-portion">first</p>',
                }),
            ),
        );
        getChunk.mockImplementation(
            signalAware(() => ({
                chunk: '<p data-testid="portion-1-html"><span id="c2">chapter 2 start</span></p>',
            })),
        );

        // jsdom does not implement scrollIntoView at all; install a stub on
        // the prototype before the component mounts so the call is observable.
        const scrollIntoView = vi.fn();
        Element.prototype.scrollIntoView = scrollIntoView;

        renderDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByTestId('preview-toc-trigger'));
        await screen.findByTestId('preview-toc-panel');

        await userEvent.click(screen.getByRole('button', { name: 'Chapter 2' }));

        await screen.findByTestId('portion-1-html');
        await waitFor(() => expect(scrollIntoView).toHaveBeenCalled());

        // The call landed on the anchor, and the panel closed.
        const target = scrollIntoView.mock.instances[0] as HTMLElement;
        expect(target.id).toBe('c2');
        expect(screen.queryByTestId('preview-toc-panel')).not.toBeInTheDocument();

        delete (Element.prototype as unknown as Record<string, unknown>).scrollIntoView;
    });

    it('selecting a second entry inside the portion already shown still scrolls', async () => {
        // A chunk can hold more than one TOC entry — a part and its subparts
        // land in the same portion — so an entry can move the reader without
        // moving the portion. That case is what keeps the queued anchor in
        // state rather than a ref: an effect watching only the portion has
        // nothing to notice here, and the tap would do nothing at all.
        getPreview.mockImplementation(
            signalAware(() =>
                makePreview({
                    chunk_count: 1,
                    toc: [
                        { title: 'Part', depth: 1, chunk: 0, anchor: 'p1' },
                        { title: 'Subpart', depth: 2, chunk: 0, anchor: 'p2' },
                    ],
                    first_chunk:
                        '<div data-testid="first-portion">' +
                        '<h2 id="p1">part</h2><h3 id="p2">subpart</h3></div>',
                }),
            ),
        );

        const scrollIntoView = vi.fn();
        Element.prototype.scrollIntoView = scrollIntoView;

        renderDialog();
        await screen.findByTestId('first-portion');

        // First tap: Part. The portion is already the one on screen.
        await userEvent.click(screen.getByTestId('preview-toc-trigger'));
        await userEvent.click(
            within(await screen.findByTestId('preview-toc-panel')).getByRole('button', {
                name: 'Part',
            }),
        );
        await waitFor(() => expect(scrollIntoView).toHaveBeenCalledTimes(1));
        expect((scrollIntoView.mock.instances[0] as HTMLElement).id).toBe('p1');

        // Second tap: Subpart. Same portion, same index, different anchor —
        // the one the ref version would silently drop.
        await userEvent.click(screen.getByTestId('preview-toc-trigger'));
        await userEvent.click(
            within(await screen.findByTestId('preview-toc-panel')).getByRole('button', {
                name: 'Subpart',
            }),
        );
        await waitFor(() => expect(scrollIntoView).toHaveBeenCalledTimes(2));
        expect((scrollIntoView.mock.instances[1] as HTMLElement).id).toBe('p2');

        delete (Element.prototype as unknown as Record<string, unknown>).scrollIntoView;
    });

    it('selecting an entry whose anchor is absent scrolls to the portion, with no error', async () => {
        // The server guarantees the anchor; this test pins the spec's
        // fallback: missing anchor scrolls to the portion's top, no throw.
        getPreview.mockImplementation(
            signalAware(() =>
                makePreview({
                    chunk_count: 2,
                    toc: [
                        { title: 'Chapter 1', depth: 1, chunk: 0, anchor: 'c1' },
                        { title: 'Chapter 2', depth: 1, chunk: 1, anchor: 'missing' },
                    ],
                    first_chunk: '<p data-testid="first-portion">first</p>',
                }),
            ),
        );
        getChunk.mockImplementation(
            signalAware(() => ({
                chunk: '<p data-testid="portion-1-html">chapter 2 body, no anchor</p>',
            })),
        );

        const scrollIntoView = vi.fn();
        Element.prototype.scrollIntoView = scrollIntoView;

        renderDialog();
        await screen.findByTestId('first-portion');

        await userEvent.click(screen.getByTestId('preview-toc-trigger'));
        await screen.findByTestId('preview-toc-panel');

        await userEvent.click(screen.getByRole('button', { name: 'Chapter 2' }));

        await screen.findByTestId('portion-1-html');
        await waitFor(() => expect(scrollIntoView).toHaveBeenCalled());

        // Fallback target is the portion node — not nothing, and not the
        // missing anchor.
        const target = scrollIntoView.mock.instances[0] as HTMLElement;
        expect(target).toHaveAttribute('data-testid', 'preview-portion-1');

        delete (Element.prototype as unknown as Record<string, unknown>).scrollIntoView;
    });
});

describe('BookPreviewDialog — single layout boundary', () => {
    // The narrow panel is the only second JS layout branch this file has
    // gained, and its width question must be the same one the card asks —
    // CARD_WIDE_QUERY — or the two will disagree across the same band of
    // widths that broke the card before. A second inline query, or a second
    // useMediaQuery call, is the same defect under another name.

    // `import.meta.dirname` rather than `new URL('../…', import.meta.url)`:
    // Vite's transform rewrites the latter for asset URL handling, and under
    // vitest the rewrite serves the file from the dev server (http://localhost)
    // which fileURLToPath then refuses.
    const source = readFileSync(
        path.resolve(import.meta.dirname, '../BookPreviewDialog.tsx'),
        'utf8',
    );

    it('asks its one width question through the shared query', () => {
        expect(source).toContain('useMediaQuery(CARD_WIDE_QUERY)');
    });

    it('writes no inline width or height media query', () => {
        // Any inline query is a second boundary, whatever the number — the
        // named constant is the only allowed spelling.
        expect(source.replace('CARD_WIDE_QUERY', '')).not.toMatch(
            /\((?:min|max)-(?:width|height)\s*:/,
        );
    });

    it('calls useMediaQuery exactly once', () => {
        expect([...source.matchAll(/useMediaQuery\(/g)]).toHaveLength(1);
    });
});
