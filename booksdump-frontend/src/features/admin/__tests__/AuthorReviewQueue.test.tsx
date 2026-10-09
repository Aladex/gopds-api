import React from 'react';
import { readFileSync } from 'node:fs';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import AuthorNormalization from '@/features/admin/AuthorNormalization';
import AuthorReviewQueue, {
    buildEditResult,
    draftFromProposal,
} from '@/features/admin/AuthorReviewQueue';
import * as adminApi from '@/api/admin';
import type { AuthorReviewDetail, AuthorReviewListItem } from '@/api/admin';
import { ApiError } from '@/api/errors';
import { ADMIN_TABLE_WIDE_QUERY } from '@/shared/layout/breakpoints';

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
    api.listAuthorReviewItems.mockResolvedValue({
        items: [listItem(), secondItem],
        next_cursor: 'cursor-2',
    });
    api.getAuthorReviewItem.mockResolvedValue({ item: detail() });
});

const openDetail = async (name: string) => {
    fireEvent.click(await screen.findByRole('button', { name: `View: ${name}` }));
    await screen.findByRole('region', { name: 'Review item' });
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
        render(<AuthorReviewQueue />);
        await screen.findByText('Fixture Display A');

        expect(api.listAuthorReviewItems).toHaveBeenCalledWith({ status: 'open', limit: 50 });
        expect(screen.getByText('Fixture Display B')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Next page' })).toBeEnabled();
    });

    it('filters by status through the server', async () => {
        render(<AuthorReviewQueue />);
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
        render(<AuthorReviewQueue />);
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
        render(<AuthorReviewQueue />);

        expect(await screen.findByText('No review items.')).toBeInTheDocument();
    });

    it('renders the loading state before the first answer', () => {
        api.listAuthorReviewItems.mockReturnValue(new Promise(() => {}));
        render(<AuthorReviewQueue />);

        expect(screen.getByText('Loading...')).toBeInTheDocument();
    });

    it('shows a localized error, never the raw transport text', async () => {
        api.listAuthorReviewItems.mockRejectedValue(
            new ApiError('private transport message', 500, { body: { error: 'surprise_code' } }),
        );
        render(<AuthorReviewQueue />);

        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent('Action failed.');
        expect(screen.queryByText('private transport message')).toBeNull();
    });

    it('refuses list ids that JSON parsing rounded', async () => {
        api.listAuthorReviewItems.mockResolvedValue({
            items: [listItem({ id: JSON.parse('9007199254740993') as number })],
            next_cursor: null,
        });
        render(<AuthorReviewQueue />);
        await screen.findByText('Fixture Display A');

        const view = screen.getByRole('button', { name: 'View: Fixture Display A' });
        expect(view).toBeDisabled();
        fireEvent.click(view);
        expect(api.getAuthorReviewItem).not.toHaveBeenCalled();
    });
});

describe('detail', () => {
    it('shows raw components, the proposal, class, scope and bounded books', async () => {
        render(<AuthorReviewQueue />);
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

    it('keeps technical identifiers behind an explicit disclosure', async () => {
        render(<AuthorReviewQueue />);
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
        render(<AuthorReviewQueue />);
        await openDetail('Fixture Display A');

        expect(screen.getByText('No local proposal yet.')).toBeInTheDocument();
        expect(screen.getByLabelText('Display name')).toHaveValue('');
    });
});

describe('scope and actions', () => {
    it('requires an explicit scope choice and confirmation text before accepting', async () => {
        api.acceptAuthorReviewItem.mockResolvedValue({ item: detail() });
        render(<AuthorReviewQueue />);
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
        render(<AuthorReviewQueue />);
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

    it('sends the exact classify payload', async () => {
        api.classifyAuthorReviewItem.mockResolvedValue({ item: detail() });
        render(<AuthorReviewQueue />);
        await openDetail('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
        const classifyGroup = screen.getByRole('group', { name: 'Classify kind' });
        fireEvent.click(within(classifyGroup).getByRole('button', { name: 'Collective' }));
        fireEvent.click(screen.getByRole('button', { name: 'Classify' }));
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
        render(<AuthorReviewQueue />);
        await openDetail('Fixture Display A');

        fireEvent.change(screen.getByLabelText('Given name'), { target: { value: 'Given X' } });
        fireEvent.change(screen.getByLabelText('Family name'), { target: { value: 'Family X' } });
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
    });

    it('blocks an edit without a display name', async () => {
        render(<AuthorReviewQueue />);
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
        render(<AuthorReviewQueue />);
        await openDetail('Fixture Display A');
        expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(1);

        fireEvent.click(screen.getByRole('button', { name: 'Retry normalization' }));
        await waitFor(() => expect(api.retryAuthorReviewNormalization).toHaveBeenCalledWith(11));
        await waitFor(() => expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(2));
    });
});

describe('conflicts', () => {
    it('refreshes the detail on a 409 and preserves the typed draft', async () => {
        api.acceptAuthorReviewItem.mockRejectedValue(
            new ApiError('review_conflict', 409, { body: { error: 'review_conflict' } }),
        );
        render(<AuthorReviewQueue />);
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
        render(<AuthorReviewQueue />);
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
        render(<AuthorReviewQueue />);

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
    });

    it('renders the table when wide and cards when narrow', async () => {
        const restore = stubTableWide(true);
        try {
            const wide = render(<AuthorReviewQueue />);
            await screen.findByText('Fixture Display A');
            expect(screen.getByRole('table')).toBeInTheDocument();
            wide.unmount();
        } finally {
            restore();
        }

        const narrow = render(<AuthorReviewQueue />);
        await screen.findByText('Fixture Display A');
        expect(screen.queryByRole('table')).toBeNull();
        const cards = screen.getByRole('list', { name: 'Review items' });
        expect(within(cards).getAllByRole('listitem')).toHaveLength(2);
        narrow.unmount();
    });
});

describe('dashboard integration', () => {
    it('mounts the queue behind a tab of the normalization screen', async () => {
        render(<AuthorNormalization />);

        expect(
            await screen.findByText('No author normalization run yet. Start one below.'),
        ).toBeInTheDocument();
        expect(screen.getByRole('tab', { name: 'Dashboard' })).toBeInTheDocument();
        expect(api.listAuthorReviewItems).not.toHaveBeenCalled();

        await userEvent.click(screen.getByRole('tab', { name: 'Review queue' }));
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

describe('superseded selections and delayed responses', () => {
    const deferred = <T,>() => {
        let resolve!: (value: T) => void;
        let reject!: (reason: unknown) => void;
        const promise = new Promise<T>((yes, no) => {
            resolve = yes;
            reject = no;
        });
        return { promise, resolve, reject };
    };
    const detailB = () =>
        detail({
            ...secondItem,
            source: { ...detail().source, display: 'Source B' },
            proposal: { ...detail().proposal!, display_name: 'Proposal B' },
        });
    const confirmDialog = async () =>
        fireEvent.click(
            within(await screen.findByRole('dialog')).getByRole('button', { name: 'Confirm' }),
        );

    it('discards a delayed action response that belongs to a superseded selection', async () => {
        const gate = deferred<{ item: AuthorReviewDetail }>();
        api.getAuthorReviewItem.mockImplementation(async (id) => ({
            item: id === 11 ? detail() : detailB(),
        }));
        api.acceptAuthorReviewItem
            .mockReturnValueOnce(gate.promise)
            .mockResolvedValue({ item: detailB() });
        render(<AuthorReviewQueue />);
        await openDetail('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
        fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
        await confirmDialog();

        // While A's action is in flight, another item is selected.
        fireEvent.click(screen.getByRole('button', { name: 'View: Fixture Display B' }));
        await screen.findByDisplayValue('Proposal B');

        await act(async () => {
            gate.resolve({ item: detail({ display_name: 'Completed A' }) });
        });
        const panel = screen.getByRole('region', { name: 'Review item' });
        expect(within(panel).queryByText('Completed A')).toBeNull();
        expect(within(panel).getByText('Source B')).toBeInTheDocument();

        // The next confirmation belongs to B: its blast radius and its target.
        fireEvent.click(screen.getByRole('button', { name: 'All credits' }));
        fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
        const dialog = await screen.findByRole('dialog');
        expect(
            within(dialog).getByText('Apply this action to all 4 credits of this fingerprint?'),
        ).toBeInTheDocument();
        fireEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }));

        await waitFor(() =>
            expect(api.acceptAuthorReviewItem).toHaveBeenLastCalledWith(12, {
                scope: 'fingerprint',
            }),
        );
    });

    it('discards a delayed conflict for a superseded selection', async () => {
        const gate = deferred<{ item: AuthorReviewDetail }>();
        api.getAuthorReviewItem.mockImplementation(async (id) => ({
            item: id === 11 ? detail() : detailB(),
        }));
        api.acceptAuthorReviewItem.mockReturnValueOnce(gate.promise);
        render(<AuthorReviewQueue />);
        await openDetail('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
        fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
        await confirmDialog();

        fireEvent.click(screen.getByRole('button', { name: 'View: Fixture Display B' }));
        await screen.findByDisplayValue('Proposal B');

        await act(async () => {
            gate.reject(
                new ApiError('review_conflict', 409, { body: { error: 'review_conflict' } }),
            );
        });

        await waitFor(() =>
            expect(screen.getByRole('button', { name: 'Retry normalization' })).toBeEnabled(),
        );
        // The old item's conflict neither notifies nor refreshes A over B.
        expect(screen.queryByRole('status')).toBeNull();
        expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(2);
        expect(
            within(screen.getByRole('region', { name: 'Review item' })).getByText('Source B'),
        ).toBeInTheDocument();
    });

    it('discards a delayed retry refresh for a superseded selection', async () => {
        const gate = deferred<{ item: AuthorReviewDetail }>();
        api.getAuthorReviewItem.mockImplementation(async (id) => ({
            item: id === 11 ? detail() : detailB(),
        }));
        api.retryAuthorReviewNormalization.mockReturnValueOnce(gate.promise);
        render(<AuthorReviewQueue />);
        await openDetail('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Retry normalization' }));
        fireEvent.click(screen.getByRole('button', { name: 'View: Fixture Display B' }));
        await screen.findByDisplayValue('Proposal B');

        await act(async () => {
            gate.resolve({ item: detail() });
        });

        await waitFor(() =>
            expect(screen.getByRole('button', { name: 'Retry normalization' })).toBeEnabled(),
        );
        expect(api.getAuthorReviewItem).toHaveBeenCalledTimes(2);
        expect(
            within(screen.getByRole('region', { name: 'Review item' })).getByText('Source B'),
        ).toBeInTheDocument();
    });

    it('keeps Retry disabled until the detail is confirmed', async () => {
        let resolveDetail!: (value: { item: AuthorReviewDetail }) => void;
        const gate = new Promise<{ item: AuthorReviewDetail }>((resolve) => {
            resolveDetail = resolve;
        });
        api.getAuthorReviewItem.mockReturnValueOnce(gate);
        render(<AuthorReviewQueue />);

        fireEvent.click(await screen.findByRole('button', { name: 'View: Fixture Display A' }));
        const retry = screen.getByRole('button', { name: 'Retry normalization' });
        expect(retry).toBeDisabled();

        await act(async () => {
            resolveDetail({ item: detail() });
        });
        await waitFor(() => expect(retry).toBeEnabled());
    });

    it('preserves the latest explicit selection when detail GETs race', async () => {
        const old = deferred<{ item: AuthorReviewDetail }>();
        api.getAuthorReviewItem.mockReturnValueOnce(old.promise).mockResolvedValueOnce({
            item: detailB(),
        });
        render(<AuthorReviewQueue />);

        fireEvent.click(await screen.findByRole('button', { name: 'View: Fixture Display A' }));
        fireEvent.click(screen.getByRole('button', { name: 'View: Fixture Display B' }));
        await screen.findByDisplayValue('Proposal B');

        await act(async () => {
            old.resolve({ item: detail() });
        });
        expect(screen.getByLabelText('Display name')).toHaveValue('Proposal B');
    });
});

describe('dialog focus and privacy hardening', () => {
    it('restores focus to the action button after the dialog closes', async () => {
        const user = userEvent.setup();
        render(<AuthorReviewQueue />);
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
        render(<AuthorReviewQueue />);
        await openDetail('Fixture Display A');

        fireEvent.click(screen.getByRole('button', { name: 'Single credit' }));
        fireEvent.click(screen.getByRole('button', { name: 'Accept' }));
        fireEvent.click(
            within(await screen.findByRole('dialog')).getByRole('button', { name: 'Confirm' }),
        );

        expect(await screen.findByRole('alert')).toHaveTextContent('Action failed.');
        const logged = JSON.stringify(spies.map((spy) => spy.mock.calls));
        expect(logged).not.toContain('Canary Author');
        expect(logged).not.toContain('Canary Book');
        spies.forEach((spy) => spy.mockRestore());
    });
});
