import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';

import type { SuggestionScope } from '@/api/autocomplete';
import { useLocation, useNavigate } from 'react-router';
import { Heart, Plus, X } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/ui/select';
import { cn } from '@/shared/lib/utils';

import { useAuth } from '@/context/AuthContext';
import { useAuthor } from '@/context/AuthorContext';
import { useFav } from '@/context/FavContext';
import { useSearchBar } from '@/context/SearchBarContext';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import LanguageSwitcher from '@/shared/layout/LanguageSwitcher';
import useSearchScope, {
    searchTitleFromLocation,
    unsearchedPathFrom,
} from '@/features/catalogue/hooks/useSearchScope';
import type { AutocompleteSuggestion } from '@/api/autocomplete';
import AutocompleteSearch from '@/features/catalogue/AutocompleteSearch';

/**
 * SearchBar turns a query into a route. Nothing here fetches: the list route it
 * navigates to does the loading.
 *
 * Every scoped list — an author's books, a series, a genre, a collection,
 * favourites — confines the next title search to itself, and the query rides
 * the URL so a reload or a shared link reproduces exactly what is on screen.
 */
const SearchBar: React.FC = () => {
    const { t } = useTranslation();
    const {
        searchItem,
        setSearchItem,
        selectedSearch,
        setSelectedSearch,
        languages,
        selectedLanguage,
        setSelectedLanguage,
    } = useSearchBar();
    const navigate = useNavigate();
    const location = useLocation();
    const { fav, favEnabled } = useFav();
    const { updateLang } = useAuth();
    const isMobile = useMediaQuery('(max-width: 600px)');

    // A reload or a pasted link lands on an already filtered list with an
    // empty box, because the query lives in the URL rather than in state.
    // Seed the field from the address so the reader can see — and edit —
    // what is being searched.
    useEffect(() => {
        const title = searchTitleFromLocation(location.pathname, location.search);
        if (title) {
            setSearchItem(title);
        }
    }, [location.pathname, location.search, setSearchItem]);

    const chooseLanguage = (lang: string) => {
        updateLang(lang);
        setSelectedLanguage(lang);
    };
    // Arriving in a scoped list puts the panel in the mode the scope belongs
    // to. Without this a reader who got here by searching for a name stays in
    // "authors by name", where the scope cannot apply and so is not even
    // shown — they had to know to switch modes themselves.
    const scope = useSearchScope(() => setSelectedSearch('title'));
    const { setAuthorId, setAuthorName, clearAuthorId } = useAuthor();

    const searchOptions = [
        { value: 'title', label: t('byTitle') },
        { value: 'author', label: t('byAuthor') },
    ];

    // Looking for authors by name is a search of the whole library by
    // definition, so the scope only applies to a search for books.
    const scopeApplies = scope.kind !== null && selectedSearch === 'title';
    const scoped = scopeApplies && scope.active;

    const navigateToSearchResults = () => {
        // Check that the search field is not empty and contains at least one character
        if (!searchItem || searchItem.trim().length === 0) {
            return;
        }

        const encoded = encodeURIComponent(searchItem);

        if (selectedSearch === 'author') {
            clearAuthorId();
            navigate(`/authors/${encoded}/1`);
            return;
        }

        // Confined to a list: stay on it and filter it, the query riding the
        // URL so a reload or a shared link asks for exactly this.
        if (scoped) {
            navigate(`${scope.firstPagePath}?title=${encoded}`);
            return;
        }

        clearAuthorId();
        navigate(`/books/find/title/${encoded}/1`);
    };

    // Where the reader is standing is what decides whether there is anything
    // to clear — the box can hold words that were never searched for.
    const unsearchedPath = unsearchedPathFrom(location.pathname, location.search);

    // The picker answers from the list the reader is in, the same list the
    // search will stay in. Releasing the scope widens both together.
    const suggestionScope: SuggestionScope | undefined =
        scoped && scope.kind
            ? { kind: scope.kind === 'collection' ? 'collection' : scope.kind, id: scope.id }
            : undefined;

    const resetSearch = () => {
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
            navigate(`/books/find/title/${encodeURIComponent(query)}/1`);
        }
    };

    const pickSuggestion = (suggestion: AutocompleteSuggestion) => {
        if (suggestion.type === 'author') {
            // A picked author is chosen, not searched: open their books with
            // the scope already on, the name already in hand for the chip, and
            // a clean field — the name is not a title to filter by.
            if (suggestion.id == null) {
                return;
            }
            setAuthorId(String(suggestion.id));
            setAuthorName(suggestion.value);
            setSearchItem('');
            navigate(`/books/find/author/${suggestion.id}/1`);
            return;
        }
        // A picked book is a title, not a copy of it. Pinning the suggestion's
        // own id used to answer with exactly one book, which is wrong for the
        // reason readers pick from the list at all: someone choosing "Властелин
        // колец" wants every edition of it, not whichever one the picker
        // happened to put first. The words go in and the search runs.
        setSearchItem(suggestion.value);
        const encoded = encodeURIComponent(suggestion.value);
        if (scoped) {
            navigate(`${scope.firstPagePath}?title=${encoded}`);
        } else {
            navigate(`/books/find/title/${encoded}/1`);
        }
    };

    const toggleFavourites = () => {
        if (!favEnabled) {
            return;
        }
        const query = searchItem.trim();
        const encoded = encodeURIComponent(query);
        if (fav) {
            // Back to the library: the words return to the broad search they
            // would have been had favourites never narrowed it.
            navigate(query ? `/books/find/title/${encoded}/1` : '/books/page/1');
        } else {
            // Into favourites, keeping the words: the filter changes, the
            // search does not.
            navigate(query ? `/books/favorite/1?title=${encoded}` : '/books/favorite/1');
        }
    };

    return (
        <div className="mx-auto w-full max-w-[1200px] py-1.5">
            <div className="rounded border border-border bg-card p-3.5">
                {/*
                  Narrow screens stack the two fields but keep the search button
                  and the favourites toggle on one row — the toggle is a small
                  square and a row of its own leaves it stranded.
                */}
                <div className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-end gap-3 md:grid-cols-[minmax(150px,max-content)_minmax(0,1fr)_auto_auto_auto]">
                    <div className="col-span-3 flex min-w-0 flex-col gap-1.5 md:col-span-1">
                        <label htmlFor="search-category" className="text-xs text-muted-foreground">
                            {t('searchWhat')}
                        </label>
                        <Select value={selectedSearch} onValueChange={setSelectedSearch}>
                            <SelectTrigger id="search-category" className="w-full">
                                <SelectValue placeholder={t('searchWhat')} />
                            </SelectTrigger>
                            <SelectContent>
                                {searchOptions.map((option) => (
                                    <SelectItem key={option.value} value={option.value}>
                                        {option.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>

                    <div className="col-span-3 flex min-w-0 flex-col gap-1.5 md:col-span-1">
                        {/*
                          The scope rides the label's own row: it costs no
                          height there, and it leaves the field its full width —
                          inside the box it took nearly half of a phone's.
                        */}
                        {/*
                          The row is always here and always holds exactly one
                          thing on a phone: the label, or — once the reader is
                          inside a list — the chip that replaces it and takes
                          the full width. A conditional row is what made the
                          card grow by thirty pixels the moment a reader
                          stepped into a genre, and shrink again on the way
                          out. The height is pinned to the chip's own 24px,
                          because a label is only eighteen and the difference
                          would still show as a smaller jump — on the desktop
                          too, where the label was never conditional.
                        */}
                        <div className="flex min-h-6 min-w-0 items-center justify-between gap-2">
                            {/*
                              On a phone the row has no width for both: the
                              label repeats what the field's own placeholder
                              already says, and keeping it squeezed the scope
                              chip down to its cross. It yields, rather than
                              disappearing — the row it occupied stays.
                            */}
                            {(!isMobile || !scopeApplies) && (
                                <span className="shrink-0 text-xs text-muted-foreground">
                                    {t('searchQuery')}
                                </span>
                            )}
                            {/*
                              While the reader is on a scoped list the scope
                              stays on screen either way — on, with a cross to
                              drop it, or off and offering itself back. It used
                              to vanish once released, leaving no way to confine
                              the search again short of navigating out and in.
                            */}
                            {scopeApplies &&
                                (scope.active ? (
                                    <span className="inline-flex min-w-0 items-center gap-0.5 rounded-full bg-accent py-0.5 pr-0.5 pl-2 text-xs text-accent-foreground">
                                        {/* A long label is cut on a narrow
                                            screen, so the whole of it stays
                                            reachable. */}
                                        <span className="truncate" title={scope.label}>
                                            {scope.label}
                                        </span>
                                        <button
                                            type="button"
                                            onClick={releaseScope}
                                            aria-label={t('searchEverywhere')}
                                            title={t('searchEverywhere')}
                                            className="flex size-4 shrink-0 items-center justify-center rounded-full hover:bg-foreground/15"
                                        >
                                            <X className="size-3" />
                                        </button>
                                    </span>
                                ) : (
                                    <button
                                        type="button"
                                        onClick={scope.reclaim}
                                        aria-label={t('searchScopeRestore')}
                                        title={t('searchScopeRestore')}
                                        className={cn(
                                            'inline-flex min-w-0 items-center gap-1 rounded-full border border-dashed border-border py-0.5 pr-2 pl-1.5 text-xs text-muted-foreground',
                                            'hover:border-solid hover:bg-accent hover:text-accent-foreground',
                                        )}
                                    >
                                        <Plus className="size-3 shrink-0" />
                                        <span className="truncate">{scope.label}</span>
                                    </button>
                                ))}
                        </div>

                        <AutocompleteSearch
                            value={searchItem}
                            onChange={setSearchItem}
                            searchType={selectedSearch}
                            scope={suggestionScope}
                            onEnterPressed={navigateToSearchResults}
                            onSuggestionSelected={pickSuggestion}
                            onClear={unsearchedPath ? resetSearch : undefined}
                            placeholder={t('searchItem')}
                        />
                    </div>

                    <Button
                        type="button"
                        onClick={navigateToSearchResults}
                        className="px-6 uppercase tracking-wide"
                    >
                        {t('search')}
                    </Button>

                    {/*
                      The books language belongs beside the favourites toggle,
                      not up in the bar: both narrow the catalogue, and one of
                      them living in the chrome while the other lived here read
                      as two unrelated things.
                    */}
                    <LanguageSwitcher
                        languages={languages}
                        selected={selectedLanguage}
                        onSelect={chooseLanguage}
                        isMobile={isMobile}
                    />

                    <Button
                        type="button"
                        variant="outline"
                        size="icon"
                        onClick={toggleFavourites}
                        disabled={!favEnabled}
                        aria-pressed={fav}
                        title={fav ? t('showAllBooks') : t('showFavourites')}
                        aria-label={fav ? t('showAllBooks') : t('showFavourites')}
                        className={cn(fav && 'border-amber-500 text-amber-500')}
                    >
                        <Heart className={cn('size-4', fav && 'fill-current')} />
                    </Button>
                </div>
            </div>
        </div>
    );
};

export default SearchBar;
