import React from 'react';
import { useTranslation } from 'react-i18next';

import { Plus, X } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/ui/select';
import { cn } from '@/shared/lib/utils';

import { useSearchBar } from '@/context/SearchBarContext';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import LanguageSwitcher from '@/shared/layout/LanguageSwitcher';
import AutocompleteSearch from '@/features/catalogue/AutocompleteSearch';
import {
    CatalogueSearchProvider,
    useCatalogueSearchContext,
} from '@/features/catalogue/CatalogueSearchContext';
import { useBookLanguage } from '@/features/catalogue/hooks/useBookLanguage';

/** The search panel: what useCatalogueSearch decides, drawn. */
const SearchPanel: React.FC = () => {
    const { t } = useTranslation();
    const { searchItem, setSearchItem, selectedSearch, setSelectedSearch } = useSearchBar();
    const { languages, selected, choose } = useBookLanguage();
    const isMobile = useMediaQuery('(max-width: 600px)');
    const { scope, scopeApplies, suggestionScope, reset, submit, releaseScope, pick } =
        useCatalogueSearchContext();

    const searchOptions = [
        { value: 'title', label: t('byTitle') },
        { value: 'author', label: t('byAuthor') },
    ];

    return (
        <div className="mx-auto w-full max-w-[1200px] py-1.5">
            <div className="rounded border border-border bg-card p-3.5">
                <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3 md:grid-cols-[minmax(150px,max-content)_minmax(0,1fr)_auto_auto]">
                    <div className="col-span-2 flex min-w-0 flex-col gap-1.5 md:col-span-1">
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

                    <div className="col-span-2 flex min-w-0 flex-col gap-1.5 md:col-span-1">
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
                            onEnterPressed={submit}
                            onSuggestionSelected={pick}
                            onClear={reset}
                            placeholder={t('searchItem')}
                        />
                    </div>

                    <Button type="button" onClick={submit} className="px-6 uppercase tracking-wide">
                        {t('search')}
                    </Button>

                    <LanguageSwitcher
                        languages={languages}
                        selected={selected}
                        onSelect={choose}
                        isMobile={isMobile}
                    />
                </div>
            </div>
        </div>
    );
};

const SearchBar: React.FC = () => (
    <CatalogueSearchProvider>
        <SearchPanel />
    </CatalogueSearchProvider>
);

export default SearchBar;
