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

const Arrow: React.FC = () => {
    const { restoreWords } = useCatalogueSearchContext();
    return (
        <button type="button" onClick={restoreWords}>
            arrow
        </button>
    );
};

describe('CatalogueSearchProvider', () => {
    it.each([
        ['a search', `/search?q=${encodeURIComponent('война')}&type=books`, 'война'],
        ['a search among authors', '/search?q=%D0%BA%D0%B8%D0%BD%D0%B3&type=authors', 'кинг'],
        ['a scoped search', '/author/42?title=%D1%85', 'х'],
        ['a plain list', '/books/page/1', ''],
    ])('puts the page’s own words back in the box on %s', async (_, path, words) => {
        render(
            <MemoryRouter initialEntries={[path]}>
                <CatalogueSearchProvider>
                    <Arrow />
                </CatalogueSearchProvider>
            </MemoryRouter>,
        );
        searchState.setSearchItem.mockClear();

        await userEvent.click(screen.getByRole('button', { name: 'arrow' }));

        expect(searchState.setSearchItem).toHaveBeenCalledWith(words);
    });

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
