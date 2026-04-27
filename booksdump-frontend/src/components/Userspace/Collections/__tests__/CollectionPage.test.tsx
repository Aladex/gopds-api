import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';

const sampleDetail = {
    id: 7,
    name: 'Antiutopias',
    books: [
        { id: 100, title: '1984', authors: [{ id: 1, full_name: 'George Orwell' }] },
        { id: 200, title: 'Brave New World', authors: [{ id: 2, full_name: 'Aldous Huxley' }] },
    ],
};

jest.mock('../api', () => ({
    getPublicCollection: jest.fn().mockResolvedValue(sampleDetail),
}));

// Stub the axios-loading config module so jest 27 does not try to parse axios ESM.
jest.mock('../../../../api/config', () => ({
    API_URL: 'http://test',
    fetchWithAuth: { get: jest.fn(), post: jest.fn() },
}));

// react-router-dom is globally mapped to src/__mocks__/react-router-dom.tsx via package.json.
// The default useParams returns {}, so we override it for this test.
jest.mock('react-router-dom', () => {
    const actual = jest.requireActual('react-router-dom');
    return { ...actual, useParams: () => ({ id: '7' }) };
});

jest.mock('react-i18next', () => ({
    useTranslation: () => ({
        t: (_key: string, fallback?: any) => {
            if (typeof fallback === 'object' && fallback?.defaultValue) return fallback.defaultValue;
            return fallback ?? _key;
        },
    }),
}));

// eslint-disable-next-line @typescript-eslint/no-var-requires
const CollectionPage = require('../CollectionPage').default;
const api = require('../api');

const adminFieldsMustNotAppear = [
    'source_url',
    'import_status',
    'import_error',
    'import_stats',
    'is_curated',
    'is_public',
    'external_title',
    'external_author',
    'match_status',
    'match_score',
    'ambiguous',
    'not_found',
    'not found',
];

describe('CollectionPage (public)', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        api.getPublicCollection.mockResolvedValue(sampleDetail);
    });

    it('renders collection name and book titles', async () => {
        render(<CollectionPage />);
        await waitFor(() => expect(api.getPublicCollection).toHaveBeenCalledWith(7));

        expect(await screen.findByText('Antiutopias')).toBeInTheDocument();
        expect(await screen.findByText('1984')).toBeInTheDocument();
        expect(await screen.findByText('Brave New World')).toBeInTheDocument();
    });

    it('renders authors of each book', async () => {
        render(<CollectionPage />);
        await waitFor(() => expect(api.getPublicCollection).toHaveBeenCalled());

        expect(await screen.findByText('George Orwell')).toBeInTheDocument();
        expect(await screen.findByText('Aldous Huxley')).toBeInTheDocument();
    });

    it('does not leak admin or item-level matching metadata', async () => {
        render(<CollectionPage />);
        await waitFor(() => expect(api.getPublicCollection).toHaveBeenCalled());
        await screen.findByText('1984');

        const html = document.body.innerHTML.toLowerCase();
        for (const key of adminFieldsMustNotAppear) {
            expect(html).not.toContain(key.toLowerCase());
        }
    });
});
