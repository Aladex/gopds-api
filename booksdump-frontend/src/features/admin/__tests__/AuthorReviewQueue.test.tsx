import React from 'react';
import { readFileSync } from 'node:fs';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Router, Routes, useLocation } from 'react-router';
import { UNSAFE_createMemoryHistory as createMemoryHistory } from 'react-router';

import AuthorReviewQueue from '@/features/admin/AuthorReviewQueue';
import {
    LegacyAuthorNormalizationRedirect,
    LegacyAuthorReviewRedirect,
} from '@/features/admin/AdminPanel';
import AuthorReviewDetailScreen, {
    REVIEW_DETAIL_ROUTE,
    buildEditResult,
    draftFromProposal,
} from '@/features/admin/AuthorReviewDetail';
import * as adminApi from '@/api/admin';
import type { AuthorReviewDetail, AuthorReviewListItem } from '@/api/admin';
import { ApiError } from '@/api/errors';
import { ADMIN_TABLE_WIDE_QUERY } from '@/shared/layout/breakpoints';
import enTranslation from '@/locales/en/translation.json';
import ruTranslation from '@/locales/ru/translation.json';

// A stable t: a fresh one per render would change every effect's dependencies
// on each pass and loop the fetch effects.
const { i18nHolder } = vi.hoisted(() => ({
    i18nHolder: {
        // Mirrors i18next closely enough for the assertions: a string second
        // argument is the fallback, { defaultValue } interpolates like the real
        // translator, everything else renders as the key.
        t: (key: string, opts?: unknown) =>
            typeof opts === 'string'
                ? opts
                : opts &&
                    typeof opts === 'object' &&
                    typeof (opts as { defaultValue?: unknown }).defaultValue === 'string'
                  ? (opts as { defaultValue: string }).defaultValue.replace(
                        /\{\{(\w+)\}\}/g,
                        (_match: string, name: string) =>
                            String((opts as Record<string, unknown>)[name] ?? ''),
                    )
                  : key,
    },
}));

vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: i18nHolder.t }),
}));

vi.mock('@/api/admin', async (importOriginal) => {
    // Spread the real module first so types and any future guards stay the
    // production ones; only the network functions are mocked.
    const actual = await importOriginal<typeof import('@/api/admin')>();
    return {
        ...actual,
        getCurrentAuthorMetadataRun: vi.fn(),
        getLatestAuthorMetadataRun: vi.fn(),
        listAuthorReviewItems: vi.fn(),
        getAuthorReviewItem: vi.fn(),
        acceptAuthorReviewItem: vi.fn(),
        editAuthorReviewItem: vi.fn(),
        classifyAuthorReviewItem: vi.fn(),
        markAuthorReviewUnresolved: vi.fn(),
        retryAuthorReviewNormalization: vi.fn(),
    };
});

const api = vi.mocked(adminApi);

/**
 * One fixture timeline from a single anchor plus offsets: the common rules
 * forbid calendar literals anywhere in tests or fixtures.
 */
const anchorMs = Date.now();
const minutesBefore = (minutes: number) => new Date(anchorMs - minutes * 60_000).toISOString();

const listItem = (overrides: Partial<AuthorReviewListItem> = {}): AuthorReviewListItem => ({
    id: 11,
    scope: 'fingerprint',
    credit_id: null,
    source_fingerprint: 'a1b2c3d4e5f60718',
    reason: 'initials',
    decision_class: 'ambiguous_initials',
    display_name: 'Fixture Display A',
    credits_count: 1,
    created_at: minutesBefore(90),
    ...overrides,
});

const secondItem = listItem({
    id: 12,
    scope: 'fingerprint',
    credit_id: null,
    source_fingerprint: '0f9e8d7c6b5a4321',
    reason: 'mixed_script',
    decision_class: 'mixed_script',
    display_name: 'Fixture Display B',
    credits_count: 4,
    created_at: minutesBefore(30),
});

/**
 * A legacy credit-scoped item: only a credit-scoped override flagged after a
 * schema change opens one, and the simplified tab never shows or decides it.
 */
const legacyCreditItem = listItem({
    id: 13,
    scope: 'credit',
    credit_id: 31,
    source_fingerprint: '5566778899aabbcc',
    display_name: 'Fixture Display C',
    created_at: minutesBefore(60),
});

const detail = (overrides: Partial<AuthorReviewDetail> = {}): AuthorReviewDetail => ({
    ...listItem(),
    source: {
        first: 'Fixture',
        middle: null,
        last: 'Author',
        nickname: null,
        display: 'Fixture Author',
        flags: ['two_surnames'],
    },
    proposal: {
        given_name: 'Fixture',
        additional_names: null,
        family_name: 'Author',
        nickname: null,
        display_name: 'Fixture Display A',
        sort_name: 'Author, Fixture',
        search_key: 'author fixture',
        script: 'Cyrl',
        kind: 'person',
        method: 'rules',
        quality_flags: ['initials_split'],
    },
    linked_books: [
        { id: 101, title: 'Fixture Book One' },
        { id: 102, title: 'Fixture Book Two' },
    ],
    ...overrides,
});

beforeEach(() => {
    vi.clearAllMocks();
    api.getCurrentAuthorMetadataRun.mockResolvedValue({ run: null });
    api.getLatestAuthorMetadataRun.mockResolvedValue({ run: null });
    api.listAuthorReviewItems.mockResolvedValue({
        items: [listItem(), legacyCreditItem, secondItem],
        next_cursor: 'cursor-2',
    });
    api.getAuthorReviewItem.mockResolvedValue({ item: detail() });
});

/** The real route pair: the queue (narrow leaves it, wide stays on it) and the detail screen. */
const renderAt = (path: string) =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <Routes>
                <Route path="/queue" element={<AuthorReviewQueue />} />
                <Route path="/admin/book-scanning" element={<AuthorReviewQueue />} />
                <Route
                    path="/admin/book-scanning/authors/:id"
                    element={<AuthorReviewDetailScreen />}
                />
            </Routes>
        </MemoryRouter>,
    );

/**
 * The same route pair over a history the test owns, so browser Back and
 * Forward can be driven exactly as the browser would (a POP that changes only
 * the query keeps the queue mounted — the case the address must still answer).
 */
const renderWithHistory = (path: string) => {
    // v5Compat: what MemoryRouter passes — push/replace notify the listener,
    // so the harness re-renders on the queue's own writes as well as on POP.
    const history = createMemoryHistory({ initialEntries: [path], v5Compat: true });
    const Harness: React.FC = () => {
        const [location, setLocation] = React.useState(history.location);
        React.useEffect(() => history.listen(({ location: next }) => setLocation(next)), [history]);
        return (
            <Router navigator={history} location={location}>
                <Routes>
                    <Route path="/queue" element={<AuthorReviewQueue />} />
                    <Route path="/admin/book-scanning" element={<AuthorReviewQueue />} />
                    <Route
                        path="/admin/book-scanning/authors/:id"
                        element={<AuthorReviewDetailScreen />}
                    />
                </Routes>
            </Router>
        );
    };
    return { history, ...render(<Harness />) };
};

/** Shows where a redirect landed. */
const LocationProbe: React.FC = () => {
    const location = useLocation();
    return <p data-testid="location">{`${location.pathname}${location.search}`}</p>;
};

// The default matchMedia answer is "not matching", which is the narrow layout:
// View navigates to the detail route.
const openDetail = async (name: string) => {
    fireEvent.click(await screen.findByRole('button', { name: `View: ${name}` }));
    await screen.findByRole('region', { name: 'Review item' });
};

// Wide layout: View opens the modal and the queue stays mounted under it.
const openDetailWide = async (name: string) => {
    const view = await screen.findByRole('button', { name: `View: ${name}` });
    view.focus();
    fireEvent.click(view);
    await screen.findByRole('dialog');
    await screen.findByRole('region', { name: 'Review item' });
    return view;
};

/** Overrides the media query answer for the one boundary the queue asks about. */
const stubTableWide = (wide: boolean) => {
    const original = window.matchMedia;
    window.matchMedia = ((query: string) =>
        ({
            matches: wide && query === ADMIN_TABLE_WIDE_QUERY,
            media: query,
            onchange: null,
            addListener: () => {},
            removeListener: () => {},
            addEventListener: () => {},
            removeEventListener: () => {},
            dispatchEvent: () => false,
        }) as MediaQueryList) as typeof window.matchMedia;
    return () => {
        window.matchMedia = original;
    };
};

describe('queue list', () => {
    it('renders the open queue by default with paginated rows', async () => {
        renderAt('/queue');
        await screen.findByText('Fixture Display A');

        expect(api.listAuthorReviewItems).toHaveBeenCalledWith({ status: 'open', limit: 50 });
        expect(screen.getByText('Fixture Display B')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Next page' })).toBeEnabled();
    });

    it('filters by status through the server', async () => {
        renderAt('/queue');
        await screen.findByText('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Closed' }));

        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenCalledWith({ status: 'closed', limit: 50 }),
        );
    });

    it('pages forward and back through opaque cursors', async () => {
        api.listAuthorReviewItems
            .mockResolvedValueOnce({ items: [listItem()], next_cursor: 'cursor-2' })
            .mockResolvedValueOnce({ items: [secondItem], next_cursor: null });
        renderAt('/queue');
        await screen.findByText('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
        await screen.findByText('Fixture Display B');
        expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
            status: 'open',
            cursor: 'cursor-2',
            limit: 50,
        });
        expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Previous page' })).toBeEnabled();

        fireEvent.click(screen.getByRole('button', { name: 'Previous page' }));
        await screen.findByText('Fixture Display A');
        expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
            status: 'open',
            limit: 50,
        });
    });

    it('renders the empty state', async () => {
        api.listAuthorReviewItems.mockResolvedValue({ items: [], next_cursor: null });
        renderAt('/queue');

        expect(await screen.findByText('No review items.')).toBeInTheDocument();
    });

    it('renders the loading state before the first answer', () => {
        api.listAuthorReviewItems.mockReturnValue(new Promise(() => {}));
        renderAt('/queue');

        expect(screen.getByText('Loading...')).toBeInTheDocument();
    });

    it('shows a localized error, never the raw transport text', async () => {
        api.listAuthorReviewItems.mockRejectedValue(
            new ApiError('private transport message', 500, { body: { error: 'surprise_code' } }),
        );
        renderAt('/queue');

        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent('Action failed.');
        expect(screen.queryByText('private transport message')).toBeNull();
    });

    it('refuses list ids that JSON parsing rounded', async () => {
        api.listAuthorReviewItems.mockResolvedValue({
            items: [listItem({ id: JSON.parse('9007199254740993') as number })],
            next_cursor: null,
        });
        renderAt('/queue');
        await screen.findByText('Fixture Display A');

        const view = screen.getByRole('button', { name: 'View: Fixture Display A' });
        expect(view).toBeDisabled();
        fireEvent.click(view);
        expect(api.getAuthorReviewItem).not.toHaveBeenCalled();
    });
});

describe('detail on its own route (narrow)', () => {
    it('shows the name as in the file, the proposal and bounded books, nothing technical', async () => {
        renderAt('/queue');
        await openDetail('Fixture Display A');

        const panel = screen.getByRole('region', { name: 'Review item' });
        expect(within(panel).getByText('Fixture Author')).toBeInTheDocument();
        expect(within(panel).getByText('Author, Fixture')).toBeInTheDocument();
        expect(within(panel).getByText('Fixture Book One')).toBeInTheDocument();
        expect(within(panel).getByText('Fixture Book Two')).toBeInTheDocument();
        // Flags, classes, scripts, methods, scope, fingerprint and search key
        // are not shown at all.
        for (const technical of [
            'two_surnames',
            'initials_split',
            'ambiguous_initials',
            'Cyrl',
            'rules',
            'Credit',
            'a1b2c3d4e5f60718',
            'author fixture',
        ]) {
            expect(within(panel).queryByText(technical)).toBeNull();
        }
        expect(screen.queryByRole('button', { name: 'Show technical details' })).toBeNull();
    });

    it('replaces the queue with the detail screen and offers the way back', async () => {
        renderAt('/queue');
        await openDetail('Fixture Display A');

        expect(api.getAuthorReviewItem).toHaveBeenCalledWith(11);
        expect(screen.queryByRole('button', { name: 'Next page' })).toBeNull();
        expect(screen.getByRole('button', { name: 'Back to the queue' })).toBeInTheDocument();
    });

    it('deep-links and reloads the detail route without the queue', async () => {
        renderAt('/admin/book-scanning/authors/12');

        await screen.findByRole('region', { name: 'Review item' });
        expect(api.getAuthorReviewItem).toHaveBeenCalledWith(12);
        expect(api.listAuthorReviewItems).not.toHaveBeenCalled();
        expect(screen.getByRole('button', { name: 'Back to the queue' })).toBeInTheDocument();
    });

    it('refuses unsafe or non-canonical ids from the address', async () => {
        for (const bad of ['9007199254740993', 'abc', '007', '0', '-11']) {
            const { unmount } = renderAt(`/admin/book-scanning/authors/${bad}`);
            expect(
                await screen.findByText('This review item address is invalid.'),
            ).toBeInTheDocument();
            expect(api.getAuthorReviewItem).not.toHaveBeenCalled();
            expect(screen.getByRole('button', { name: 'Back to the queue' })).toBeInTheDocument();
            unmount();
        }
    });

    it('back returns to the queue at the same filter and page', async () => {
        renderAt('/queue');
        await screen.findByText('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenCalledWith({ status: 'closed', limit: 50 }),
        );
        fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenCalledWith({
                status: 'closed',
                cursor: 'cursor-2',
                limit: 50,
            }),
        );

        await openDetail('Fixture Display A');
        fireEvent.click(screen.getByRole('button', { name: 'Back to the queue' }));

        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                cursor: 'cursor-2',
                limit: 50,
            }),
        );
        expect(screen.getByRole('button', { name: 'Previous page' })).toBeEnabled();
    });

    it('back from a bare deep-link lands on the queue defaults', async () => {
        renderAt('/admin/book-scanning/authors/11');
        await screen.findByRole('region', { name: 'Review item' });

        fireEvent.click(screen.getByRole('button', { name: 'Back to the queue' }));

        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                limit: 50,
            }),
        );
    });

    it('offers the correction form when there is no proposal yet', async () => {
        api.getAuthorReviewItem.mockResolvedValue({ item: detail({ proposal: null }) });
        renderAt('/queue');
        await openDetail('Fixture Display A');

        expect(screen.getByText('No proposal yet.')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Accept' })).toBeDisabled();
        fireEvent.click(screen.getByRole('button', { name: 'Correct' }));
        fireEvent.click(
            within(screen.getByRole('group', { name: 'What this is' })).getByRole('button', {
                name: 'Person',
            }),
        );
        expect(screen.getByLabelText('Display name')).toHaveValue('');
    });
});

describe('detail in a modal (wide)', () => {
    it('opens the detail in a dialog while the queue stays in place', async () => {
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await openDetailWide('Fixture Display A');

            const dialog = screen.getByRole('dialog');
            expect(within(dialog).getByRole('region', { name: 'Review item' })).toBeInTheDocument();
            // The list stays where it was. While the modal is open Radix marks
            // the background aria-hidden, so the rows are asked for by text and
            // the pager with hidden: true — present is what matters.
            expect(screen.getByText('Fixture Display B')).toBeInTheDocument();
            expect(screen.getByRole('button', { name: 'Next page', hidden: true })).toBeEnabled();
        } finally {
            restore();
        }
    });

    it('Escape closes the modal and returns focus to the row View button', async () => {
        const user = userEvent.setup();
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            const view = await screen.findByRole('button', { name: 'View: Fixture Display A' });
            await user.click(view);
            expect(await screen.findByRole('dialog')).toBeInTheDocument();
            expect(view).not.toHaveFocus();

            await user.keyboard('{Escape}');
            await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
            expect(view).toHaveFocus();
            // And the queue is still there, un-navigated.
            expect(screen.getByRole('button', { name: 'Next page' })).toBeEnabled();
        } finally {
            restore();
        }
    });

    it('returns focus to the clicked row even when a mouse click does not focus it', async () => {
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            // A previously used control still holds keyboard focus — a pointer
            // activation of the row need not move it.
            const open = await screen.findByRole('button', { name: 'Open' });
            open.focus();
            const view = screen.getByRole('button', { name: 'View: Fixture Display A' });
            fireEvent.click(view);
            expect(await screen.findByRole('dialog')).toBeInTheDocument();

            fireEvent.keyDown(document.body, { key: 'Escape' });
            await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
            expect(view).toHaveFocus();
            expect(open).not.toHaveFocus();
        } finally {
            restore();
        }
    });

    it('focuses a stable queue fallback when the opener row is gone', async () => {
        const restore = stubTableWide(true);
        try {
            api.listAuthorReviewItems
                .mockResolvedValueOnce({ items: [listItem(), secondItem], next_cursor: 'cursor-2' })
                .mockResolvedValue({ items: [secondItem], next_cursor: null });
            renderAt('/queue');
            const view = await screen.findByRole('button', { name: 'View: Fixture Display A' });
            fireEvent.click(view);
            await screen.findByRole('dialog');

            // The list moves on while the modal is open; the opener's row leaves.
            fireEvent.click(screen.getByRole('button', { name: 'Next page', hidden: true }));
            await waitFor(() =>
                expect(
                    screen.queryByRole('button', { name: 'View: Fixture Display A' }),
                ).toBeNull(),
            );

            fireEvent.keyDown(document.body, { key: 'Escape' });
            await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
            expect(screen.getByRole('region', { name: 'Review queue' })).toHaveFocus();
        } finally {
            restore();
        }
    });

    it('keeps the header clear of the dialog close button and offers no retry', async () => {
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await openDetailWide('Fixture Display A');

            // jsdom cannot overlap-check two boxes; what keeps the header row
            // clear of the absolutely placed 44px close square is its padding.
            const header = document.getElementById('author-review-detail-heading')?.parentElement;
            expect(header).not.toBeNull();
            expect(header).toHaveClass('pr-12');
            expect(screen.queryByRole('button', { name: 'Retry normalization' })).toBeNull();
        } finally {
            restore();
        }
    });
});

describe('browser history within the queue', () => {
    it('reflects browser Back within the queue in both URL and data', async () => {
        const { history } = renderWithHistory('/queue');
        await screen.findByText('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                limit: 50,
            }),
        );
        fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                cursor: 'cursor-2',
                limit: 50,
            }),
        );

        // Native Back pops the pagination entry: the address loses the cursor
        // and the queue must ask for the first page of the closed list again.
        act(() => history.go(-1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                limit: 50,
            }),
        );
        expect(screen.getByText('Fixture Display A')).toBeInTheDocument();
    });

    it('restores the status filter after native browser Back', async () => {
        const { history } = renderWithHistory('/queue');
        await screen.findByText('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                limit: 50,
            }),
        );

        // Native Back pops the filter entry: the queue returns to Open in the
        // address and in the data it asks for.
        act(() => history.go(-1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                limit: 50,
            }),
        );
        expect(screen.getByRole('button', { name: 'Open' })).toHaveAttribute(
            'aria-pressed',
            'true',
        );
    });

    it('follows browser Forward back onto the paginated page', async () => {
        const { history } = renderWithHistory('/queue');
        await screen.findByText('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                cursor: 'cursor-2',
                limit: 50,
            }),
        );
        act(() => history.go(-1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                limit: 50,
            }),
        );

        act(() => history.go(1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                cursor: 'cursor-2',
                limit: 50,
            }),
        );
    });

    it('carries the address state, not stale local state, into the detail route', async () => {
        const { history } = renderWithHistory('/queue');
        await screen.findByText('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                limit: 50,
            }),
        );
        act(() => history.go(-1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                limit: 50,
            }),
        );

        await openDetail('Fixture Display A');
        // The detail was opened from the restored first page, and its Back
        // returns there: no cursor in what comes back.
        fireEvent.click(screen.getByRole('button', { name: 'Back to the queue' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                limit: 50,
            }),
        );
    });
});

const confirmInDialog = async () =>
    fireEvent.click(
        within(await screen.findByRole('dialog')).getByRole('button', { name: 'Confirm' }),
    );

describe("three decisions, always on the item's own scope", () => {
    it('offers exactly Accept, Correct and Keep as in the file, with no scope to choose', async () => {
        renderAt('/queue');
        await openDetail('Fixture Display A');

        for (const name of ['Accept', 'Correct', 'Keep as in the file']) {
            expect(screen.getByRole('button', { name })).toBeEnabled();
        }
        for (const gone of [
            'Single credit',
            'All credits',
            'Classify',
            'Mark unresolved',
            'Save edit',
        ]) {
            expect(screen.queryByRole('button', { name: gone })).toBeNull();
        }
        // The kinds live behind Correct.
        expect(screen.queryByRole('group', { name: 'What this is' })).toBeNull();
    });

    it('accepts with the item scope after a confirmation that can be cancelled', async () => {
        api.acceptAuthorReviewItem.mockResolvedValue({ item: detail() });
        api.getAuthorReviewItem.mockResolvedValue({ item: detail({ ...secondItem }) });
        renderAt('/queue');
        await openDetail('Fixture Display B');

        const accept = screen.getByRole('button', { name: 'Accept' });
        fireEvent.click(accept);
        const dialog = await screen.findByRole('dialog');
        expect(
            within(dialog).getByText(
                'The decision applies to all 4 credits with this name in the file, books added later included.',
            ),
        ).toBeInTheDocument();
        fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        expect(api.acceptAuthorReviewItem).not.toHaveBeenCalled();

        fireEvent.click(accept);
        await confirmInDialog();
        await waitFor(() =>
            expect(api.acceptAuthorReviewItem).toHaveBeenCalledWith(12, { scope: 'fingerprint' }),
        );
        expect(await screen.findByRole('status')).toHaveTextContent('Saved.');
    });

    it('keeps the name as in the file through the unresolved action', async () => {
        api.markAuthorReviewUnresolved.mockResolvedValue({ item: detail() });
        api.getAuthorReviewItem.mockResolvedValue({ item: detail({ ...secondItem }) });
        renderAt('/queue');
        await openDetail('Fixture Display B');

        fireEvent.click(screen.getByRole('button', { name: 'Keep as in the file' }));
        await confirmInDialog();
        await waitFor(() =>
            expect(api.markAuthorReviewUnresolved).toHaveBeenCalledWith(12, {
                scope: 'fingerprint',
            }),
        );
    });

    it('every decision the tab sends is fingerprint-scoped', async () => {
        api.acceptAuthorReviewItem.mockResolvedValue({ item: detail() });
        api.markAuthorReviewUnresolved.mockResolvedValue({ item: detail() });
        api.classifyAuthorReviewItem.mockResolvedValue({ item: detail() });
        api.editAuthorReviewItem.mockResolvedValue({ item: detail() });
        renderAt('/queue');
        await openDetail('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
        await confirmInDialog();
        await waitFor(() => expect(api.acceptAuthorReviewItem).toHaveBeenCalled());
        fireEvent.click(await screen.findByRole('button', { name: 'Keep as in the file' }));
        await confirmInDialog();
        await waitFor(() => expect(api.markAuthorReviewUnresolved).toHaveBeenCalled());
        fireEvent.click(await screen.findByRole('button', { name: 'Correct' }));
        fireEvent.click(screen.getByRole('button', { name: 'Save correction' }));
        await confirmInDialog();
        await waitFor(() => expect(api.editAuthorReviewItem).toHaveBeenCalled());
        fireEvent.click(await screen.findByRole('button', { name: 'Correct' }));
        const kinds = screen.getByRole('group', { name: 'What this is' });
        fireEvent.click(within(kinds).getByRole('button', { name: 'Unknown' }));
        fireEvent.click(screen.getByRole('button', { name: 'Save correction' }));
        await confirmInDialog();
        await waitFor(() => expect(api.classifyAuthorReviewItem).toHaveBeenCalled());

        const sent = [
            ...api.acceptAuthorReviewItem.mock.calls,
            ...api.markAuthorReviewUnresolved.mock.calls,
            ...api.editAuthorReviewItem.mock.calls,
            ...api.classifyAuthorReviewItem.mock.calls,
        ];
        expect(sent).toHaveLength(4);
        for (const [, payload] of sent) {
            expect(payload.scope).toBe('fingerprint');
        }
    });

    it('keeps a legacy credit-scoped item out of the list', async () => {
        renderAt('/queue');
        await screen.findByText('Fixture Display A');
        expect(screen.getByText('Fixture Display B')).toBeInTheDocument();
        expect(screen.queryByText('Fixture Display C')).toBeNull();
    });

    it('shows a legacy credit-scoped item opened by its link read-only', async () => {
        api.getAuthorReviewItem.mockResolvedValue({ item: detail({ ...legacyCreditItem }) });
        renderAt(`${REVIEW_DETAIL_ROUTE}/13`);
        await screen.findByRole('region', { name: 'Review item' });

        expect(
            await screen.findByText(
                'This item concerns a single book, not every book with this name. It cannot be decided here.',
            ),
        ).toBeInTheDocument();
        for (const name of ['Accept', 'Correct', 'Keep as in the file']) {
            expect(screen.queryByRole('button', { name })).toBeNull();
        }
    });

    it('skips pages that hold only legacy items, so the queue is never empty-looking with more to come', async () => {
        api.listAuthorReviewItems
            .mockResolvedValueOnce({ items: [legacyCreditItem], next_cursor: 'cursor-2' })
            .mockResolvedValueOnce({ items: [secondItem], next_cursor: null });
        renderAt('/queue');

        await screen.findByText('Fixture Display B');
        expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
            status: 'open',
            cursor: 'cursor-2',
            limit: 50,
        });
        expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled();
    });

    it('shows the empty state when only legacy items are left', async () => {
        api.listAuthorReviewItems.mockResolvedValue({
            items: [legacyCreditItem],
            next_cursor: null,
        });
        renderAt('/queue');

        expect(await screen.findByText('No review items.')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled();
    });

    it('classifies through the kind field of Correct', async () => {
        api.classifyAuthorReviewItem.mockResolvedValue({ item: detail() });
        renderAt('/queue');
        await openDetail('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Correct' }));
        const kinds = screen.getByRole('group', { name: 'What this is' });
        fireEvent.click(within(kinds).getByRole('button', { name: 'Collective' }));
        // Not a person: no name fields to fill.
        expect(screen.queryByLabelText('Display name')).toBeNull();
        fireEvent.click(screen.getByRole('button', { name: 'Save correction' }));
        await confirmInDialog();

        await waitFor(() =>
            expect(api.classifyAuthorReviewItem).toHaveBeenCalledWith(11, {
                scope: 'fingerprint',
                kind: 'collective',
            }),
        );
        expect(api.editAuthorReviewItem).not.toHaveBeenCalled();
    });

    it('sends the exact correction payload with empty optionals as null', async () => {
        api.editAuthorReviewItem.mockResolvedValue({
            item: detail({ display_name: 'Edited Display' }),
        });
        api.getAuthorReviewItem.mockResolvedValue({ item: detail({ ...secondItem }) });
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await openDetailWide('Fixture Display B');

            fireEvent.click(screen.getByRole('button', { name: 'Correct' }));
            fireEvent.change(screen.getByLabelText('Given name'), { target: { value: 'Given X' } });
            fireEvent.change(screen.getByLabelText('Family name'), {
                target: { value: 'Family X' },
            });
            fireEvent.change(screen.getByLabelText('Display name'), {
                target: { value: 'Display X' },
            });
            fireEvent.change(screen.getByLabelText('Sort name'), { target: { value: '' } });
            fireEvent.click(screen.getByRole('button', { name: 'Save correction' }));
            await confirmInDialog();

            await waitFor(() =>
                expect(api.editAuthorReviewItem).toHaveBeenCalledWith(12, {
                    scope: 'fingerprint',
                    result: {
                        given_name: 'Given X',
                        additional_names: null,
                        family_name: 'Family X',
                        nickname: null,
                        display_name: 'Display X',
                        sort_name: null,
                        kind: 'person',
                    },
                }),
            );
            // The response replaces the detail view...
            expect(await screen.findByText('Edited Display')).toBeInTheDocument();
            // ...but the list keeps the prior item until it is fetched again.
            expect(screen.getByText('Fixture Display B')).toBeInTheDocument();
        } finally {
            restore();
        }
    });

    it('blocks a correction without a display name', async () => {
        renderAt('/queue');
        await openDetail('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Correct' }));
        fireEvent.change(screen.getByLabelText('Display name'), { target: { value: '   ' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save correction' }));

        expect(await screen.findByText('Display name is required.')).toBeInTheDocument();
        expect(api.editAuthorReviewItem).not.toHaveBeenCalled();
        expect(screen.queryByRole('dialog')).toBeNull();
    });
});

describe('the scope in plain words', () => {
    it('carries the later-books explanation in natural Russian', async () => {
        const { default: i18next } = await import('i18next');
        const instance = i18next.createInstance();
        await instance.init({
            lng: 'ru',
            fallbackLng: 'en',
            resources: { ru: { translation: ruTranslation }, en: { translation: enTranslation } },
            keySeparator: false,
            interpolation: { escapeValue: false },
        });
        const previous = i18nHolder.t;
        i18nHolder.t = (key: string, opts?: unknown) =>
            typeof opts === 'string'
                ? instance.t(key, { defaultValue: opts })
                : instance.t(key, (opts ?? {}) as Record<string, unknown>);
        try {
            api.getAuthorReviewItem.mockResolvedValue({ item: detail({ ...secondItem }) });
            renderAt('/queue');
            fireEvent.click(
                await screen.findByRole('button', { name: 'Открыть: Fixture Display B' }),
            );
            await screen.findByRole('region', { name: 'Элемент проверки' });
            for (const label of ['Принять', 'Исправить', 'Оставить как в файле']) {
                expect(screen.getByRole('button', { name: label })).toBeInTheDocument();
            }
            fireEvent.click(screen.getByRole('button', { name: 'Принять' }));
            const help = within(await screen.findByRole('dialog')).getByText(/появятся позже/);
            expect(help).toHaveTextContent(/всем записям с этим именем/i);
            expect(help).toHaveTextContent('4');
        } finally {
            i18nHolder.t = previous;
        }
    });

    it('keeps both locales complete for the review strings', () => {
        const en = enTranslation as Record<string, string>;
        const ru = ruTranslation as Record<string, string>;
        for (const key of [
            'authorReview.accept',
            'authorReview.correct',
            'authorReview.keepAsInFile',
            'authorReview.saveCorrection',
            'authorReview.confirmFingerprint',
            'authorReview.backToQueue',
            'authorReview.invalidItemAddress',
            'authorsTab',
        ]) {
            expect(en[key], `en ${key}`).toBeTruthy();
            expect(ru[key], `ru ${key}`).toBeTruthy();
            expect(ru[key], `ru ${key} is not the English text`).not.toBe(en[key]);
        }
        expect(ru['authorReview.accept']).toBe('Принять');
        expect(ru['authorReview.correct']).toBe('Исправить');
        expect(ru['authorReview.keepAsInFile']).toBe('Оставить как в файле');
        expect(Object.keys(ru).filter((key) => key.startsWith('authorNormalization.'))).toEqual([]);
    });
});

describe('conflicts', () => {
    it('refreshes the detail on a 409 and preserves the typed draft', async () => {
        api.acceptAuthorReviewItem.mockRejectedValue(
            new ApiError('review_conflict', 409, { body: { error: 'review_conflict' } }),
        );
        renderAt('/queue');
        await openDetail('Fixture Display A');
        expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(1);

        fireEvent.click(screen.getByRole('button', { name: 'Correct' }));
        fireEvent.change(screen.getByLabelText('Display name'), {
            target: { value: 'Typed Display' },
        });
        fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
        fireEvent.click(
            await within(await screen.findByRole('dialog')).getByRole('button', {
                name: 'Confirm',
            }),
        );

        expect(await screen.findByRole('status')).toHaveTextContent(
            'Another admin changed this review item. It has been refreshed; your draft is preserved.',
        );
        await waitFor(() => expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(2));
        expect(screen.getByLabelText('Display name')).toHaveValue('Typed Display');
    });

    it('maps other failures to the generic message', async () => {
        api.acceptAuthorReviewItem.mockRejectedValue(
            new ApiError('surprise_code', 500, { body: { error: 'surprise_code' } }),
        );
        renderAt('/queue');
        await openDetail('Fixture Display A');
        fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
        fireEvent.click(
            await within(await screen.findByRole('dialog')).getByRole('button', {
                name: 'Confirm',
            }),
        );

        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent('Action failed.');
        expect(alert).not.toHaveTextContent('surprise_code');
    });
});

describe('privacy', () => {
    it('never sends names or titles to the console on failures', async () => {
        const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
        const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
        api.listAuthorReviewItems.mockRejectedValue(
            new ApiError('private transport message', 500, { body: { error: 'surprise_code' } }),
        );
        renderAt('/queue');

        await screen.findByRole('alert');
        const logged = JSON.stringify([errorSpy.mock.calls, warnSpy.mock.calls]);
        expect(logged).not.toContain('Fixture Display A');
        expect(logged).not.toContain('Fixture Book One');
        expect(screen.queryByText('private transport message')).toBeNull();

        errorSpy.mockRestore();
        warnSpy.mockRestore();
    });
});

describe('layout boundary', () => {
    it('uses the shared admin table query and asks no other width question', () => {
        const queue = readFileSync('src/features/admin/AuthorReviewQueue.tsx', 'utf-8');
        expect(queue).toContain('useMediaQuery(ADMIN_TABLE_WIDE_QUERY)');
        expect(queue.replace('ADMIN_TABLE_WIDE_QUERY', '')).not.toMatch(
            /\((?:min|max)-(?:width|height)\s*:/,
        );
        const detailSource = readFileSync('src/features/admin/AuthorReviewDetail.tsx', 'utf-8');
        expect(detailSource.replace('ADMIN_TABLE_WIDE_QUERY', '')).not.toMatch(
            /\((?:min|max)-(?:width|height)\s*:/,
        );
    });

    it('renders the table when wide and cards when narrow', async () => {
        const restore = stubTableWide(true);
        try {
            const wide = renderAt('/queue');
            await screen.findByText('Fixture Display A');
            expect(screen.getByRole('table')).toBeInTheDocument();
            wide.unmount();
        } finally {
            restore();
        }

        const narrow = renderAt('/queue');
        await screen.findByText('Fixture Display A');
        expect(screen.queryByRole('table')).toBeNull();
        const cards = screen.getByRole('list', { name: 'Review items' });
        expect(within(cards).getAllByRole('listitem')).toHaveLength(2);
        narrow.unmount();
    });
});

describe('the old section address', () => {
    const renderRedirect = (path: string) =>
        render(
            <MemoryRouter initialEntries={[path]}>
                <Routes>
                    <Route
                        path="/admin/author-normalization"
                        element={<LegacyAuthorNormalizationRedirect />}
                    />
                    <Route
                        path="/admin/author-normalization/review/:id"
                        element={<LegacyAuthorReviewRedirect />}
                    />
                    <Route path="/admin/book-scanning" element={<LocationProbe />} />
                    <Route path="/admin/book-scanning/authors/:id" element={<LocationProbe />} />
                </Routes>
            </MemoryRouter>,
        );

    it('lands on the authors tab of scanning, query kept', async () => {
        renderRedirect('/admin/author-normalization?status=closed');
        expect(await screen.findByTestId('location')).toHaveTextContent(
            '/admin/book-scanning?status=closed&tab=authors',
        );
    });

    it('sends an old detail link to the same item in scanning', async () => {
        renderRedirect('/admin/author-normalization/review/12?status=open');
        expect(await screen.findByTestId('location')).toHaveTextContent(
            '/admin/book-scanning/authors/12?status=open',
        );
    });
});

describe('pure payload helpers', () => {
    it('builds a draft from the proposal and an empty one without', () => {
        const fromProposal = draftFromProposal(detail().proposal);
        expect(fromProposal).toEqual({
            given: 'Fixture',
            additional: '',
            family: 'Author',
            nickname: '',
            display: 'Fixture Display A',
            sort: 'Author, Fixture',
            kind: 'person',
        });
        expect(draftFromProposal(null).kind).toBe('unknown');
    });

    it('trims values, sends empty optionals as null and requires a display name', () => {
        const built = buildEditResult({
            given: '  Given  ',
            additional: '',
            family: 'Family',
            nickname: '  ',
            display: 'Display X',
            sort: '',
            kind: 'person',
        });
        expect(built).toEqual({
            result: {
                given_name: 'Given',
                additional_names: null,
                family_name: 'Family',
                nickname: null,
                display_name: 'Display X',
                sort_name: null,
                kind: 'person',
            },
        });
        expect(
            buildEditResult({
                given: '',
                additional: '',
                family: '',
                nickname: '',
                display: '   ',
                sort: '',
                kind: 'person',
            }),
        ).toEqual({ error: 'display_required' });
    });
});

describe('stale modal state and delayed responses', () => {
    const deferred = <T,>() => {
        let resolve!: (value: T) => void;
        let reject!: (reason: unknown) => void;
        const promise = new Promise<T>((yes, no) => {
            resolve = yes;
            reject = no;
        });
        return { promise, resolve, reject };
    };
    const confirmDialog = async () =>
        fireEvent.click(
            within(await screen.findByRole('dialog')).getByRole('button', { name: 'Confirm' }),
        );

    it('discards a delayed action response once its modal is closed', async () => {
        const gate = deferred<{ item: AuthorReviewDetail }>();
        api.acceptAuthorReviewItem.mockReturnValueOnce(gate.promise);
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            const view = await openDetailWide('Fixture Display A');
            fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
            await confirmDialog();

            // The admin closes the modal while the action is still in flight.
            fireEvent.keyDown(document.body, { key: 'Escape' });
            await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

            await act(async () => {
                gate.resolve({ item: detail({ display_name: 'Completed A' }) });
            });
            expect(screen.queryByText('Completed A')).toBeNull();
            expect(view).toHaveFocus();

            // Reopening the item starts from the server, not the stale answer.
            fireEvent.click(view);
            await screen.findByRole('dialog');
            expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(2);
            expect(
                await screen.findByText('Fixture Display A', { selector: 'p' }),
            ).toBeInTheDocument();
        } finally {
            restore();
        }
    });

    it('discards a delayed conflict once its modal is closed', async () => {
        const gate = deferred<{ item: AuthorReviewDetail }>();
        api.acceptAuthorReviewItem.mockReturnValueOnce(gate.promise);
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await openDetailWide('Fixture Display A');
            fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
            await confirmDialog();
            fireEvent.click(screen.getByRole('button', { name: 'Close' }));
            await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

            await act(async () => {
                gate.reject(
                    new ApiError('review_conflict', 409, { body: { error: 'review_conflict' } }),
                );
            });

            expect(screen.queryByRole('status')).toBeNull();
            expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(1);
        } finally {
            restore();
        }
    });

    it('keeps the second item when a delayed detail GET belongs to the first view', async () => {
        // The supported mobile flow the reviewer named: View A, Back while A's
        // GET is pending, then View B — A's late answer must install nothing.
        const gate = deferred<{ item: AuthorReviewDetail }>();
        const detailB = () =>
            detail({
                ...secondItem,
                source: { ...detail().source, display: 'Source B' },
                proposal: { ...detail().proposal!, display_name: 'Proposal B' },
            });
        api.getAuthorReviewItem.mockImplementation(async (id) =>
            id === 11 ? gate.promise : { item: detailB() },
        );
        renderAt('/queue');

        fireEvent.click(await screen.findByRole('button', { name: 'View: Fixture Display A' }));
        fireEvent.click(screen.getByRole('button', { name: 'Back to the queue' }));
        await screen.findByText('Fixture Display A');
        fireEvent.click(screen.getByRole('button', { name: 'View: Fixture Display B' }));
        await screen.findByText('Proposal B');

        await act(async () => {
            gate.resolve({ item: detail() });
        });
        expect(screen.getByText('Proposal B')).toBeInTheDocument();
        expect(screen.queryByText('Source B')).toBeInTheDocument();
        expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(2);
    });

    it('keeps the decisions disabled until the detail is confirmed', async () => {
        let resolveDetail!: (value: { item: AuthorReviewDetail }) => void;
        const gate = new Promise<{ item: AuthorReviewDetail }>((resolve) => {
            resolveDetail = resolve;
        });
        api.getAuthorReviewItem.mockReturnValueOnce(gate);
        renderAt('/queue');

        fireEvent.click(await screen.findByRole('button', { name: 'View: Fixture Display A' }));
        expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();

        await act(async () => {
            resolveDetail({ item: detail() });
        });
        await waitFor(() => expect(screen.getByRole('button', { name: 'Accept' })).toBeEnabled());
    });
});

describe('dialog focus and privacy hardening', () => {
    it('restores focus to the action button after the dialog closes', async () => {
        const user = userEvent.setup();
        renderAt('/queue');
        await openDetail('Fixture Display A');
        const accept = screen.getByRole('button', { name: 'Accept' });
        accept.focus();
        await user.keyboard('{Enter}');
        expect(await screen.findByRole('dialog')).toBeInTheDocument();

        await user.keyboard('{Escape}');
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        expect(accept).toHaveFocus();
    });

    it('never logs loaded names or titles when a scoped action fails', async () => {
        const spies = (['error', 'warn', 'info', 'log'] as const).map((method) =>
            vi.spyOn(console, method).mockImplementation(() => {}),
        );
        api.getAuthorReviewItem.mockResolvedValue({
            item: detail({
                source: { ...detail().source, display: 'Canary Author' },
                linked_books: [{ id: 1, title: 'Canary Book' }],
            }),
        });
        api.acceptAuthorReviewItem.mockRejectedValue(
            new ApiError('surprise_code', 500, { body: { error: 'surprise_code' } }),
        );
        renderAt('/queue');
        await openDetail('Fixture Display A');
        fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
        fireEvent.click(
            await within(await screen.findByRole('dialog')).getByRole('button', {
                name: 'Confirm',
            }),
        );

        expect(await screen.findByRole('alert')).toHaveTextContent('Action failed.');
        const logged = JSON.stringify(spies.map((spy) => spy.mock.calls));
        expect(logged).not.toContain('Canary Author');
        expect(logged).not.toContain('Canary Book');
        spies.forEach((spy) => spy.mockRestore());
    });
});

describe('confirm focus settlement', () => {
    it('returns focus to the action button once a confirmed request settles', async () => {
        const user = userEvent.setup();
        let resolveAccept!: (value: { item: AuthorReviewDetail }) => void;
        const gate = new Promise<{ item: AuthorReviewDetail }>((resolve) => {
            resolveAccept = resolve;
        });
        api.acceptAuthorReviewItem.mockReturnValueOnce(gate);
        renderAt('/queue');
        await openDetail('Fixture Display A');
        const accept = screen.getByRole('button', { name: 'Accept' });
        accept.focus();
        await user.keyboard('{Enter}');
        const dialog = await screen.findByRole('dialog');
        const confirm = within(dialog).getByRole('button', { name: 'Confirm' });
        confirm.focus();
        await user.keyboard('{Enter}');

        // The dialog is closed and the request is in flight: the opener is
        // disabled, so focus cannot land on it yet.
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        expect(accept).toBeDisabled();

        await act(async () => {
            resolveAccept({ item: detail() });
        });
        await waitFor(() => expect(accept).toBeEnabled());
        expect(accept).toHaveFocus();
    });
});
