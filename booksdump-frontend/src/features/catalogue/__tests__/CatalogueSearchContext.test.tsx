import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import {
    CatalogueSearchProvider,
    useCatalogueSearchContext,
} from '@/features/catalogue/CatalogueSearchContext';

// The chip, the field and the picker sit in different places on the page and
// have to agree about one thing: whether the scope is on. That agreement is what
// the provider is for.

const translation = { t: (key: string) => key };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const favState = { fav: false, favEnabled: true };
vi.mock('@/context/FavContext', () => ({ useFav: () => favState }));

const authorState = {
    authorId: '',
    authorName: '',
    setAuthorId: vi.fn(),
    setAuthorName: vi.fn(),
    clearAuthorId: vi.fn(),
};
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

const searchState = {
    searchItem: '',
    setSearchItem: vi.fn(),
    selectedSearch: 'title',
    setSelectedSearch: vi.fn(),
    scopeName: '',
};
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchState }));

const Chip: React.FC = () => {
    const { releaseScope } = useCatalogueSearchContext();
    return (
        <button type="button" onClick={releaseScope}>
            release
        </button>
    );
};

const Field: React.FC = () => {
    const { suggestionScope } = useCatalogueSearchContext();
    return <span data-testid="field">{suggestionScope?.kind ?? 'whole library'}</span>;
};

describe('CatalogueSearchProvider', () => {
    it('gives every consumer the one scope', async () => {
        render(
            <MemoryRouter initialEntries={['/books/find/genre/9/1']}>
                <CatalogueSearchProvider>
                    <Chip />
                    <Field />
                </CatalogueSearchProvider>
            </MemoryRouter>,
        );
        expect(screen.getByTestId('field')).toHaveTextContent('genre');

        await userEvent.click(screen.getByRole('button', { name: 'release' }));

        expect(screen.getByTestId('field')).toHaveTextContent('whole library');
    });

    it('refuses a consumer with no provider, rather than a scope of its own', () => {
        const quiet = vi.spyOn(console, 'error').mockImplementation(() => {});

        expect(() =>
            render(
                <MemoryRouter>
                    <Field />
                </MemoryRouter>,
            ),
        ).toThrow(/CatalogueSearchProvider/);

        quiet.mockRestore();
    });
});
