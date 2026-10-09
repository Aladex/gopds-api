import React from 'react';
import { readFileSync } from 'node:fs';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Router, Routes } from 'react-router';
import { UNSAFE_createMemoryHistory as createMemoryHistory } from 'react-router';

import AuthorNormalization from '@/features/admin/AuthorNormalization';
import AuthorReviewQueue from '@/features/admin/AuthorReviewQueue';
import AuthorReviewDetailScreen, {
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
    scope: 'credit',
    credit_id: 31,
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
        items: [listItem(), secondItem],
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
                <Route path="/admin/author-normalization" element={<AuthorNormalization />} />
                <Route
                    path="/admin/author-normalization/review/:id"
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
                    <Route path="/admin/author-normalization" element={<AuthorNormalization />} />
                    <Route
                        path="/admin/author-normalization/review/:id"
                        element={<AuthorReviewDetailScreen />}
                    />
                </Routes>
            </Router>
        );
    };
    return { history, ...render(<Harness />) };
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
    it('shows raw components, the proposal, class, scope and bounded books', async () => {
        renderAt('/queue');
        await openDetail('Fixture Display A');

        const panel = screen.getByRole('region', { name: 'Review item' });
        // Raw source components and flags.
        expect(within(panel).getByText('Fixture Author')).toBeInTheDocument();
        expect(within(panel).getByText('two_surnames')).toBeInTheDocument();
        // Proposal fields and diagnostics.
        expect(screen.getByLabelText('Sort name')).toHaveValue('Author, Fixture');
        expect(within(panel).getByText('initials_split')).toBeInTheDocument();
        expect(within(panel).getByText('ambiguous_initials')).toBeInTheDocument();
        expect(within(panel).getByText('Cyrl')).toBeInTheDocument();
        // Scope and linked books.
        expect(within(panel).getByText('Credit')).toBeInTheDocument();
        expect(within(panel).getByText('Fixture Book One')).toBeInTheDocument();
        expect(within(panel).getByText('Fixture Book Two')).toBeInTheDocument();
    });

    it('replaces the queue with the detail screen and offers the way back', async () => {
        renderAt('/queue');
        await openDetail('Fixture Display A');

        expect(api.getAuthorReviewItem).toHaveBeenCalledWith(11);
        expect(screen.queryByRole('button', { name: 'Next page' })).toBeNull();
        expect(screen.getByRole('button', { name: 'Back to the queue' })).toBeInTheDocument();
    });

    it('deep-links and reloads the detail route without the queue', async () => {
        renderAt('/admin/author-normalization/review/12');

        await screen.findByRole('region', { name: 'Review item' });
        expect(api.getAuthorReviewItem).toHaveBeenCalledWith(12);
        expect(api.listAuthorReviewItems).not.toHaveBeenCalled();
        expect(screen.getByRole('button', { name: 'Back to the queue' })).toBeInTheDocument();
    });

    it('refuses unsafe or non-canonical ids from the address', async () => {
        for (const bad of ['9007199254740993', 'abc', '007', '0', '-11']) {
            const { unmount } = renderAt(`/admin/author-normalization/review/${bad}`);
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
        renderAt('/admin/author-normalization/review/11');
        await screen.findByRole('region', { name: 'Review item' });

        fireEvent.click(screen.getByRole('button', { name: 'Back to the queue' }));

        await waitFor(() =>
            expect(api.listAuthorReviewItems).toHaveBeenLastCalledWith({
                status: 'open',
                limit: 50,
            }),
        );
    });

    it('keeps technical identifiers behind an explicit disclosure', async () => {
        renderAt('/queue');
        await openDetail('Fixture Display A');

        expect(screen.queryByText('a1b2c3d4e5f60718')).toBeNull();
        expect(screen.queryByText('author fixture')).toBeNull();

        fireEvent.click(screen.getByRole('button', { name: 'Show technical details' }));
        expect(screen.getByText('a1b2c3d4e5f60718')).toBeInTheDocument();
        expect(screen.getByText('author fixture')).toBeInTheDocument();

        fireEvent.click(screen.getByRole('button', { name: 'Hide technical details' }));
        expect(screen.queryByText('a1b2c3d4e5f60718')).toBeNull();
    });

    it('offers the edit form when there is no proposal yet', async () => {
        api.getAuthorReviewItem.mockResolvedValue({ item: detail({ proposal: null }) });
        renderAt('/queue');
        await openDetail('Fixture Display A');

        expect(screen.getByText('No local proposal yet.')).toBeInTheDocument();
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

    it('keeps the retry action clear of the dialog close button', async () => {
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await openDetailWide('Fixture Display A');

            // jsdom cannot overlap-check two boxes. What is pinned is the
            // contract that keeps them apart: the close control is an
            // absolutely placed 44px square in the dialog's top-right corner,
            // and the header row that carries the retry button at its right
            // edge reserves right padding at least that wide, at every dialog
            // width and through any wrap.
            const header = document.getElementById('author-review-detail-heading')?.parentElement;
            expect(header).not.toBeNull();
            expect(header).toHaveClass('pr-12');
            expect(screen.getByRole('button', { name: 'Retry normalization' })).toBeInTheDocument();
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

describe('scope and actions', () => {
    it('requires an explicit scope choice and confirmation text before accepting', async () => {
        api.acceptAuthorReviewItem.mockResolvedValue({ item: detail() });
        renderAt('/queue');
        await openDetail('Fixture Display A');

        const accept = screen.getByRole('button', { name: 'Accept' });
        expect(accept).toBeDisabled();

        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
        expect(accept).toBeEnabled();

        fireEvent.click(accept);
        const dialog = await screen.findByRole('dialog');
        expect(
            within(dialog).getByText('Apply this action to this single credit?'),
        ).toBeInTheDocument();

        fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        expect(api.acceptAuthorReviewItem).not.toHaveBeenCalled();

        fireEvent.click(accept);
        fireEvent.click(
            await within(await screen.findByRole('dialog')).getByRole('button', {
                name: 'Confirm',
            }),
        );
        await waitFor(() =>
            expect(api.acceptAuthorReviewItem).toHaveBeenCalledWith(11, { scope: 'credit' }),
        );
    });

    it('names the fingerprint blast radius in the confirmation', async () => {
        api.markAuthorReviewUnresolved.mockResolvedValue({ item: detail() });
        api.getAuthorReviewItem.mockResolvedValue({ item: detail({ ...secondItem }) });
        renderAt('/queue');
        await openDetail('Fixture Display B');

        fireEvent.click(screen.getByRole('button', { name: 'All credits' }));
        fireEvent.click(screen.getByRole('button', { name: 'Mark unresolved' }));
        const dialog = await screen.findByRole('dialog');
        expect(
            within(dialog).getByText('Apply this action to all 4 credits of this fingerprint?'),
        ).toBeInTheDocument();

        fireEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }));
        await waitFor(() =>
            expect(api.markAuthorReviewUnresolved).toHaveBeenCalledWith(12, {
                scope: 'fingerprint',
            }),
        );
    });

    it('chooses the kind once and classifies with that control', async () => {
        api.classifyAuthorReviewItem.mockResolvedValue({ item: detail() });
        renderAt('/queue');
        await openDetail('Fixture Display A');

        // The proposal's kind is person: classify cannot send it.
        expect(screen.queryByRole('group', { name: 'Classify kind' })).toBeNull();
        const kindGroup = screen.getByRole('group', { name: 'Result kind' });
        const classify = screen.getByRole('button', { name: 'Classify' });
        expect(classify).toBeDisabled();

        // A chosen scope does not rescue it: the person kind is not a
        // classification, so Classify stays disabled until the kind is.
        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
        expect(classify).toBeDisabled();

        fireEvent.click(within(kindGroup).getByRole('button', { name: 'Collective' }));
        expect(classify).toBeEnabled();
        fireEvent.click(classify);
        fireEvent.click(
            await within(await screen.findByRole('dialog')).getByRole('button', {
                name: 'Confirm',
            }),
        );

        await waitFor(() =>
            expect(api.classifyAuthorReviewItem).toHaveBeenCalledWith(11, {
                scope: 'credit',
                kind: 'collective',
            }),
        );
    });

    it('sends the exact edit payload with empty optionals as null', async () => {
        api.editAuthorReviewItem.mockResolvedValue({
            item: detail({ display_name: 'Edited Display' }),
        });
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await openDetailWide('Fixture Display A');

            fireEvent.change(screen.getByLabelText('Given name'), { target: { value: 'Given X' } });
            fireEvent.change(screen.getByLabelText('Family name'), {
                target: { value: 'Family X' },
            });
            fireEvent.change(screen.getByLabelText('Display name'), {
                target: { value: 'Display X' },
            });
            fireEvent.change(screen.getByLabelText('Sort name'), { target: { value: '' } });

            fireEvent.click(screen.getByRole('button', { name: 'All credits' }));
            fireEvent.click(screen.getByRole('button', { name: 'Save edit' }));
            fireEvent.click(
                await within(await screen.findByRole('dialog')).getByRole('button', {
                    name: 'Confirm',
                }),
            );

            await waitFor(() =>
                expect(api.editAuthorReviewItem).toHaveBeenCalledWith(11, {
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
            expect(screen.getByText('Fixture Display A')).toBeInTheDocument();
        } finally {
            restore();
        }
    });

    it('blocks an edit without a display name', async () => {
        renderAt('/queue');
        await openDetail('Fixture Display A');

        fireEvent.change(screen.getByLabelText('Display name'), { target: { value: '   ' } });
        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
        fireEvent.click(screen.getByRole('button', { name: 'Save edit' }));

        expect(await screen.findByText('Display name is required.')).toBeInTheDocument();
        expect(api.editAuthorReviewItem).not.toHaveBeenCalled();
        expect(screen.queryByRole('dialog')).toBeNull();
    });

    it('retries the local normalizer without a scope and refreshes the detail', async () => {
        api.retryAuthorReviewNormalization.mockResolvedValue({ item: detail() });
        renderAt('/queue');
        await openDetail('Fixture Display A');
        expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(1);

        fireEvent.click(screen.getByRole('button', { name: 'Retry normalization' }));
        await waitFor(() => expect(api.retryAuthorReviewNormalization).toHaveBeenCalledWith(11));
        await waitFor(() => expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(2));
    });
});

describe('scope explanations', () => {
    it('explains each scope, the future books, and why Accept is disabled', async () => {
        api.getAuthorReviewItem.mockResolvedValue({ item: detail({ ...secondItem }) });
        renderAt('/queue');
        await openDetail('Fixture Display B');

        expect(
            screen.getByText('Apply the decision to this single author credit only.'),
        ).toBeInTheDocument();
        expect(
            screen.getByText(
                'Apply the decision to all 4 credits of the same source name, including books added to the library later.',
            ),
        ).toBeInTheDocument();
        const hint = screen.getByText(
            'Choose what the action applies to first — the action buttons stay disabled until then.',
        );
        expect(hint).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Accept' })).toBeDisabled();

        fireEvent.click(screen.getByRole('button', { name: 'All credits' }));
        expect(
            screen.queryByText(
                'Choose what the action applies to first — the action buttons stay disabled until then.',
            ),
        ).toBeNull();
        expect(screen.getByRole('button', { name: 'Accept' })).toBeEnabled();
    });

    it('carries the future-books explanation in natural Russian', async () => {
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
            // Under the real Russian translator the row button is «Открыть».
            fireEvent.click(
                await screen.findByRole('button', { name: 'Открыть: Fixture Display B' }),
            );
            await screen.findByRole('region', { name: 'Элемент проверки' });

            const help = screen.getByText(/появятся в библиотеке позже/);
            expect(help).toHaveTextContent(/всем записям/i);
            expect(help).toHaveTextContent('4');
        } finally {
            i18nHolder.t = previous;
        }
    });

    it('keeps both locales complete for the new strings', () => {
        const keys = [
            'authorReview.scopeHelp.credit',
            'authorReview.scopeHelp.fingerprint',
            'authorReview.scopeDisabledHint',
            'authorReview.classifyKindHint',
            'authorReview.backToQueue',
            'authorReview.invalidItemAddress',
        ];
        const en = enTranslation as Record<string, string>;
        const ru = ruTranslation as Record<string, string>;
        for (const key of keys) {
            expect(en[key], `en ${key}`).toBeTruthy();
            expect(ru[key], `ru ${key}`).toBeTruthy();
            expect(ru[key], `ru ${key} is not the English text`).not.toBe(en[key]);
        }
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

        fireEvent.change(screen.getByLabelText('Display name'), {
            target: { value: 'Typed Display' },
        });
        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
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

        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
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

describe('dashboard integration', () => {
    it('mounts the queue behind a tab of the normalization screen', async () => {
        renderAt('/admin/author-normalization');

        expect(
            await screen.findByText('No author normalization run yet. Start one below.'),
        ).toBeInTheDocument();
        expect(screen.getByRole('tab', { name: 'Dashboard' })).toBeInTheDocument();
        expect(api.listAuthorReviewItems).not.toHaveBeenCalled();

        await userEvent.click(screen.getByRole('tab', { name: 'Review queue' }));
        await waitFor(() => expect(api.listAuthorReviewItems).toHaveBeenCalled());
        expect(screen.getByText('Fixture Display A')).toBeInTheDocument();
    });

    it('restores the review tab from the address', async () => {
        renderAt('/admin/author-normalization?tab=review');

        await waitFor(() => expect(api.listAuthorReviewItems).toHaveBeenCalled());
        expect(screen.getByText('Fixture Display A')).toBeInTheDocument();
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

            fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
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
            expect(screen.getByLabelText('Display name')).toHaveValue('Fixture Display A');
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

            fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
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

    it('discards a delayed retry refresh once its modal is closed', async () => {
        const gate = deferred<{ item: AuthorReviewDetail }>();
        api.retryAuthorReviewNormalization.mockReturnValueOnce(gate.promise);
        const restore = stubTableWide(true);
        try {
            renderAt('/queue');
            await openDetailWide('Fixture Display A');

            fireEvent.click(screen.getByRole('button', { name: 'Retry normalization' }));
            fireEvent.keyDown(document.body, { key: 'Escape' });
            await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

            await act(async () => {
                gate.resolve({ item: detail() });
            });

            expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(1);
            expect(screen.queryByRole('region', { name: 'Review item' })).toBeNull();
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
        await screen.findByDisplayValue('Proposal B');

        await act(async () => {
            gate.resolve({ item: detail() });
        });
        expect(screen.getByLabelText('Display name')).toHaveValue('Proposal B');
        expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(2);
    });

    it('keeps Retry disabled until the detail is confirmed', async () => {
        let resolveDetail!: (value: { item: AuthorReviewDetail }) => void;
        const gate = new Promise<{ item: AuthorReviewDetail }>((resolve) => {
            resolveDetail = resolve;
        });
        api.getAuthorReviewItem.mockReturnValueOnce(gate);
        renderAt('/queue');

        fireEvent.click(await screen.findByRole('button', { name: 'View: Fixture Display A' }));
        const retry = screen.getByRole('button', { name: 'Retry normalization' });
        expect(retry).toBeDisabled();

        await act(async () => {
            resolveDetail({ item: detail() });
        });
        await waitFor(() => expect(retry).toBeEnabled());
    });
});

describe('dialog focus and privacy hardening', () => {
    it('restores focus to the action button after the dialog closes', async () => {
        const user = userEvent.setup();
        renderAt('/queue');
        await openDetail('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
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

        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
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

        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
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
