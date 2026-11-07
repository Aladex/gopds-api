import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { NavigationType, useLocation, useNavigate, useNavigationType } from 'react-router';

import type { AutocompleteSuggestion, SuggestionScope } from '@/api/autocomplete';
import { useAuthor } from '@/context/AuthorContext';
import { useSearchBar } from '@/context/SearchBarContext';
import { authorPath, isAuthorPath, isSearchPath, searchPath } from '@/features/catalogue/addresses';
import useSearchScope, {
    authorNameFromLocation,
    searchTitleFromLocation,
    unsearchedPathFrom,
} from '@/features/catalogue/hooks/useSearchScope';
import { authorTitle } from '@/shared/lib/authorTitle';

/**
 * useCatalogueSearch turns a query into a route. Nothing here fetches: the list
 * route it navigates to does the loading.
 *
 * Every scoped list — an author's books, a series, a genre, a collection,
 * favourites — confines the next title search to itself, and the query rides
 * the URL so a reload or a shared link reproduces exactly what is on screen.
 *
 * It holds the state of the scope, so it must run once however many controls
 * show that state: read it through CatalogueSearchContext, not from here.
 */
export function useCatalogueSearch() {
    const { t } = useTranslation();
    const { searchItem, setSearchItem, selectedSearch, setSelectedSearch } = useSearchBar();
    const navigate = useNavigate();
    const location = useLocation();

    // A reload or a pasted link lands on an already filtered list with an
    // empty box, because the query lives in the URL rather than in state.
    // Seed the field from the address so the reader can see — and edit —
    // what is being searched. A search among authors brings its mode too, or
    // the reader's next search would go to the books under a list of authors.
    // Back and Forward to a book search bring title mode the same way. Any
    // other way onto one leaves the mode to the reader who picked it.
    const onNameSearch = useRef(false);
    // Read from a ref so that the kind of arrival alone, with the address
    // unchanged, does not run the seeding over a draft.
    const navigationType = useNavigationType();
    const arrival = useRef(navigationType);
    useEffect(() => {
        arrival.current = navigationType;
    });
    useEffect(() => {
        const name = authorNameFromLocation(location.pathname, location.search);
        const title = searchTitleFromLocation(location.pathname, location.search);
        if (name) {
            setSelectedSearch('author');
            setSearchItem(name);
        } else if (title) {
            if (isSearchPath(location.pathname) && arrival.current === NavigationType.Pop) {
                setSelectedSearch('title');
            }
            setSearchItem(title);
        } else if (onNameSearch.current && isAuthorPath(location.pathname)) {
            // From a list of names into one author. A card empties the box on
            // its way through, but Forward passes no card, and the name left
            // there would filter this author's books as a title.
            setSearchItem('');
        }
        onNameSearch.current = Boolean(name);
    }, [location.pathname, location.search, setSearchItem, setSelectedSearch]);

    // Arriving in a scoped list puts the panel in the mode the scope belongs
    // to. Without this a reader who got here by searching for a name stays in
    // "authors by name", where the scope cannot apply and so is not even
    // shown — they had to know to switch modes themselves.
    const scope = useSearchScope(() => setSelectedSearch('title'));
    const { setAuthorId, setAuthorName, clearAuthorId } = useAuthor();

    // Looking for authors by name is a search of the whole library by
    // definition, so the scope only applies to a search for books.
    const scopeApplies = scope.kind !== null && selectedSearch === 'title';
    const scoped = scopeApplies && scope.active;

    const submit = () => {
        // Check that the search field is not empty and contains at least one character
        if (!searchItem || searchItem.trim().length === 0) {
            return;
        }

        if (selectedSearch === 'author') {
            clearAuthorId();
            navigate(searchPath(searchItem, 'authors'));
            return;
        }

        // Confined to a list: stay on it and filter it, the query riding the
        // URL so a reload or a shared link asks for exactly this.
        if (scoped) {
            navigate(`${scope.firstPagePath}?title=${encodeURIComponent(searchItem)}`);
            return;
        }

        clearAuthorId();
        navigate(searchPath(searchItem));
    };

    // Where the reader is standing is what decides whether there is anything
    // to clear — the box can hold words that were never searched for.
    const unsearchedPath = unsearchedPathFrom(location.pathname, location.search);

    // The picker answers from the list the reader is in, the same list the
    // search will stay in. Releasing the scope widens both together.
    const suggestionScope: SuggestionScope | undefined =
        scoped && scope.kind
            ? { kind: scope.kind === 'collection' ? 'curated' : scope.kind, id: scope.id }
            : undefined;

    const reset = () => {
        setSearchItem('');
        clearAuthorId();
        navigate(unsearchedPath);
    };

    const releaseScope = () => {
        scope.release();
        // With words in the box, "search everywhere" is a search: same text,
        // no scope. With an empty box the chip simply offers itself back.
        const query = searchItem.trim();
        if (query) {
            navigate(searchPath(query));
        }
    };

    const pick = (suggestion: AutocompleteSuggestion) => {
        if (suggestion.type === 'author') {
            // A picked author is chosen, not searched: open their books with
            // the scope already on, the name already in hand for the chip, and
            // a clean field — the name is not a title to filter by.
            if (suggestion.id == null) {
                return;
            }
            setAuthorId(String(suggestion.id));
            setAuthorName(
                authorTitle(
                    {
                        full_name: suggestion.value,
                        label: suggestion.label,
                        label_books: suggestion.label_books,
                    },
                    t,
                ),
            );
            setSearchItem('');
            navigate(authorPath(suggestion.id));
            return;
        }
        // A picked book is a title, not a copy of it. Pinning the suggestion's
        // own id used to answer with exactly one book, which is wrong for the
        // reason readers pick from the list at all: someone choosing "Властелин
        // колец" wants every edition of it, not whichever one the picker
        // happened to put first. The words go in and the search runs.
        setSearchItem(suggestion.value);
        if (scoped) {
            navigate(`${scope.firstPagePath}?title=${encodeURIComponent(suggestion.value)}`);
        } else {
            navigate(searchPath(suggestion.value));
        }
    };

    return {
        scope,
        scopeApplies,
        suggestionScope,
        /** What clearing the box means when the words are the filter, if they are. */
        reset: unsearchedPath ? reset : undefined,
        submit,
        releaseScope,
        pick,
    };
}

export type CatalogueSearch = ReturnType<typeof useCatalogueSearch>;
