import React from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';

import { useFav } from '@/context/FavContext';
import { useSearchBar } from '@/context/SearchBarContext';
import type { AutocompleteSuggestion } from '@/api/autocomplete';
import { useKeyboardInset } from '@/shared/hooks/useKeyboardInset';
import AutocompleteSearch from '@/features/catalogue/AutocompleteSearch';
import { useCatalogueSearchContext } from '@/features/catalogue/CatalogueSearchContext';
import ScopeChip from '@/features/catalogue/ScopeChip';
import BookLanguage from '@/shared/layout/BookLanguage';
import { useForeignLanguage } from '@/shared/layout/useForeignLanguage';

type HeaderSearchFieldProps = {
    /** Whether the field has the phone's screen to itself. */
    taking?: boolean;
    /** Asked for on a real focus; a wider screen leaves it out. */
    onTake?: () => void;
    /** The reader is done with the takeover, one way or another. */
    onDone?: () => void;
    className?: string;
};

/**
 * HeaderSearchField is the one field: a book or an author, in the chrome. What
 * it does lives in useCatalogueSearch; this says where the search will go and
 * what the open field offers.
 */
const HeaderSearchField: React.FC<HeaderSearchFieldProps> = ({
    taking = false,
    onTake,
    onDone,
    className,
}) => {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const { searchItem, setSearchItem } = useSearchBar();
    const { fav, favEnabled } = useFav();
    const { scope, mode, suggestionScope, reset, submit, pick } = useCatalogueSearchContext();
    const keyboard = useKeyboardInset(taking);
    const loud = useForeignLanguage();

    const confined = scope.kind !== null && scope.active;

    const runTag = () => {
        if (confined) return t('search.runHere');
        return mode === 'authors' ? t('search.runAuthors') : t('search.runBooks');
    };

    // Whatever takes the reader somewhere ends the takeover, even a search for
    // the words already on the page, which no change of address would close.
    const finish = (action: () => void) => () => {
        action();
        onDone?.();
    };

    return (
        <div className={className}>
            <AutocompleteSearch
                value={searchItem}
                onChange={setSearchItem}
                // Inside a list only its books can be reached, so a name offered
                // there would lead out of it. Authors come back with the cross.
                searchType={confined ? 'title' : 'all'}
                scope={suggestionScope}
                onEnterPressed={finish(submit)}
                onSuggestionSelected={(suggestion: AutocompleteSuggestion) =>
                    finish(() => pick(suggestion))()
                }
                onClear={reset && finish(reset)}
                placeholder={t(confined ? 'search.placeholderScoped' : 'search.placeholder')}
                runTag={runTag()}
                hint={t(confined ? 'search.hintScoped' : 'search.hint')}
                // A shortcut needs a shelf behind it, and none is wanted on the
                // shelf itself. The word in the row stays either way.
                shelf={
                    favEnabled && !fav
                        ? {
                              label: t('favouritesTab'),
                              onPick: finish(() => navigate('/books/favorite/1')),
                          }
                        : undefined
                }
                taking={taking}
                keyboard={keyboard}
                onFocused={onTake}
                onDismissed={onDone}
                top={
                    scope.kind !== null || loud ? (
                        <div className="flex min-h-11 items-center gap-2 border-b border-border px-3">
                            <ScopeChip />
                            {loud && <BookLanguage where="takeover" className="ml-auto" />}
                        </div>
                    ) : undefined
                }
            />
        </div>
    );
};

export default HeaderSearchField;
