import React from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Heart, Plus, X } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/shared/ui/select';
import { cn } from '@/shared/lib/utils';

import { useAuth } from '@/context/AuthContext';
import { useAuthor } from '@/context/AuthorContext';
import { useFav } from '@/context/FavContext';
import { useSearchBar } from '@/context/SearchBarContext';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import LanguageSwitcher from '@/shared/layout/LanguageSwitcher';
import useAuthorScope from '@/features/catalogue/hooks/useAuthorScope';
import AutocompleteSearch from '@/features/catalogue/AutocompleteSearch';

interface SearchRecord {
    option: string;
    path: string;
}

/**
 * SearchBar turns a query into a route. Nothing here fetches: the list route it
 * navigates to does the loading.
 *
 * While the favourites filter is on, every control is disabled — the backend
 * cannot search inside favourites, so an enabled box would promise something it
 * would not deliver.
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
    const { fav, favEnabled } = useFav();
    const { updateLang } = useAuth();
    const isMobile = useMediaQuery('(max-width: 600px)');

    const chooseLanguage = (lang: string) => {
        updateLang(lang);
        setSelectedLanguage(lang);
    };
    // Arriving in an author's list puts the panel in the mode the scope
    // belongs to. Without this a reader who got here by searching for a name
    // stays in "authors by name", where the scope cannot apply and so is not
    // even shown — they had to know to switch modes themselves.
    const scope = useAuthorScope(() => setSelectedSearch('title'));
    const { setAuthorBook, clearAuthorId, clearAuthorBook } = useAuthor();

    const searchOptions = [
        { value: 'title', label: t('byTitle') },
        { value: 'author', label: t('byAuthor') },
    ];

    const records: SearchRecord[] = [
        { option: 'title', path: `/books/find/title/${encodeURIComponent(searchItem)}/1` },
        { option: 'author', path: `/authors/${encodeURIComponent(searchItem)}/1` },
    ];

    // Looking for authors by name is a search of the whole library by
    // definition, so the scope only applies to a search for books.
    const scopeApplies = scope.available && selectedSearch === 'title';
    const scoped = scopeApplies && scope.active;

    const scopeLabel = scope.name
        ? t('searchWithinAuthor', { name: scope.name })
        : t('searchWithinThisAuthor');

    const navigateToSearchResults = () => {
        // Check that the search field is not empty and contains at least one character
        if (!searchItem || searchItem.trim().length === 0) {
            return;
        }

        // Confined to one author: stay on their list and filter it, which is
        // what the list route does with a title alongside an author.
        if (scoped && scope.id) {
            setAuthorBook(searchItem);
            navigate(`/books/find/author/${scope.id}/1`);
            return;
        }

        const record = records.find((item) => item.option === selectedSearch);
        if (!record) {
            return;
        }

        clearAuthorId();
        clearAuthorBook();
        navigate(record.path);
    };

    const toggleFavourites = () => {
        if (!favEnabled) {
            return;
        }
        navigate(fav ? '/books/page/1' : '/books/favorite/1');
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
                        <label
                            htmlFor="search-category"
                            className="text-xs text-muted-foreground"
                        >
                            {t('searchWhat')}
                        </label>
                        <Select
                            value={selectedSearch}
                            onValueChange={setSelectedSearch}
                            disabled={fav}
                        >
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
                        <div className="flex min-w-0 items-center justify-between gap-2">
                            <span className="shrink-0 text-xs text-muted-foreground">
                                {t('searchQuery')}
                            </span>
                            {/*
                              While the reader is in an author's list the scope
                              stays on screen either way — on, with a cross to
                              drop it, or off and offering itself back. It used
                              to vanish once released, leaving no way to confine
                              the search again short of navigating out and in.
                            */}
                            {scopeApplies &&
                                (scope.active ? (
                                    <span className="inline-flex min-w-0 items-center gap-0.5 rounded-full bg-accent py-0.5 pr-0.5 pl-2 text-xs text-accent-foreground">
                                        {/* A long name is cut on a narrow
                                            screen, so the whole of it stays
                                            reachable. */}
                                        <span className="truncate" title={scope.name || undefined}>
                                            {scopeLabel}
                                        </span>
                                        <button
                                            type="button"
                                            onClick={scope.release}
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
                                        <span className="truncate">{scopeLabel}</span>
                                    </button>
                                ))}
                        </div>

                        <AutocompleteSearch
                            value={searchItem}
                            onChange={setSearchItem}
                            searchType={selectedSearch}
                            authorScope={scoped ? scope.id : undefined}
                            disabled={fav}
                            onEnterPressed={navigateToSearchResults}
                            placeholder={t('searchItem')}
                        />
                    </div>

                    <Button
                        type="button"
                        onClick={navigateToSearchResults}
                        disabled={fav}
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
                        disabled={fav}
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
