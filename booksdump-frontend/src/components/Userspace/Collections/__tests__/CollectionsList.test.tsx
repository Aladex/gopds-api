import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

import CollectionsList from '@/components/Userspace/Collections/CollectionsList';
import * as api from '@/api/collections';

const sampleRows = [
    { id: 1, name: 'Antiutopias' },
    { id: 2, name: 'Russian classics' },
];

vi.mock('@/api/collections', () => ({
    listPublicCollections: vi.fn().mockResolvedValue({ rows: [], total: 0, page: 1, page_size: 12 }),
}));
const samplePage = { rows: sampleRows, total: sampleRows.length, page: 1, page_size: 12 };

// Stub the API config so the component never reaches the network.
vi.mock('@/api/config', () => ({
    API_URL: 'http://test',
    fetchWithAuth: { get: vi.fn(), post: vi.fn() },
}));

vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: (_key: string, fallback?: any) => fallback ?? _key }),
}));

const listPublicCollections = vi.mocked(api.listPublicCollections);

// The component renders router Links, so it needs a real router context.
const renderList = () => render(<CollectionsList />, { wrapper: MemoryRouter });

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
    beforeEach(() => {
        vi.clearAllMocks();
        listPublicCollections.mockResolvedValue(samplePage);
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
