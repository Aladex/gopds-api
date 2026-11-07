import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router';

import CollectionsList from '@/features/collections/CollectionsList';
import { PAGE_TURN, scrollToLoadedList } from '@/shared/layout/landOnList';
import * as api from '@/api/collections';

const sampleRows = [
    { id: 1, name: 'Antiutopias' },
    { id: 2, name: 'Russian classics' },
];

vi.mock('@/api/collections', () => ({
    listPublicCollections: vi
        .fn()
        .mockResolvedValue({ rows: [], total: 0, page: 1, page_size: 12 }),
}));
const samplePage = { rows: sampleRows, total: sampleRows.length, page: 1, page_size: 12 };

vi.mock('@/shared/layout/landOnList', async () => ({
    ...(await vi.importActual<typeof import('@/shared/layout/landOnList')>(
        '@/shared/layout/landOnList',
    )),
    scrollToLoadedList: vi.fn(),
}));

// Stub the API config so the component never reaches the network.
vi.mock('@/api/config', () => ({
    API_URL: 'http://test',
    fetchWithAuth: { get: vi.fn(), post: vi.fn() },
}));

// A stable t: useTranslation must not hand back a fresh function each render,
// or effects keyed on it loop forever.
const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'en' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const listPublicCollections = vi.mocked(api.listPublicCollections);

/** Stands in for the collection screen so a click can be seen to arrive. */
const CollectionView = () => <div>collection screen</div>;

// The component renders router Links, so it needs a real router context. The
// routes are spelled out the way privateRoutes registers them, so a card's link
// is checked against the address the application actually serves.
const renderList = (path = '/collections') =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <Routes>
                <Route path="/collections" element={<CollectionsList />} />
                <Route path="/collections/page/:page" element={<CollectionsList />} />
                <Route path="/collections/:id/page/:page" element={<CollectionView />} />
            </Routes>
        </MemoryRouter>,
    );

const adminFieldsMustNotAppear = [
    'source_url',
    'import_status',
    'import_error',
    'imported_at',
    'import_stats',
    'is_curated',
    'is_public',
    'user_id',
    'external_title',
    'external_author',
    'match_status',
    'match_score',
    'ambiguous',
    'not_found',
    'not found',
];

describe('CollectionsList (public)', () => {
    it('asks for thumbnails and draws a tile for one that fails', async () => {
        listPublicCollections.mockResolvedValueOnce({
            ...samplePage,
            rows: [
                {
                    id: 1,
                    name: 'Antiutopias',
                    cover_books: [
                        { id: 7, path: 'fb2.zip', filename: 'a.fb2', cover: true, title: 'A' },
                        { id: 8, path: 'fb2.zip', filename: 'b.fb2', cover: true, title: 'B' },
                    ],
                },
            ],
        });
        const { container } = renderList();

        await waitFor(() => expect(container.querySelectorAll('img')).toHaveLength(2));
        const [first, second] = Array.from(container.querySelectorAll('img'));
        expect(first).toHaveAttribute('src', 'http://test/books-thumbs/fb2-zip/a-fb2.jpg');
        expect(second).toHaveAttribute('src', 'http://test/books-thumbs/fb2-zip/b-fb2.jpg');

        fireEvent.error(first);

        expect(container.querySelectorAll('img')).toHaveLength(1);
        expect(container.querySelector('img')).toHaveAttribute(
            'src',
            'http://test/books-thumbs/fb2-zip/b-fb2.jpg',
        );
    });

    beforeEach(() => {
        vi.clearAllMocks();
        listPublicCollections.mockResolvedValue(samplePage);
        // jsdom has no layout and so no scrollTo; the component calls it after
        // every load to put the reader at the top of the new page.
        window.scrollTo = vi.fn();
    });

    it('opens a cold page at the top and a turned page on the list', async () => {
        const Turn: React.FC = () => {
            const navigate = useNavigate();
            return (
                <button
                    type="button"
                    onClick={() => navigate('/collections/page/2', { state: PAGE_TURN })}
                >
                    turn
                </button>
            );
        };
        render(
            <MemoryRouter initialEntries={['/collections/page/1']}>
                <Turn />
                <Routes>
                    <Route path="/collections/page/:page" element={<CollectionsList />} />
                </Routes>
            </MemoryRouter>,
        );
        await waitFor(() => expect(scrollToLoadedList).toHaveBeenLastCalledWith(false));

        await userEvent.click(screen.getByRole('button', { name: 'turn' }));

        await waitFor(() => expect(scrollToLoadedList).toHaveBeenLastCalledWith(true));
    });

    it('asks for the first page when the route carries no page number', async () => {
        renderList();
        await waitFor(() => expect(listPublicCollections).toHaveBeenCalledWith(1, 12));
    });

    it('asks for the page named in the route', async () => {
        renderList('/collections/page/3');
        await waitFor(() => expect(listPublicCollections).toHaveBeenCalledWith(3, 12));
    });

    it('falls back to the first page when the route page is not a number', async () => {
        renderList('/collections/page/nonsense');
        await waitFor(() => expect(listPublicCollections).toHaveBeenCalledWith(1, 12));
    });

    it('renders cards for each collection', async () => {
        renderList();

        await waitFor(() => expect(listPublicCollections).toHaveBeenCalled());
        expect(await screen.findByText('Antiutopias')).toBeInTheDocument();
        expect(await screen.findByText('Russian classics')).toBeInTheDocument();
    });

    it('shows empty state when there are no public collections', async () => {
        listPublicCollections.mockResolvedValue({ rows: [], total: 0, page: 1, page_size: 12 });
        renderList();
        expect(await screen.findByText(/No collections yet/i)).toBeInTheDocument();
    });

    it('shows the failure instead of an empty list when the request fails', async () => {
        listPublicCollections.mockRejectedValue(new Error('service unavailable'));
        renderList();

        expect(await screen.findByRole('alert')).toHaveTextContent('service unavailable');
        expect(screen.queryByText(/No collections yet/i)).not.toBeInTheDocument();
    });

    it('still reports a failure that carries no message', async () => {
        listPublicCollections.mockRejectedValue({});
        renderList();

        expect(await screen.findByRole('alert')).toBeInTheDocument();
    });

    it('opens the collection when a card is clicked', async () => {
        renderList();

        await userEvent.click(await screen.findByText('Antiutopias'));

        expect(await screen.findByText('collection screen')).toBeInTheDocument();
    });

    it('links each card at the first page of that collection', async () => {
        renderList();

        const link = (await screen.findByText('Russian classics')).closest('a');
        expect(link).toHaveAttribute('href', '/collections/2/page/1');
    });

    it('pages when the total does not fit on one page', async () => {
        listPublicCollections.mockResolvedValue({ ...samplePage, total: 40 });
        renderList();

        await screen.findByText('Antiutopias');
        expect(await screen.findByRole('navigation')).toBeInTheDocument();
    });

    it('does not page when everything fits', async () => {
        renderList();

        await screen.findByText('Antiutopias');
        expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
    });

    it('does not leak admin metadata into the rendered DOM', async () => {
        renderList();
        await waitFor(() => expect(listPublicCollections).toHaveBeenCalled());
        await screen.findByText('Antiutopias');

        const html = document.body.innerHTML.toLowerCase();
        for (const key of adminFieldsMustNotAppear) {
            expect(html).not.toContain(key.toLowerCase());
        }
    });
});
