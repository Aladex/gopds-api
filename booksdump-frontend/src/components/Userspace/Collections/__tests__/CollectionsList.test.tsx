import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';

const sampleRows = [
    { id: 1, name: 'Antiutopias' },
    { id: 2, name: 'Russian classics' },
];

jest.mock('../api', () => ({
    listPublicCollections: jest.fn().mockResolvedValue({ rows: [], total: 0, page: 1, page_size: 12 }),
}));
const samplePage = { rows: sampleRows, total: sampleRows.length, page: 1, page_size: 12 };

// CollectionsList now imports API_URL from api/config which pulls axios (ESM).
// Stub the config so jest 27 does not try to parse axios.
jest.mock('../../../../api/config', () => ({
    API_URL: 'http://test',
    fetchWithAuth: { get: jest.fn(), post: jest.fn() },
}));

// react-router-dom is globally mapped to src/__mocks__/react-router-dom.tsx via package.json.

jest.mock('react-i18next', () => ({
    useTranslation: () => ({ t: (_key: string, fallback?: any) => fallback ?? _key }),
}));

// eslint-disable-next-line @typescript-eslint/no-var-requires
const CollectionsList = require('../CollectionsList').default;
const api = require('../api');

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
        jest.clearAllMocks();
        api.listPublicCollections.mockResolvedValue(samplePage);
    });

    it('renders cards for each collection', async () => {
        render(<CollectionsList />);

        await waitFor(() => expect(api.listPublicCollections).toHaveBeenCalled());
        expect(await screen.findByText('Antiutopias')).toBeInTheDocument();
        expect(await screen.findByText('Russian classics')).toBeInTheDocument();
    });

    it('shows empty state when there are no public collections', async () => {
        api.listPublicCollections.mockResolvedValue({ rows: [], total: 0, page: 1, page_size: 12 });
        render(<CollectionsList />);
        expect(await screen.findByText(/No collections yet/i)).toBeInTheDocument();
    });

    it('does not leak admin metadata into the rendered DOM', async () => {
        render(<CollectionsList />);
        await waitFor(() => expect(api.listPublicCollections).toHaveBeenCalled());
        await screen.findByText('Antiutopias');

        const html = document.body.innerHTML.toLowerCase();
        for (const key of adminFieldsMustNotAppear) {
            expect(html).not.toContain(key.toLowerCase());
        }
    });
});
