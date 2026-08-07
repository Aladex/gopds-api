import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router';

import SearchBar from '@/features/catalogue/SearchBar';

// Characterisation tests written before the search panel is rebuilt on shadcn.
// The URLs this panel constructs are the contract between the search box and
// every list route; getting one wrong sends the reader to an empty page with no
// error to explain it.

// react-router is deliberately not mocked: the panel's whole job is to
// produce a URL, so the assertion is on where a real router actually lands.
let currentPath = '';

const PathProbe: React.FC = () => {
    const { pathname, search } = useLocation();
    // Recorded in an effect, not during render: writing to the outside world
    // while rendering is the side effect React asks components not to have.
    React.useEffect(() => {
        currentPath = pathname + search;
    }, [pathname, search]);
    return null;
};

const favState = { fav: false, favEnabled: true, setFavEnabled: vi.fn() };
vi.mock('@/context/FavContext', () => ({ useFav: () => favState }));

const authorState = {
    authorId: '',
    authorName: '',
    setAuthorId: vi.fn(),
    setAuthorName: vi.fn(),
    clearAuthorId: vi.fn(),
};
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

const authState = { updateLang: vi.fn() };
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));

const searchState = {
    languages: ['ru', 'en'],
    selectedLanguage: 'ru',
    setSelectedLanguage: vi.fn(),
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
// under test, so stand in with a plain input that reports typing and Enter,
// plus buttons standing in for picking a suggestion.
vi.mock('@/features/catalogue/AutocompleteSearch', () => ({
    default: ({
        value,
        onChange,
        onEnterPressed,
        onSuggestionSelected,
        disabled,
        placeholder,
    }: {
        value: string;
        onChange: (v: string) => void;
        onEnterPressed: () => void;
        onSuggestionSelected?: (suggestion: {
            value: string;
            type: 'book' | 'author';
            id?: number;
        }) => void;
        disabled?: boolean;
        placeholder?: string;
    }) => (
        <div>
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
            <button
                type="button"
                aria-label="pickBook"
                onClick={() =>
                    onSuggestionSelected?.({ value: 'Война и мир', type: 'book', id: 555 })
                }
            />
            <button
                type="button"
                aria-label="pickAuthor"
                onClick={() =>
                    onSuggestionSelected?.({ value: 'Толстой Лев', type: 'author', id: 42 })
                }
            />
        </div>
    ),
}));

function renderBar(path = '/books/page/1') {
    return render(
        <MemoryRouter initialEntries={[path]}>
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

        await userEvent.click(screen.getByRole('button', { name: 'search' }));

        expect(currentPath).toBe('/books/find/title/%D0%B4%D1%8E%D0%BD%D0%B0/1');
    });

    it('searches by author through /authors', async () => {
        searchState.searchItem = 'Герберт';
        searchState.selectedSearch = 'author';
        renderBar();

        await userEvent.click(screen.getByRole('button', { name: 'search' }));

        expect(currentPath).toMatch(/^\/authors\/.+\/1$/);
    });

    it('percent-encodes the query so slashes cannot break the route', async () => {
        searchState.searchItem = 'а/б';
        renderBar();

        await userEvent.click(screen.getByRole('button', { name: 'search' }));

        expect(currentPath).not.toContain('/а/б/');
        expect(currentPath).toContain('%2F');
    });

    it('does nothing when the query is empty', async () => {
        searchState.searchItem = '   ';
        renderBar();

        await userEvent.click(screen.getByRole('button', { name: 'search' }));

        expect(currentPath).toBe('/books/page/1');
    });

    it('submits on Enter as well as on the button', async () => {
        searchState.searchItem = 'дюна';
        renderBar();

        await userEvent.type(screen.getByLabelText('searchItem'), '{Enter}');

        expect(currentPath).not.toBe('/books/page/1');
    });
});

// A reload or a pasted link lands on an already filtered list. The query lives
// in the URL, so the field can show what is being searched instead of sitting
// blank over a narrowed list.
describe('SearchBar on a cold reload', () => {
    // The mocked context keeps its state in a plain object, so a seed shows up
    // as what the panel asked the context to hold — the input itself is
    // controlled and follows that state in the real chain.
    it('seeds the field from the query a scoped search wrote', async () => {
        renderBar(`/books/find/author/42/1?title=${encodeURIComponent('война')}`);

        await waitFor(() => expect(searchState.setSearchItem).toHaveBeenCalledWith('война'));
    });

    it('seeds the field from a title route', async () => {
        renderBar(`/books/find/title/${encodeURIComponent('дюна')}/3`);

        await waitFor(() => expect(searchState.setSearchItem).toHaveBeenCalledWith('дюна'));
    });

    it('seeds nothing on a plain list', () => {
        renderBar('/books/page/1');

        expect(searchState.setSearchItem).not.toHaveBeenCalled();
        expect(screen.getByLabelText('searchItem')).toHaveValue('');
    });
});

describe('SearchBar in favourites mode', () => {
    it('stays usable while the favourites filter is on', () => {
        // Favourites are a scope like any other now: the backend searches
        // inside them, so a disabled panel would only hide that.
        favState.fav = true;
        renderBar('/books/favorite/1');

        expect(screen.getByRole('button', { name: 'search' })).toBeEnabled();
        expect(screen.getByLabelText('searchItem')).toBeEnabled();
        // Radix marks the trigger disabled for real rather than announcing it
        // through aria-disabled, so keyboard focus skips it too.
        expect(screen.getByRole('combobox')).toBeEnabled();
    });

    it('leaves the panel usable when the filter is off', () => {
        renderBar();

        expect(screen.getByRole('button', { name: 'search' })).toBeEnabled();
        expect(screen.getByLabelText('searchItem')).toBeEnabled();
        expect(screen.getByRole('combobox')).toBeEnabled();
    });

    it('offers the favourites filter only to a reader who has favourites', () => {
        favState.favEnabled = false;
        renderBar();

        expect(screen.getByRole('button', { name: 'showFavourites' })).toBeDisabled();
    });

    it('searches inside favourites without leaving the list', async () => {
        favState.fav = true;
        searchState.searchItem = 'война';
        renderBar('/books/favorite/2');

        await userEvent.click(screen.getByRole('button', { name: 'search' }));

        expect(currentPath).toBe(`/books/favorite/1?title=${encodeURIComponent('война')}`);
    });

    it('carries the query into favourites when the toggle goes on', async () => {
        searchState.searchItem = 'дюна';
        renderBar('/books/page/1');

        await userEvent.click(screen.getByRole('button', { name: 'showFavourites' }));

        expect(currentPath).toBe(`/books/favorite/1?title=${encodeURIComponent('дюна')}`);
    });

    it('hands the query back to a broad search when the toggle goes off', async () => {
        favState.fav = true;
        searchState.searchItem = 'дюна';
        renderBar(`/books/favorite/1?title=${encodeURIComponent('дюна')}`);

        await userEvent.click(screen.getByRole('button', { name: 'showAllBooks' }));

        expect(currentPath).toBe(`/books/find/title/${encodeURIComponent('дюна')}/1`);
    });

    it('keeps the plain swap when there is no query', async () => {
        renderBar('/books/page/1');

        await userEvent.click(screen.getByRole('button', { name: 'showFavourites' }));

        expect(currentPath).toBe('/books/favorite/1');
    });
});

// The books language moved here from the header, where it sat among the site
// chrome while the favourites toggle — the other catalogue filter — was in this
// panel. Both narrow the list, so both belong in the same place.
describe('SearchBar books language', () => {
    beforeEach(() => {
        favState.fav = false;
        favState.favEnabled = true;
        searchState.selectedLanguage = 'ru';
        vi.clearAllMocks();
    });

    it('offers the books language beside the favourites toggle', () => {
        renderBar();

        expect(screen.getByRole('button', { name: /^booksLanguage:/ })).toBeInTheDocument();
    });

    // Until this existed a filter, once set, could not be cleared: every row in
    // the list narrowed the catalogue and none widened it.
    it('can hand the catalogue back with no language filter at all', async () => {
        renderBar();

        await userEvent.click(screen.getByRole('button', { name: /^booksLanguage:/ }));
        await userEvent.click(screen.getByRole('button', { name: 'allLanguages' }));

        expect(authState.updateLang).toHaveBeenCalledWith('all');
        expect(searchState.setSelectedLanguage).toHaveBeenCalledWith('all');
    });

    it('stays available while favourites are on, like the rest of the panel', () => {
        favState.fav = true;
        renderBar('/books/favorite/1');

        expect(screen.getByRole('button', { name: /^booksLanguage:/ })).toBeEnabled();
    });
});

// The scope is read from the route, so any scoped list — author, series,
// genre, collection, favourites — confines the next title search to itself,
// and the query rides the URL where a reload can find it.
describe('SearchBar scoped search', () => {
    it("searches within an author's list instead of starting over", async () => {
        searchState.searchItem = 'война';
        renderBar('/books/find/author/42/3');

        await userEvent.click(screen.getByRole('button', { name: 'search' }));

        expect(currentPath).toBe(
            `/books/find/author/42/1?title=${encodeURIComponent('война')}`,
        );
    });

    it('searches within a genre list', async () => {
        searchState.searchItem = 'война';
        renderBar('/books/find/genre/9/2');

        await userEvent.click(screen.getByRole('button', { name: 'search' }));

        expect(currentPath).toBe(`/books/find/genre/9/1?title=${encodeURIComponent('война')}`);
    });

    it('shows the scope on a scoped list and nowhere else', () => {
        renderBar('/books/find/author/42/1');

        expect(screen.getByText('searchWithinThisAuthor')).toBeInTheDocument();
    });

    it('releasing the scope widens the search but keeps the words', async () => {
        // The seeded field is the common case; the mock context does not
        // re-render on a seed, so the words are given before the render.
        searchState.searchItem = 'х';
        renderBar(`/books/find/author/42/3?title=${encodeURIComponent('х')}`);

        await userEvent.click(screen.getByRole('button', { name: 'searchEverywhere' }));

        // "Search the whole library" means exactly that: same text, no scope.
        expect(currentPath).toBe(`/books/find/title/${encodeURIComponent('х')}/1`);
        expect(searchState.searchItem).toBe('х');
    });

    it('clearing the query leaves the scope alone', async () => {
        renderBar('/books/find/genre/9/1');
        expect(screen.getByText('searchWithinThisGenre')).toBeInTheDocument();

        await userEvent.clear(screen.getByLabelText('searchItem'));

        // The chip still offers "search everywhere" — the scope is on, the
        // field is just empty. Losing the scope here would come as a surprise.
        expect(screen.getByRole('button', { name: 'searchEverywhere' })).toBeInTheDocument();
    });
});

describe('SearchBar suggestion picks', () => {
    it("navigates to the author's books when an author suggestion is picked", async () => {
        renderBar('/books/page/1');

        await userEvent.click(screen.getByRole('button', { name: 'pickAuthor' }));

        expect(currentPath).toBe('/books/find/author/42/1');
    });

    it('pins a picked book by id and keeps its title in the field', async () => {
        renderBar('/books/page/1');

        await userEvent.click(screen.getByRole('button', { name: 'pickBook' }));

        // The field shows the words, the URL pins the exact book — typing the
        // same title later must not silently resurrect the pin.
        expect(searchState.searchItem).toBe('Война и мир');
        expect(currentPath).toBe(
            `/books/find/title/${encodeURIComponent('Война и мир')}/1?book_id=555`,
        );
    });

    it('pins a picked book inside the current scope', async () => {
        renderBar('/books/find/genre/9/2');

        await userEvent.click(screen.getByRole('button', { name: 'pickBook' }));

        expect(currentPath).toBe(
            `/books/find/genre/9/1?title=${encodeURIComponent('Война и мир')}&book_id=555`,
        );
    });
});
