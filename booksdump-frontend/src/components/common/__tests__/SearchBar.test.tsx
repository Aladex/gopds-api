import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';

import SearchBar from '@/components/common/SearchBar';

// Characterisation tests written before the search panel is rebuilt on shadcn.
// The URLs this panel constructs are the contract between the search box and
// every list route; getting one wrong sends the reader to an empty page with no
// error to explain it.

// react-router-dom is deliberately not mocked: the panel's whole job is to
// produce a URL, so the assertion is on where a real router actually lands.
let currentPath = '';

const PathProbe: React.FC = () => {
    const { pathname } = useLocation();
    // Recorded in an effect, not during render: writing to the outside world
    // while rendering is the side effect React asks components not to have.
    React.useEffect(() => {
        currentPath = pathname;
    }, [pathname]);
    return null;
};

const favState = { fav: false, favEnabled: true, setFavEnabled: vi.fn() };
vi.mock('@/context/FavContext', () => ({ useFav: () => favState }));

const authorState = {
    authorId: '',
    setAuthorBook: vi.fn(),
    clearAuthorId: vi.fn(),
    clearAuthorBook: vi.fn(),
};
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

const searchState = {
    searchItem: '',
    setSearchItem: vi.fn((v: string) => {
        searchState.searchItem = v;
    }),
    selectedSearch: 'title',
    setSelectedSearch: vi.fn((v: string) => {
        searchState.selectedSearch = v;
    }),
};
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchState }));

// t must keep a stable identity across renders. useSearchOptions has an effect
// keyed on it, so a mock that returns a fresh function every call spins the
// component in an endless render loop — real react-i18next memoises it.
const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

// The autocomplete talks to the network; the panel's own behaviour is what is
// under test, so stand in with a plain input that reports typing and Enter.
vi.mock('@/components/common/AutocompleteSearch', () => ({
    default: ({
        value,
        onChange,
        onEnterPressed,
        disabled,
        placeholder,
    }: {
        value: string;
        onChange: (v: string) => void;
        onEnterPressed: () => void;
        disabled?: boolean;
        placeholder?: string;
    }) => (
        <input
            aria-label="searchItem"
            placeholder={placeholder}
            value={value}
            disabled={disabled}
            onChange={(e) => onChange(e.target.value)}
            onKeyDown={(e) => {
                if (e.key === 'Enter') onEnterPressed();
            }}
        />
    ),
}));

function renderBar() {
    return render(
        <MemoryRouter initialEntries={['/books/page/1']}>
            <SearchBar />
            <PathProbe />
        </MemoryRouter>,
    );
}

beforeEach(() => {
    vi.clearAllMocks();
    currentPath = '/books/page/1';
    favState.fav = false;
    favState.favEnabled = true;
    searchState.searchItem = '';
    searchState.selectedSearch = 'title';
});

describe('SearchBar routing', () => {
    it('searches by title through /books/find/title', async () => {
        searchState.searchItem = 'дюна';
        renderBar();

        await userEvent.click(screen.getByRole('button', { name: /search/i }));

        expect(currentPath).toBe('/books/find/title/%D0%B4%D1%8E%D0%BD%D0%B0/1');
    });

    it('searches by author through /authors', async () => {
        searchState.searchItem = 'Герберт';
        searchState.selectedSearch = 'author';
        renderBar();

        await userEvent.click(screen.getByRole('button', { name: /search/i }));

        expect(currentPath).toMatch(/^\/authors\/.+\/1$/);
    });

    it('percent-encodes the query so slashes cannot break the route', async () => {
        searchState.searchItem = 'а/б';
        renderBar();

        await userEvent.click(screen.getByRole('button', { name: /search/i }));

        expect(currentPath).not.toContain('/а/б/');
        expect(currentPath).toContain('%2F');
    });

    it('does nothing when the query is empty', async () => {
        searchState.searchItem = '   ';
        renderBar();

        await userEvent.click(screen.getByRole('button', { name: /search/i }));

        expect(currentPath).toBe('/books/page/1');
    });

    it('submits on Enter as well as on the button', async () => {
        searchState.searchItem = 'дюна';
        renderBar();

        await userEvent.type(screen.getByLabelText('searchItem'), '{Enter}');

        expect(currentPath).not.toBe('/books/page/1');
    });
});

describe('SearchBar in favourites mode', () => {
    it('disables the whole panel while the favourites filter is on', () => {
        favState.fav = true;
        renderBar();

        expect(screen.getByRole('button', { name: /search/i })).toBeDisabled();
        expect(screen.getByLabelText('searchItem')).toBeDisabled();
        // Radix marks the trigger disabled for real rather than announcing it
        // through aria-disabled, so keyboard focus skips it too.
        expect(screen.getByRole('combobox')).toBeDisabled();
    });

    it('leaves the panel usable when the filter is off', () => {
        renderBar();

        expect(screen.getByRole('button', { name: /search/i })).toBeEnabled();
        expect(screen.getByLabelText('searchItem')).toBeEnabled();
        expect(screen.getByRole('combobox')).toBeEnabled();
    });

    it('offers the favourites filter only to a reader who has favourites', () => {
        favState.favEnabled = false;
        renderBar();

        expect(screen.getByRole('button', { name: 'showFavourites' })).toBeDisabled();
    });
});
