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
    source_display_name: 'Fixture Source A',
    proposal_kind: 'person',
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
// Rows are asked for by role, hidden included: an open dialog marks the list
// aria-hidden, and the focused row's evidence repeats the proposed name as
// text once its detail arrives, so a text match would depend on timing.
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

/** Picks a sort order through the queue's Select, as a keyboard user would. */
const chooseSort = async (name: string) => {
    const trigger = screen.getByRole('combobox', { name: 'Sort' });
    fireEvent.keyDown(trigger, { key: 'Enter' });
    fireEvent.click(await screen.findByRole('option', { name }));
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
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });

        expect(api.listAuthorReviewItems).toHaveBeenCalledWith({
            status: 'open',
            order: 'credits',
            view: 'important',
            limit: 50,
        });
        expect(
            screen.getByRole('listitem', { name: 'Fixture Display B', hidden: true }),
        ).toBeInTheDocument();
        // Appending replaces paging: there is no way back, only more.
        expect(screen.getByRole('button', { name: 'More 50' })).toBeEnabled();
        expect(screen.queryByRole('button', { name: 'Previous page' })).toBeNull();
        expect(screen.queryByRole('button', { name: 'Next page' })).toBeNull();
    });

    it('filters by status through the server', async () => {
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });

        fireEvent.click(screen.getByRole('button', { name: 'Closed' }));

        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenCalledWith({
                status: 'closed',
                order: 'credits',
                view: 'important',
                limit: 50,
            }),
        );
    });

    it('more appends the next page', async () => {
        api.listAuthorReviewItems
            .mockResolvedValueOnce({ items: [listItem()], next_cursor: 'cursor-2' })
            .mockResolvedValueOnce({ items: [secondItem], next_cursor: null });
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });

        fireEvent.click(screen.getByRole('button', { name: 'More 50' }));
        await screen.findByRole('listitem', { name: 'Fixture Display B', hidden: true });
        expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
            status: 'open',
            order: 'credits',
            view: 'important',
            cursor: 'cursor-2',
            limit: 50,
        });
        // The earlier rows stay, and so does the focus on the first one.
        expect(
            screen.getByRole('listitem', { name: 'Fixture Display A', hidden: true }),
        ).toBeInTheDocument();
        expect(screen.getByRole('listitem', { name: 'Fixture Display A' })).toHaveAttribute(
            'aria-current',
            'true',
        );
        expect(screen.queryByRole('button', { name: 'More 50' })).toBeNull();
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
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });

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
        expect(screen.queryByRole('button', { name: 'More 50' })).toBeNull();
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

    it('back returns to the queue at the same filter and order', async () => {
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });

        fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                order: 'credits',
                view: 'important',
                limit: 50,
            }),
        );
        await chooseSort('Oldest first');
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                order: 'created',
                view: 'important',
                limit: 50,
            }),
        );

        await openDetail('Fixture Display A');
        fireEvent.click(screen.getByRole('button', { name: 'Back to the queue' }));

        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                order: 'created',
                view: 'important',
                limit: 50,
            }),
        );
        expect(screen.getByRole('button', { name: 'Closed' })).toHaveAttribute(
            'aria-pressed',
            'true',
        );
    });

    it('back from a bare deep-link lands on the queue defaults', async () => {
        renderAt('/admin/book-scanning/authors/11');
        await screen.findByRole('region', { name: 'Review item' });

        fireEvent.click(screen.getByRole('button', { name: 'Back to the queue' }));

        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'credits',
                view: 'important',
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
            expect(
                screen.getByRole('listitem', { name: 'Fixture Display B', hidden: true }),
            ).toBeInTheDocument();
            expect(screen.getByRole('button', { name: 'More 50', hidden: true })).toBeEnabled();
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
            expect(screen.getByRole('button', { name: 'More 50' })).toBeEnabled();
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
            fireEvent.click(screen.getByRole('button', { name: 'Closed', hidden: true }));
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
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });

        fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                order: 'credits',
                view: 'important',
                limit: 50,
            }),
        );
        await chooseSort('Oldest first');
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                order: 'created',
                view: 'important',
                limit: 50,
            }),
        );
        expect(history.location.search).toContain('order=created');

        // Native Back pops the sort entry: the address loses the order and
        // the queue must ask for the closed list in the default order again.
        act(() => history.go(-1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                order: 'credits',
                view: 'important',
                limit: 50,
            }),
        );
        expect(history.location.search).not.toContain('order=');
        expect(
            screen.getByRole('listitem', { name: 'Fixture Display A', hidden: true }),
        ).toBeInTheDocument();
    });

    it('restores the status filter after native browser Back', async () => {
        const { history } = renderWithHistory('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });

        fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                order: 'credits',
                view: 'important',
                limit: 50,
            }),
        );

        // Native Back pops the filter entry: the queue returns to Open in the
        // address and in the data it asks for.
        act(() => history.go(-1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'credits',
                view: 'important',
                limit: 50,
            }),
        );
        expect(screen.getByRole('button', { name: 'Open' })).toHaveAttribute(
            'aria-pressed',
            'true',
        );
    });

    it('follows browser Forward back onto the chosen order', async () => {
        const { history } = renderWithHistory('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });

        await chooseSort('Oldest first');
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'created',
                view: 'important',
                limit: 50,
            }),
        );
        act(() => history.go(-1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'credits',
                view: 'important',
                limit: 50,
            }),
        );

        act(() => history.go(1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'created',
                view: 'important',
                limit: 50,
            }),
        );
    });

    it('carries the address state, not stale local state, into the detail route', async () => {
        const { history } = renderWithHistory('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });

        fireEvent.click(screen.getByRole('button', { name: 'Closed' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'closed',
                order: 'credits',
                view: 'important',
                limit: 50,
            }),
        );
        act(() => history.go(-1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'credits',
                view: 'important',
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
                order: 'credits',
                view: 'important',
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
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });
        expect(
            screen.getByRole('listitem', { name: 'Fixture Display B', hidden: true }),
        ).toBeInTheDocument();
        expect(
            screen.queryByRole('listitem', { name: 'Fixture Display C', hidden: true }),
        ).toBeNull();
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

        await screen.findByRole('listitem', { name: 'Fixture Display B', hidden: true });
        expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
            status: 'open',
            order: 'credits',
            view: 'important',
            cursor: 'cursor-2',
            limit: 50,
        });
        expect(screen.queryByRole('button', { name: 'More 50' })).toBeNull();
    });

    it('shows the empty state when only legacy items are left', async () => {
        api.listAuthorReviewItems.mockResolvedValue({
            items: [legacyCreditItem],
            next_cursor: null,
        });
        renderAt('/queue');

        expect(await screen.findByText('No review items.')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'More 50' })).toBeNull();
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
            expect(
                screen.getByRole('listitem', { name: 'Fixture Display B', hidden: true }),
            ).toBeInTheDocument();
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
        // Counted from here: the queue's inline evidence may have read the
        // focused row before the detail opened, depending on timing.
        expect(api.getAuthorReviewItem).toHaveBeenLastCalledWith(11);
        const readsBefore = api.getAuthorReviewItem.mock.calls.length;

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
        await waitFor(() =>
            expect(api.getAuthorReviewItem.mock.calls.length).toBe(readsBefore + 1),
        );
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

    it('renders one list at every width, with the key hint only when wide', async () => {
        const restore = stubTableWide(true);
        try {
            const wide = renderAt('/queue');
            await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });
            expect(screen.queryByRole('table')).toBeNull();
            const rows = screen.getByRole('list', { name: 'Review items' });
            // The rows themselves; the focused row's book list has items too.
            expect(rows.querySelectorAll(':scope > li')).toHaveLength(2);
            expect(screen.getByText('next')).toBeInTheDocument();
            wide.unmount();
        } finally {
            restore();
        }

        const narrow = renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });
        const cards = screen.getByRole('list', { name: 'Review items' });
        expect(cards.querySelectorAll(':scope > li')).toHaveLength(2);
        expect(screen.queryByText('next')).toBeNull();
        // No keycaps on a phone, not even on the focused row.
        expect(cards.querySelectorAll('kbd')).toHaveLength(0);
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
            // (The queue's own inline evidence reads the focused row too, so
            // only the reads the reopen adds are counted.)
            const readsBefore = api.getAuthorReviewItem.mock.calls.length;
            fireEvent.click(view);
            await screen.findByRole('dialog');
            expect(api.getAuthorReviewItem.mock.calls.length).toBe(readsBefore + 1);
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
            // The queue reads the focused row for its own inline evidence once
            // the focus rests (a 150 ms debounce from the list load). Let that
            // read land first: otherwise it races the baseline below and is
            // counted as a refresh the closed modal never sent.
            const row = screen.getByRole('listitem', { name: 'Fixture Display A' });
            await within(row).findByRole('heading', { name: 'In the file' });
            const readsBefore = api.getAuthorReviewItem.mock.calls.length;

            await act(async () => {
                gate.reject(
                    new ApiError('review_conflict', 409, { body: { error: 'review_conflict' } }),
                );
            });

            expect(screen.queryByRole('status')).toBeNull();
            expect(api.getAuthorReviewItem.mock.calls.length).toBe(readsBefore);
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
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });
        fireEvent.click(screen.getByRole('button', { name: 'View: Fixture Display B' }));
        await screen.findByText('Proposal B');

        await act(async () => {
            gate.resolve({ item: detail() });
        });
        expect(screen.getByText('Proposal B')).toBeInTheDocument();
        expect(screen.queryByText('Source B')).toBeInTheDocument();
        // B was read once; A's reads (the route, maybe the queue's evidence) all hang.
        expect(api.getAuthorReviewItem.mock.calls.filter(([id]) => id === 12)).toHaveLength(1);
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

describe('triage by keyboard', () => {
    const thirdItem = listItem({
        id: 14,
        source_fingerprint: '1122334455667788',
        display_name: 'Fixture Display D',
        source_display_name: 'Fixture Source D',
        credits_count: 1,
        created_at: minutesBefore(20),
    });

    /** A detail whose source line names its id, so the evidence on screen says whose it is. */
    const detailFor = (id: number): AuthorReviewDetail => {
        const base =
            [listItem(), secondItem, thirdItem].find((item) => item.id === id) ?? listItem();
        return detail({
            ...base,
            source: { ...detail().source, display: `Source Shown ${id}` },
        });
    };

    const KEYS: Record<string, string> = {
        KeyJ: 'j',
        KeyK: 'k',
        Digit1: '1',
        Digit2: '2',
        Digit3: '3',
        Enter: 'Enter',
    };
    const press = (code: string, init: Partial<KeyboardEventInit> = {}, target?: Element) =>
        fireEvent.keyDown(target ?? document, { code, key: KEYS[code] ?? code, ...init });

    // Hidden included: an open dialog marks the list aria-hidden, and the
    // tests still ask where the focus sits under it.
    const rowOf = (name: string) => screen.getByRole('listitem', { name, hidden: true });
    const focusedName = () =>
        screen
            .getAllByRole('listitem', { hidden: true })
            .find((row) => row.getAttribute('aria-current') === 'true')
            ?.getAttribute('aria-label');

    beforeEach(() => {
        api.listAuthorReviewItems.mockResolvedValue({
            items: [listItem(), legacyCreditItem, secondItem, thirdItem],
            next_cursor: null,
        });
        api.getAuthorReviewItem.mockImplementation(async (id) => ({ item: detailFor(id) }));
    });

    /** Waits until the focused row shows its own evidence. */
    const evidenceOf = async (name: string, id: number) =>
        within(rowOf(name)).findByText(`Source Shown ${id}`);

    it('the first item is focused with its evidence open', async () => {
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            const first = await screen.findByRole('listitem', { name: 'Fixture Display A' });
            expect(first).toHaveAttribute('aria-current', 'true');
            expect(rowOf('Fixture Display B')).not.toHaveAttribute('aria-current');
            // The row names the file's spelling and the proposal.
            expect(within(first).getByText('Fixture Source A')).toBeInTheDocument();

            await evidenceOf('Fixture Display A', 11);
            expect(api.getAuthorReviewItem).toHaveBeenCalledWith(11);
            expect(within(first).getByText('In the file')).toBeInTheDocument();
            expect(within(first).getByText('Fixture Book One')).toBeInTheDocument();
            expect(
                within(first).getByText('The decision covers all 1 credits with this name'),
            ).toBeInTheDocument();
            // The proposed display name differs from the file's: it is marked.
            const changed = first.querySelectorAll('[data-changed="true"]');
            expect([...changed].map((node) => node.textContent)).toContain('Fixture Display A');

            // Evidence and keycaps belong to the focused row only.
            const second = rowOf('Fixture Display B');
            expect(within(second).queryByText('In the file')).toBeNull();
            expect(first.querySelectorAll('kbd')).toHaveLength(3);
            expect(second.querySelectorAll('kbd')).toHaveLength(0);
        } finally {
            restore();
        }
    });

    it('J and K move the focus and the evidence follows, one detail per resting item', async () => {
        vi.useFakeTimers();
        const settle = (ms: number) =>
            act(async () => {
                await vi.advanceTimersByTimeAsync(ms);
            });
        try {
            renderAt('/queue');
            await settle(0);
            expect(focusedName()).toBe('Fixture Display A');
            expect(api.getAuthorReviewItem).not.toHaveBeenCalled();
            await settle(150);
            expect(api.getAuthorReviewItem.mock.calls).toEqual([[11]]);

            // Two quick steps: the row passed over is never read.
            press('KeyJ');
            await settle(50);
            press('KeyJ');
            expect(focusedName()).toBe('Fixture Display D');
            await settle(149);
            expect(api.getAuthorReviewItem.mock.calls).toEqual([[11]]);
            await settle(1);
            expect(api.getAuthorReviewItem.mock.calls).toEqual([[11], [14]]);
            expect(within(rowOf('Fixture Display D')).getByText('Source Shown 14')).toBeVisible();
            expect(within(rowOf('Fixture Display A')).queryByText('Source Shown 11')).toBeNull();

            // J at the end stays put; K goes back.
            press('KeyJ');
            expect(focusedName()).toBe('Fixture Display D');
            press('KeyK');
            expect(focusedName()).toBe('Fixture Display B');
            await settle(150);
            expect(api.getAuthorReviewItem.mock.calls).toEqual([[11], [14], [12]]);
            expect(within(rowOf('Fixture Display B')).getByText('Source Shown 12')).toBeVisible();

            // Focus moves never re-read the list.
            expect(api.listAuthorReviewItems).toHaveBeenCalledTimes(1);
        } finally {
            vi.useRealTimers();
        }
    });

    it('keys work on a Russian layout', async () => {
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });

        press('KeyJ', { key: 'о' });
        expect(focusedName()).toBe('Fixture Display B');
        press('KeyK', { key: 'л' });
        expect(focusedName()).toBe('Fixture Display A');
        // The letter alone, on another key, does nothing.
        press('KeyO', { key: 'j' });
        expect(focusedName()).toBe('Fixture Display A');
    });

    it('keys ignore fields and dialogs', async () => {
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });
        await evidenceOf('Fixture Display A', 11);

        // Modifiers belong to the browser and the system.
        for (const modifier of ['ctrlKey', 'metaKey', 'altKey'] as const) {
            press('KeyJ', { [modifier]: true });
            expect(focusedName()).toBe('Fixture Display A');
        }

        // Anything typed into a field elsewhere on the page.
        const fields = [
            document.createElement('input'),
            document.createElement('textarea'),
            document.createElement('select'),
            document.createElement('div'),
        ];
        // jsdom does not reflect the contentEditable property; the attribute is what a page carries.
        fields[3].setAttribute('contenteditable', 'true');
        for (const field of fields) {
            document.body.appendChild(field);
            press('KeyJ', {}, field);
            press('Digit1', {}, field);
            expect(focusedName()).toBe('Fixture Display A');
            expect(screen.queryByRole('dialog')).toBeNull();
            field.remove();
        }

        // The inline correction form: typing a J into its field stays there.
        press('Digit2');
        const given = await within(rowOf('Fixture Display A')).findByLabelText('Given name');
        await waitFor(() => expect(given).toHaveFocus());
        press('KeyJ', {}, given);
        expect(focusedName()).toBe('Fixture Display A');
        // And the queue's keys rest while the form is open.
        press('KeyJ');
        expect(focusedName()).toBe('Fixture Display A');
        fireEvent.click(within(rowOf('Fixture Display A')).getByRole('button', { name: 'Cancel' }));
        await waitFor(() =>
            expect(within(rowOf('Fixture Display A')).queryByLabelText('Given name')).toBeNull(),
        );

        // The confirmation dialog: neither J nor another decision gets through.
        press('Digit1');
        const dialog = await screen.findByRole('dialog');
        press('KeyJ');
        press('Digit3');
        press('Digit3', {}, within(dialog).getByRole('button', { name: 'Confirm' }));
        expect(focusedName()).toBe('Fixture Display A');
        expect(screen.getAllByRole('dialog')).toHaveLength(1);
        expect(api.markAuthorReviewUnresolved).not.toHaveBeenCalled();
        expect(api.acceptAuthorReviewItem).not.toHaveBeenCalled();
    });

    it('a closing dialog does not swallow the next key', async () => {
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });

        // Radix keeps a dialog on the page while it fades out, focus trapped
        // inside: an open one still owns the keys, a closing one does not.
        const overlay = document.createElement('div');
        overlay.setAttribute('role', 'dialog');
        overlay.setAttribute('data-state', 'open');
        const inside = document.createElement('button');
        overlay.appendChild(inside);
        document.body.appendChild(overlay);
        try {
            press('KeyJ', {}, inside);
            expect(focusedName()).toBe('Fixture Display A');
            overlay.setAttribute('data-state', 'closed');
            press('KeyJ', {}, inside);
            expect(focusedName()).toBe('Fixture Display B');
            // Enter on the closing dialog's button opens the focused row.
            press('Enter', {}, inside);
            expect(await screen.findByRole('region', { name: 'Review item' })).toBeVisible();
            expect(api.getAuthorReviewItem).toHaveBeenLastCalledWith(12);
        } finally {
            overlay.remove();
        }
    });

    it('1 then Enter accepts the focused item', async () => {
        const user = userEvent.setup();
        api.acceptAuthorReviewItem.mockResolvedValue({ item: detailFor(11) });
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });
        await evidenceOf('Fixture Display A', 11);

        press('Digit1');
        const dialog = await screen.findByRole('dialog');
        await waitFor(() =>
            expect(within(dialog).getByRole('button', { name: 'Confirm' })).toHaveFocus(),
        );
        await user.keyboard('{Enter}');

        await waitFor(() =>
            expect(api.acceptAuthorReviewItem).toHaveBeenCalledWith(11, { scope: 'fingerprint' }),
        );
        await waitFor(() =>
            expect(rowOf('Fixture Display A')).toHaveAttribute('aria-disabled', 'true'),
        );
        // Kept in place, and the focus moved on to the next row — the
        // keyboard focus too, so the next Enter opens that row.
        expect(screen.getAllByRole('listitem')[0]).toBe(rowOf('Fixture Display A'));
        expect(focusedName()).toBe('Fixture Display B');
        await waitFor(() =>
            expect(screen.getByRole('button', { name: 'View: Fixture Display B' })).toHaveFocus(),
        );
        expect(api.acceptAuthorReviewItem).toHaveBeenCalledTimes(1);
    });

    it('focus moves to the next undecided row after a decision', async () => {
        api.markAuthorReviewUnresolved.mockResolvedValue({ item: detailFor(12) });
        api.acceptAuthorReviewItem.mockResolvedValue({ item: detailFor(11) });
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });

        // Decide B first, then come back to A.
        press('KeyJ');
        await evidenceOf('Fixture Display B', 12);
        press('Digit3');
        await confirmInDialog();
        await waitFor(() =>
            expect(rowOf('Fixture Display B')).toHaveAttribute('aria-disabled', 'true'),
        );
        expect(focusedName()).toBe('Fixture Display D');

        press('KeyK');
        press('KeyK');
        expect(focusedName()).toBe('Fixture Display A');
        await evidenceOf('Fixture Display A', 11);
        press('Digit1');
        await confirmInDialog();
        await waitFor(() =>
            expect(rowOf('Fixture Display A')).toHaveAttribute('aria-disabled', 'true'),
        );
        // B is decided already: the focus skips it.
        expect(focusedName()).toBe('Fixture Display D');
    });

    it('accept disabled without a proposal', async () => {
        api.listAuthorReviewItems.mockResolvedValue({
            items: [listItem({ proposal_kind: null }), secondItem],
            next_cursor: null,
        });
        api.getAuthorReviewItem.mockImplementation(async (id) => ({
            item: { ...detailFor(id), proposal: null },
        }));
        renderAt('/queue');
        const first = await screen.findByRole('listitem', { name: 'Fixture Display A' });
        await evidenceOf('Fixture Display A', 11);

        expect(within(first).getByRole('button', { name: 'Accept' })).toBeDisabled();
        expect(within(first).getByRole('button', { name: 'Keep as in the file' })).toBeEnabled();
        press('Digit1');
        await act(async () => {});
        expect(screen.queryByRole('dialog')).toBeNull();
        expect(api.acceptAuthorReviewItem).not.toHaveBeenCalled();
    });

    it('3 keeps as in file through the dialog', async () => {
        api.markAuthorReviewUnresolved.mockResolvedValue({ item: detailFor(11) });
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });
        await evidenceOf('Fixture Display A', 11);

        press('Digit3');
        await confirmInDialog();
        await waitFor(() =>
            expect(api.markAuthorReviewUnresolved).toHaveBeenCalledWith(11, {
                scope: 'fingerprint',
            }),
        );
        expect(api.acceptAuthorReviewItem).not.toHaveBeenCalled();
    });

    it('2 opens the inline correction form', async () => {
        api.editAuthorReviewItem.mockResolvedValue({ item: detailFor(11) });
        renderAt('/queue');
        const first = await screen.findByRole('listitem', { name: 'Fixture Display A' });
        await evidenceOf('Fixture Display A', 11);

        press('Digit2');
        expect(await within(first).findByText('Correction')).toBeInTheDocument();
        const given = within(first).getByLabelText('Given name');
        await waitFor(() => expect(given).toHaveFocus());
        expect(given).toHaveValue('Fixture');

        fireEvent.click(within(first).getByRole('button', { name: 'Save correction' }));
        await confirmInDialog();
        await waitFor(() =>
            expect(api.editAuthorReviewItem).toHaveBeenCalledWith(
                11,
                expect.objectContaining({ scope: 'fingerprint' }),
            ),
        );
    });

    it('a click on a segment of an unfocused row focuses it and arms once loaded', async () => {
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });

        fireEvent.click(
            within(rowOf('Fixture Display B')).getByRole('button', {
                name: 'Keep as in the file',
            }),
        );
        expect(focusedName()).toBe('Fixture Display B');
        const dialog = await screen.findByRole('dialog');
        expect(api.getAuthorReviewItem).toHaveBeenCalledWith(12);
        expect(dialog).toHaveTextContent('all 4 credits');
    });

    it('a decision asked before its detail arrived is dropped once the focus moves on', async () => {
        // The clicked row's detail never arrives; every other row reads at once.
        api.getAuthorReviewItem.mockImplementation((id) =>
            id === 12 ? new Promise(() => {}) : Promise.resolve({ item: detailFor(id) }),
        );
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });

        fireEvent.click(
            within(rowOf('Fixture Display B')).getByRole('button', {
                name: 'Keep as in the file',
            }),
        );
        expect(focusedName()).toBe('Fixture Display B');
        press('KeyJ');
        expect(focusedName()).toBe('Fixture Display D');

        // The next row's detail arrives; the decision meant for B must not land on it.
        await evidenceOf('Fixture Display D', 14);
        await act(async () => {
            await Promise.resolve();
        });
        expect(screen.queryByRole('dialog')).toBeNull();
        expect(api.markAuthorReviewUnresolved).not.toHaveBeenCalled();
    });

    it('sort defaults to credits and switching re-reads with order=created', async () => {
        renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });
        expect(api.listAuthorReviewItems).toHaveBeenCalledWith({
            status: 'open',
            order: 'credits',
            view: 'important',
            limit: 50,
        });
        expect(screen.getByRole('combobox', { name: 'Sort' })).toHaveTextContent(
            'Most books first',
        );

        await chooseSort('Oldest first');
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'created',
                view: 'important',
                limit: 50,
            }),
        );
        expect(api.listAuthorReviewItems).toHaveBeenCalledTimes(2);
    });

    it('the order survives the detail route round trip', async () => {
        const { history } = renderWithHistory('/queue?order=created');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });
        expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
            status: 'open',
            order: 'created',
            view: 'important',
            limit: 50,
        });

        // Narrow: Enter opens the focused item's own route.
        press('Enter');
        await screen.findByRole('region', { name: 'Review item' });
        expect(history.location.pathname).toBe(`${REVIEW_DETAIL_ROUTE}/11`);
        expect(new URLSearchParams(history.location.search).get('order')).toBe('created');

        fireEvent.click(screen.getByRole('button', { name: 'Back to the queue' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'created',
                view: 'important',
                limit: 50,
            }),
        );
        expect(new URLSearchParams(history.location.search).get('order')).toBe('created');
    });

    it('the default order stays out of the address', async () => {
        const { history } = renderWithHistory('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });
        press('Enter');
        await screen.findByRole('region', { name: 'Review item' });
        expect(new URLSearchParams(history.location.search).has('order')).toBe(false);
    });

    it('Enter opens the focused item in the modal when wide', async () => {
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await screen.findByRole('listitem', { name: 'Fixture Display A' });
            press('KeyJ');
            press('Enter');
            const dialog = await screen.findByRole('dialog');
            expect(within(dialog).getByRole('region', { name: 'Review item' })).toBeVisible();
            await waitFor(() => expect(api.getAuthorReviewItem).toHaveBeenCalledWith(12));

            fireEvent.keyDown(document.body, { key: 'Escape' });
            await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
            expect(screen.getByRole('button', { name: 'View: Fixture Display B' })).toHaveFocus();
        } finally {
            restore();
        }
    });

    it('a decision in the card greys the row and hands the focus on', async () => {
        api.markAuthorReviewUnresolved.mockResolvedValue({ item: detailFor(11) });
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await screen.findByRole('listitem', { name: 'Fixture Display A' });
            press('Enter');
            const dialog = await screen.findByRole('dialog');
            await within(dialog).findByRole('region', { name: 'Review item' });
            fireEvent.click(
                await within(dialog).findByRole('button', { name: 'Keep as in the file' }),
            );
            await confirmInDialog();
            await waitFor(() =>
                expect(rowOf('Fixture Display A')).toHaveAttribute('aria-disabled', 'true'),
            );
            expect(focusedName()).toBe('Fixture Display B');

            fireEvent.keyDown(document.body, { key: 'Escape' });
            await waitFor(() => expect(screen.queryAllByRole('dialog')).toHaveLength(0));
            await waitFor(() =>
                expect(
                    screen.getByRole('button', { name: 'View: Fixture Display B' }),
                ).toHaveFocus(),
            );
        } finally {
            restore();
        }
    });

    it('closed filter hides the decision segment', async () => {
        renderAt('/queue?status=closed');
        const first = await screen.findByRole('listitem', { name: 'Fixture Display A' });
        await evidenceOf('Fixture Display A', 11);

        expect(within(first).queryByRole('group', { name: 'Decision' })).toBeNull();
        expect(within(first).queryByRole('button', { name: 'Accept' })).toBeNull();
        press('Digit1');
        press('Digit3');
        await act(async () => {});
        expect(screen.queryByRole('dialog')).toBeNull();
    });

    it('the footer counts the loaded rows of the open total', async () => {
        render(
            <MemoryRouter initialEntries={['/queue']}>
                <Routes>
                    <Route
                        path="/queue"
                        element={<AuthorReviewQueue counts={{ important: 13023, singles: 9409 }} />}
                    />
                </Routes>
            </MemoryRouter>,
        );
        await screen.findByRole('listitem', { name: 'Fixture Display A' });
        expect(screen.getByText(/^3 of 13/)).toBeInTheDocument();
    });

    it('every desktop row keeps its keycap room, so the focus moves no column', async () => {
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await screen.findByRole('listitem', { name: 'Fixture Display A' });
            await evidenceOf('Fixture Display A', 11);

            const room = () =>
                ['Fixture Display A', 'Fixture Display B'].map((name) =>
                    [...rowOf(name).querySelectorAll('[data-keycap]')].map((cap) =>
                        // Room is only kept by a box that is laid out.
                        cap.hasAttribute('hidden') || getComputedStyle(cap).display === 'none'
                            ? 'gone'
                            : cap.tagName === 'KBD' && !cap.classList.contains('invisible')
                              ? 'shown'
                              : cap.classList.contains('invisible')
                                ? 'held'
                                : 'other',
                    ),
                );
            // Shown on the focused row, held empty on the others: the same width.
            expect(room()).toEqual([
                ['shown', 'shown', 'shown'],
                ['held', 'held', 'held'],
            ]);

            press('KeyJ');
            await evidenceOf('Fixture Display B', 12);
            expect(room()).toEqual([
                ['held', 'held', 'held'],
                ['shown', 'shown', 'shown'],
            ]);
        } finally {
            restore();
        }
    });

    it('the heading and the rows share one set of columns', async () => {
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await screen.findByRole('listitem', { name: 'Fixture Display A' });

            // One column template for the whole table, laid out once; the
            // heading and every row take their columns from it.
            const templates = [...document.querySelectorAll('*')].filter((element) =>
                String(element.getAttribute('class')).includes('grid-cols-[minmax'),
            );
            expect(templates).toHaveLength(1);
            const table = templates[0];
            const head = table.querySelector('[data-queue-head]');
            const rows = table.querySelectorAll('[data-queue-row]');
            expect(head).not.toBeNull();
            expect(rows.length).toBeGreaterThan(1);
            for (const grid of [head!, ...rows]) {
                expect(grid).toHaveClass('grid-cols-subgrid');
                // Every level between the template and the cells passes it on.
                for (
                    let level = grid.parentElement;
                    level !== null && level !== table;
                    level = level.parentElement
                ) {
                    expect(level).toHaveClass('grid-cols-subgrid');
                }
            }
        } finally {
            restore();
        }
    });

    it('rapid duplicate decisions and Enters send one decision', async () => {
        // Wide, so a spare Enter opens the modal over the list, not the route.
        const restore = stubTableWide(true);
        try {
            const user = userEvent.setup();
            let settle!: (value: { item: AuthorReviewDetail }) => void;
            api.acceptAuthorReviewItem.mockImplementation(
                () =>
                    new Promise((resolve) => {
                        settle = resolve;
                    }),
            );
            renderAt('/queue');
            await screen.findByRole('listitem', { name: 'Fixture Display A' });
            await evidenceOf('Fixture Display A', 11);

            press('Digit1');
            press('Digit1');
            const dialog = await screen.findByRole('dialog');
            expect(screen.getAllByRole('dialog')).toHaveLength(1);
            await waitFor(() =>
                expect(within(dialog).getByRole('button', { name: 'Confirm' })).toHaveFocus(),
            );
            await user.keyboard('{Enter}{Enter}');
            press('Digit1');
            press('Enter');
            await act(async () => {});

            expect(api.acceptAuthorReviewItem).toHaveBeenCalledTimes(1);
            await act(async () => settle({ item: detailFor(11) }));
            await waitFor(() =>
                expect(rowOf('Fixture Display A')).toHaveAttribute('aria-disabled', 'true'),
            );
            expect(api.acceptAuthorReviewItem).toHaveBeenCalledTimes(1);
            expect(api.acceptAuthorReviewItem).toHaveBeenCalledWith(11, { scope: 'fingerprint' });
        } finally {
            restore();
        }
    });

    it('keys are inert after the queue unmounts', async () => {
        // Every keydown listener the queue puts on the document, and every one taken off.
        const added = vi.spyOn(document, 'addEventListener');
        const removed = vi.spyOn(document, 'removeEventListener');
        const keydown = (spy: typeof added) =>
            spy.mock.calls.filter(([type]) => type === 'keydown').map(([, listener]) => listener);
        const view = renderAt('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });
        await evidenceOf('Fixture Display A', 11);
        const reads = api.getAuthorReviewItem.mock.calls.length;
        view.unmount();

        for (const code of ['KeyJ', 'KeyK', 'Digit1', 'Digit2', 'Digit3', 'Enter']) {
            press(code);
        }
        // Longer than the focus debounce: a live listener would have read a detail by now.
        await act(() => new Promise((resolve) => setTimeout(resolve, 250)));

        expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(reads);
        expect(api.acceptAuthorReviewItem).not.toHaveBeenCalled();
        expect(api.markAuthorReviewUnresolved).not.toHaveBeenCalled();
        expect(screen.queryByRole('dialog')).toBeNull();
        // Nothing of the queue is left listening.
        const left = keydown(added).filter((listener) => !keydown(removed).includes(listener));
        expect(keydown(added).length).toBeGreaterThan(0);
        expect(left).toEqual([]);
        added.mockRestore();
        removed.mockRestore();
    });

    it('keeps both locales complete for the triage strings', () => {
        const en = enTranslation as Record<string, string>;
        const ru = ruTranslation as Record<string, string>;
        expect(ru['authorReview.sort.credits']).toBe('Сначала частые');
        expect(ru['authorReview.more']).toBe('Ещё {{count}}');
        expect(ru['authorReview.shown']).toBe('{{shown}} из {{total}}');
        expect(ru['authorReview.scopeLine_many']).toBe(
            'Решение — для всех {{count}} записей с этим именем',
        );
        for (const key of [
            'authorReview.sort.label',
            'authorReview.sort.credits',
            'authorReview.sort.created',
            'authorReview.more',
            'authorReview.shown',
            'authorReview.decision',
            'authorReview.keepShort',
            'authorReview.keysHint.move',
            'authorReview.keysHint.decide',
            'authorReview.keysHint.open',
        ]) {
            expect(en[key], `en ${key}`).toBeTruthy();
            expect(ru[key], `ru ${key}`).toBeTruthy();
            expect(ru[key], `ru ${key} is not the English text`).not.toBe(en[key]);
        }
    });
});

describe('the important items first, the singles behind their own filter', () => {
    it('lists the important items by default and keeps the default out of the address', async () => {
        const { history } = renderWithHistory('/queue');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });

        expect(api.listAuthorReviewItems).toHaveBeenCalledWith({
            status: 'open',
            order: 'credits',
            view: 'important',
            limit: 50,
        });
        const views = screen.getByRole('group', { name: 'Show' });
        expect(within(views).getByRole('button', { name: 'Important' })).toHaveAttribute(
            'aria-pressed',
            'true',
        );
        expect(within(views).getByRole('button', { name: 'Singles' })).toHaveAttribute(
            'aria-pressed',
            'false',
        );
        expect(history.location.search).not.toContain('view=');
    });

    it('the singles filter re-reads the server, writes the address and Back restores', async () => {
        const { history } = renderWithHistory('/queue?order=created&stage=human');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });

        fireEvent.click(screen.getByRole('button', { name: 'Singles' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'created',
                view: 'singles',
                limit: 50,
            }),
        );
        const params = new URLSearchParams(history.location.search);
        expect(params.get('view')).toBe('singles');
        expect(params.get('order')).toBe('created');
        expect(params.get('stage')).toBe('human');
        expect(screen.getByRole('button', { name: 'Singles' })).toHaveAttribute(
            'aria-pressed',
            'true',
        );

        act(() => history.go(-1));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'created',
                view: 'important',
                limit: 50,
            }),
        );
        expect(history.location.search).not.toContain('view=');
    });

    it('choosing Important from the singles drops the view from the address', async () => {
        const { history } = renderWithHistory('/queue?view=singles&stage=human');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });

        fireEvent.click(screen.getByRole('button', { name: 'Important' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'credits',
                view: 'important',
                limit: 50,
            }),
        );
        const params = new URLSearchParams(history.location.search);
        expect(params.getAll('view')).toEqual([]);
        expect(params.get('stage')).toBe('human');
    });

    it('a cursor stays with its view: more pages read the same view', async () => {
        api.listAuthorReviewItems
            .mockResolvedValueOnce({ items: [listItem()], next_cursor: 'cursor-s2' })
            .mockResolvedValueOnce({ items: [secondItem], next_cursor: null });
        renderAt('/queue?view=singles');
        await screen.findByRole('listitem', { name: 'Fixture Display A', hidden: true });

        fireEvent.click(screen.getByRole('button', { name: 'More 50' }));
        await screen.findByRole('listitem', { name: 'Fixture Display B', hidden: true });
        expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
            status: 'open',
            order: 'credits',
            view: 'singles',
            cursor: 'cursor-s2',
            limit: 50,
        });
    });

    it('an unknown view in the address reads as the important items', async () => {
        renderAt('/queue?view=everything');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });
        expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
            status: 'open',
            order: 'credits',
            view: 'important',
            limit: 50,
        });
    });

    it('the view survives the detail route round trip', async () => {
        const { history } = renderWithHistory('/queue?view=singles');
        await screen.findByRole('listitem', { name: 'Fixture Display A' });

        await openDetail('Fixture Display A');
        expect(new URLSearchParams(history.location.search).get('view')).toBe('singles');

        fireEvent.click(screen.getByRole('button', { name: 'Back to the queue' }));
        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                order: 'credits',
                view: 'singles',
                limit: 50,
            }),
        );
        expect(new URLSearchParams(history.location.search).get('view')).toBe('singles');
    });

    it("the footer's total is the count of the view on screen", async () => {
        render(
            <MemoryRouter initialEntries={['/queue?view=singles']}>
                <Routes>
                    <Route
                        path="/queue"
                        element={<AuthorReviewQueue counts={{ important: 13023, singles: 9409 }} />}
                    />
                </Routes>
            </MemoryRouter>,
        );
        await screen.findByRole('listitem', { name: 'Fixture Display A' });
        // The legacy credit item is not shown, so two rows of the singles' total.
        expect(screen.getByText('2 of 9,409')).toBeInTheDocument();
        expect(screen.queryByText(/of 13,023/)).toBeNull();
    });

    it('the locales name both views', () => {
        const en = enTranslation as Record<string, string>;
        const ru = ruTranslation as Record<string, string>;
        expect(ru['authorReview.view.label']).toBe('Показать');
        expect(ru['authorReview.view.important']).toBe('Важное');
        expect(ru['authorReview.view.singles']).toBe('Одиночные');
        expect(en['authorReview.view.label']).toBe('Show');
        expect(en['authorReview.view.important']).toBe('Important');
        expect(en['authorReview.view.singles']).toBe('Singles');
    });
});
