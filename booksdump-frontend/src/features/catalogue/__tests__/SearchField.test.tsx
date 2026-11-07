import React from 'react';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router';

import { CatalogueSearchProvider } from '@/features/catalogue/CatalogueSearchContext';
import HeaderSearchField from '@/features/catalogue/HeaderSearchField';
import ListLine from '@/features/catalogue/ListLine';
import { CONTENT_COLUMN } from '@/shared/layout/breakpoints';

// The URLs the field constructs are the contract between the search box and
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
};
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchState }));

// t must keep a stable identity across renders. useSearchOptions has an effect
// keyed on it, so a mock that returns a fresh function every call spins the
// component in an endless render loop — real react-i18next memoises it.
const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

// The autocomplete talks to the network; the panel's own behaviour is what is
// under test, so stand in with a plain input that reports typing and Enter,
// plus buttons standing in for picking a suggestion.
// The row's content depends on the width, so the tests drive it rather than
// leaving it to whatever jsdom reports.
const viewport = { mobile: false };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: (query: string) => query === '(max-width: 600px)' && viewport.mobile,
    default: (query: string) => query === '(max-width: 600px)' && viewport.mobile,
}));

vi.mock('@/features/catalogue/AutocompleteSearch', () => ({
    default: ({
        value,
        onChange,
        onEnterPressed,
        onSuggestionSelected,
        onClear,
        scope,
        disabled,
        placeholder,
        searchType,
        runTag,
        hint,
        shelf,
    }: {
        value: string;
        onChange: (v: string) => void;
        onEnterPressed: () => void;
        onSuggestionSelected?: (suggestion: {
            value: string;
            type: 'book' | 'author';
            id?: number;
        }) => void;
        onClear?: () => void;
        scope?: { kind: string; id: string };
        disabled?: boolean;
        placeholder?: string;
        searchType?: string;
        runTag?: string;
        hint?: string;
        shelf?: { label: string; onPick: () => void };
    }) => (
        <div>
            {/* The picker answers from the list the reader is in; the panel is
                what tells it which list that is. */}
            <span data-testid="picker-scope">{scope ? `${scope.kind}:${scope.id}` : 'none'}</span>
            <span data-testid="picker-lanes">{searchType}</span>
            <span data-testid="picker-run">{runTag}</span>
            <span data-testid="picker-hint">{hint}</span>
            {shelf && (
                <button type="button" onClick={shelf.onPick}>
                    {shelf.label}
                </button>
            )}
            {/* The real field's cross carries the reset when it is given one,
                and says so; without one it only empties the box. */}
            {value.length > 0 && (
                <button
                    aria-label={onClear ? 'resetSearch' : 'clearSearch'}
                    onClick={() => {
                        onChange('');
                        onClear?.();
                    }}
                />
            )}
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
            <CatalogueSearchProvider>
                <HeaderSearchField />
                <ListLine />
            </CatalogueSearchProvider>
            <PathProbe />
        </MemoryRouter>,
    );
}

beforeEach(() => {
    vi.clearAllMocks();
    currentPath = '/books/page/1';
    viewport.mobile = false;
    favState.fav = false;
    favState.favEnabled = true;
    searchState.searchItem = '';
});

describe('The search field routing', () => {
    it('searches by title through /search', async () => {
        searchState.searchItem = 'дюна';
        renderBar();

        await userEvent.type(screen.getByLabelText('searchItem'), '{Enter}');

        expect(currentPath).toBe('/search?q=%D0%B4%D1%8E%D0%BD%D0%B0&type=books');
    });

    it('stays among authors under a list of authors', async () => {
        searchState.searchItem = 'Герберт';
        renderBar(`/search?q=${encodeURIComponent('Азимов')}&type=authors`);

        await userEvent.type(screen.getByLabelText('searchItem'), '{Enter}');

        expect(currentPath).toBe(`/search?q=${encodeURIComponent('Герберт')}&type=authors`);
    });

    it('percent-encodes the query so its punctuation cannot break the address', async () => {
        searchState.searchItem = 'а/б?в%г+д';
        renderBar();

        await userEvent.type(screen.getByLabelText('searchItem'), '{Enter}');

        expect(new URLSearchParams(currentPath.split('?')[1]).get('q')).toBe('а/б?в%г+д');
        expect(currentPath).toContain('%2F');
    });

    it('does nothing when the query is empty', async () => {
        searchState.searchItem = '   ';
        renderBar();

        await userEvent.type(screen.getByLabelText('searchItem'), '{Enter}');

        expect(currentPath).toBe('/books/page/1');
    });
});

// A reload or a pasted link lands on an already filtered list. The query lives
// in the URL, so the field can show what is being searched instead of sitting
// blank over a narrowed list.
describe('The search field on a cold reload', () => {
    // The mocked context keeps its state in a plain object, so a seed shows up
    // as what the panel asked the context to hold — the input itself is
    // controlled and follows that state in the real chain.
    it('seeds the field from the query a scoped search wrote', async () => {
        renderBar(`/author/42?title=${encodeURIComponent('война')}`);

        await waitFor(() => expect(searchState.setSearchItem).toHaveBeenCalledWith('война'));
    });

    it('seeds the field from a book search', async () => {
        renderBar(`/search?q=${encodeURIComponent('дюна')}&type=books&page=3`);

        await waitFor(() => expect(searchState.setSearchItem).toHaveBeenCalledWith('дюна'));
    });

    it('seeds the field from a search for an author by name', async () => {
        renderBar(`/search?q=${encodeURIComponent('кинг')}&type=authors`);

        await waitFor(() => expect(searchState.setSearchItem).toHaveBeenCalledWith('кинг'));
        expect(screen.getByTestId('picker-run')).toHaveTextContent('search.runAuthors');
    });

    it('asks for no clearing control on a search by name', () => {
        renderBar(`/search?q=${encodeURIComponent('кинг')}&type=authors`);

        expect(screen.queryByRole('button', { name: 'resetSearch' })).toBeNull();
    });

    it('seeds nothing on a plain list', () => {
        renderBar('/books/page/1');

        expect(searchState.setSearchItem).not.toHaveBeenCalled();
        expect(screen.getByLabelText('searchItem')).toHaveValue('');
    });
});

describe('The search field in favourites mode', () => {
    it('stays usable while the favourites filter is on', () => {
        // Favourites are a scope like any other now: the backend searches
        // inside them, so a disabled panel would only hide that.
        favState.fav = true;
        renderBar('/books/favorite/1');

        expect(screen.getByLabelText('searchItem')).toBeEnabled();
    });

    it('searches inside favourites without leaving the list', async () => {
        favState.fav = true;
        searchState.searchItem = 'война';
        renderBar('/books/favorite/2');

        await userEvent.type(screen.getByLabelText('searchItem'), '{Enter}');

        expect(currentPath).toBe(`/books/favorite/1?title=${encodeURIComponent('война')}`);
    });
});

// From 601 up the books language stands in the line over the list.
describe('The line over the list', () => {
    it('stands in the list column from 601 up, inside the page’s own gutter', () => {
        renderBar('/books/page/1');

        const line = screen.getByTestId('list-line');
        expect(line).toHaveClass(...CONTENT_COLUMN.split(' '));
        // The column's own box: padding here would set it inside the cards.
        expect(line).not.toHaveClass('px-4');
        expect(line.parentElement).toHaveClass('px-4');
    });
});

describe('The books language over the list', () => {
    beforeEach(() => {
        favState.fav = false;
        favState.favEnabled = true;
        searchState.selectedLanguage = 'ru';
        vi.clearAllMocks();
    });

    it('offers the books language', () => {
        renderBar();

        expect(screen.getByRole('button', { name: /^booksLanguage:/ })).toBeInTheDocument();
    });

    // Until this existed a filter, once set, could not be cleared: every row in
    // the list narrowed the catalogue and none widened it.
    it('can hand the catalogue back with no language filter at all', async () => {
        renderBar();

        await userEvent.click(screen.getByRole('button', { name: /^booksLanguage:/ }));
        await userEvent.click(await screen.findByRole('button', { name: 'allLanguages' }));

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
describe('The search field scoped search', () => {
    it("searches within an author's list instead of starting over", async () => {
        searchState.searchItem = 'война';
        renderBar('/author/42?page=3');

        await userEvent.type(screen.getByLabelText('searchItem'), '{Enter}');

        expect(currentPath).toBe(`/author/42?title=${encodeURIComponent('война')}`);
    });

    it('searches within a genre list', async () => {
        searchState.searchItem = 'война';
        renderBar('/books/find/genre/9/2');

        await userEvent.type(screen.getByLabelText('searchItem'), '{Enter}');

        expect(currentPath).toBe(`/books/find/genre/9/1?title=${encodeURIComponent('война')}`);
    });

    it('shows the scope on a scoped list and nowhere else', () => {
        renderBar('/author/42');
        expect(screen.getByText('searchWithinThisAuthor')).toBeInTheDocument();

        cleanup();
        renderBar('/books/page/1');
        expect(screen.queryByRole('button', { name: 'searchEverywhere' })).toBeNull();
    });

    it('releasing the scope widens the search but keeps the words', async () => {
        // The seeded field is the common case; the mock context does not
        // re-render on a seed, so the words are given before the render.
        searchState.searchItem = 'х';
        renderBar(`/author/42?title=${encodeURIComponent('х')}&page=3`);

        await userEvent.click(screen.getByRole('button', { name: 'searchEverywhere' }));

        // "Search the whole library" means exactly that: same text, no scope.
        expect(currentPath).toBe(`/search?q=${encodeURIComponent('х')}&type=books`);
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

describe('The search field suggestion picks', () => {
    it("navigates to the author's books when an author suggestion is picked", async () => {
        renderBar('/books/page/1');

        await userEvent.click(screen.getByRole('button', { name: 'pickAuthor' }));

        expect(currentPath).toBe('/author/42');
    });

    it('keeps the line whether the scope is on or released', async () => {
        // Releasing must not move the list. The chip changes; the line stays.
        viewport.mobile = true;
        renderBar('/books/find/genre/9/1');
        expect(screen.getByTestId('list-line')).toHaveTextContent('searchWithinThisGenre');

        await userEvent.click(screen.getByRole('button', { name: 'searchEverywhere' }));

        expect(screen.getByTestId('list-line')).toHaveTextContent('searchWithinThisGenre');
        expect(screen.getByRole('button', { name: 'searchScopeRestore' })).toBeInTheDocument();
    });

    it('offers the scope back once released', async () => {
        renderBar('/books/find/genre/9/1');
        await userEvent.click(screen.getByRole('button', { name: 'searchEverywhere' }));

        await userEvent.click(screen.getByRole('button', { name: 'searchScopeRestore' }));

        expect(screen.getByTestId('picker-scope')).toHaveTextContent('genre:9');
    });

    it('keeps a long label reachable', () => {
        renderBar('/books/find/genre/9/1');

        expect(screen.getByText('searchWithinThisGenre')).toHaveAttribute(
            'title',
            'searchWithinThisGenre',
        );
    });

    it('asks a confined field for books alone', () => {
        // Nothing but books can be reached from inside a list, so a name offered
        // there would lead out of it.
        renderBar('/books/find/genre/9/1');

        expect(screen.getByTestId('picker-lanes')).toHaveTextContent('title');
        expect(screen.getByTestId('picker-run')).toHaveTextContent('search.runHere');
        expect(screen.getByTestId('picker-hint')).toHaveTextContent('search.hintScoped');
        expect(screen.getByLabelText('searchItem')).toHaveAttribute(
            'placeholder',
            'search.placeholderScoped',
        );
    });

    it('asks for both lanes once the scope is released', async () => {
        renderBar('/books/find/genre/9/1');

        await userEvent.click(screen.getByRole('button', { name: 'searchEverywhere' }));

        expect(screen.getByTestId('picker-lanes')).toHaveTextContent('all');
        expect(screen.getByTestId('picker-run')).toHaveTextContent('search.runBooks');
        expect(screen.getByLabelText('searchItem')).toHaveAttribute(
            'placeholder',
            'search.placeholder',
        );
    });

    it('names both lanes and the books on a plain list', () => {
        renderBar('/books/page/1');

        expect(screen.getByTestId('picker-lanes')).toHaveTextContent('all');
        expect(screen.getByTestId('picker-run')).toHaveTextContent('search.runBooks');
        expect(screen.getByTestId('picker-hint')).toHaveTextContent('search.hint');
    });

    it('offers favourites in the empty field to a reader who has them', async () => {
        renderBar('/books/page/1');

        await userEvent.click(screen.getByRole('button', { name: 'favouritesTab' }));

        expect(currentPath).toBe('/books/favorite/1');
    });

    it('offers no shelf to a reader without one, or on the shelf itself', () => {
        favState.favEnabled = false;
        renderBar('/books/page/1');
        expect(screen.queryByRole('button', { name: 'favouritesTab' })).toBeNull();

        cleanup();
        favState.favEnabled = true;
        favState.fav = true;
        renderBar('/books/favorite/1');
        expect(screen.queryByRole('button', { name: 'favouritesTab' })).toBeNull();
    });

    it('tells the picker which list the reader is in', async () => {
        // Suggesting titles from the whole catalogue to someone standing in
        // their favourites offers books that list cannot show.
        renderBar('/books/favorite/1');
        expect(screen.getByTestId('picker-scope')).toHaveTextContent('favorites:');

        cleanup();
        renderBar('/books/find/genre/9/1');
        expect(screen.getByTestId('picker-scope')).toHaveTextContent('genre:9');
    });

    it('asks the picker for a curated collection, the list the route shows', () => {
        // The route's collections are the curated ones, and the list reads them
        // as curated_collection. The picker asked for `collection`, a reader's
        // own shelf, and production answered nothing inside any collection.
        renderBar('/collections/48/page/1');

        expect(screen.getByTestId('picker-scope')).toHaveTextContent('curated:48');
    });

    it('widens the picker when the scope is released', async () => {
        renderBar('/books/find/genre/9/1');

        // The cross beside the chip means "search everywhere", and the picker
        // has to widen with the search rather than keep offering the genre.
        await userEvent.click(screen.getByRole('button', { name: 'searchEverywhere' }));

        expect(screen.getByTestId('picker-scope')).toHaveTextContent('none');
    });

    it('clearing the box on a filtered page goes back to the list', async () => {
        // A search lives in the URL, so the box holds the words the page is
        // filtered by. The cross used to empty the field and leave the reader
        // looking at the same filtered page, with nothing on screen offering
        // the way back. One gesture now does both — and costs no width beside
        // the scope chip, which a second control had squeezed to its cross on
        // a phone.
        // The panel seeds the box from the address; the fake context does not
        // re-render on that, so the seeded state is set up here directly.
        searchState.searchItem = 'война';
        renderBar('/author/42?title=%D0%B2%D0%BE%D0%B9%D0%BD%D0%B0');

        await userEvent.click(screen.getByRole('button', { name: 'resetSearch' }));

        expect(currentPath).toBe('/author/42');
        expect(searchState.searchItem).toBe('');
    });

    it('clearing the box on an unfiltered page only empties the box', async () => {
        // Words typed but not yet searched for: the cross is what it always
        // was, because there is no filtered page to come back from.
        searchState.searchItem = 'полов';
        renderBar('/books/page/1');

        expect(screen.getByRole('button', { name: 'clearSearch' })).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'resetSearch' })).not.toBeInTheDocument();
    });

    it('searches for a picked title rather than opening the one copy', async () => {
        renderBar('/books/page/1');

        await userEvent.click(screen.getByRole('button', { name: 'pickBook' }));

        // The picker used to pin the suggestion's own id, which answered with
        // exactly one book. A reader picking "Властелин колец" out of the list
        // wants every edition of it, not whichever one came first.
        expect(searchState.searchItem).toBe('Война и мир');
        expect(currentPath).toBe(
            `/search?q=${encodeURIComponent('Война и мир').replaceAll('%20', '+')}&type=books`,
        );
    });

    it('searches for a picked title inside the current scope', async () => {
        renderBar('/books/find/genre/9/2');

        await userEvent.click(screen.getByRole('button', { name: 'pickBook' }));

        expect(currentPath).toBe(
            `/books/find/genre/9/1?title=${encodeURIComponent('Война и мир')}`,
        );
    });
});
