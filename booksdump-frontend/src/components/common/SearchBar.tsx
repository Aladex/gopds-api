import React from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Heart } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { cn } from '@/lib/utils';

import { useAuthor } from '../../context/AuthorContext';
import { useFav } from '../../context/FavContext';
import { useSearchBar } from '../../context/SearchBarContext';
import useSearchOptions from '../hooks/useSearchOptions';
import AutocompleteSearch from './AutocompleteSearch';

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
    const { searchItem, setSearchItem, selectedSearch, setSelectedSearch } = useSearchBar();
    const navigate = useNavigate();
    const { fav, favEnabled } = useFav();
    const searchOptions = useSearchOptions(setSelectedSearch);
    const { authorId, setAuthorBook, clearAuthorId, clearAuthorBook } = useAuthor();

    const records: SearchRecord[] = [
        { option: 'authorsBookSearch', path: `/books/find/author/` },
        { option: 'title', path: `/books/find/title/${encodeURIComponent(searchItem)}/1` },
        { option: 'author', path: `/authors/${encodeURIComponent(searchItem)}/1` },
    ];

    const navigateToSearchResults = () => {
        // Check that the search field is not empty and contains at least one character
        if (!searchItem || searchItem.trim().length === 0) {
            return;
        }

        const record = records.find((item) => item.option === selectedSearch);
        setAuthorBook(searchItem);
        if (!record) {
            return;
        }

        // Searching an author's own books keeps the author id; anything else
        // starts from a clean slate.
        if (record.option !== 'authorsBookSearch') {
            clearAuthorId();
            clearAuthorBook();
            navigate(record.path);
        } else {
            navigate(record.path + authorId + '/1');
        }
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
                <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3 md:grid-cols-[minmax(170px,1fr)_minmax(0,2.6fr)_auto_auto]">
                    <div className="col-span-2 flex min-w-0 flex-col gap-1.5 md:col-span-1">
                        <label
                            htmlFor="search-category"
                            className="text-xs text-muted-foreground"
                        >
                            {t('categorySearch')}
                        </label>
                        <Select
                            value={selectedSearch}
                            onValueChange={setSelectedSearch}
                            disabled={fav}
                        >
                            <SelectTrigger id="search-category" className="w-full">
                                <SelectValue placeholder={t('categorySearch')} />
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

                    <div className="col-span-2 flex min-w-0 flex-col gap-1.5 md:col-span-1">
                        <span className="text-xs text-muted-foreground">{t('searchQuery')}</span>
                        <AutocompleteSearch
                            value={searchItem}
                            onChange={setSearchItem}
                            searchType={selectedSearch}
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
