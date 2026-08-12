import React from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import BookPreviewDialog from '@/features/catalogue/BookPreviewDialog';
import * as previewApi from '@/api/preview';
import { ApiError } from '@/api/errors';
import type { PreviewResponse } from '@/api/preview';

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
