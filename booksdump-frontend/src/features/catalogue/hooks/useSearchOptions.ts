import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation } from 'react-router-dom';

const AUTHOR_BOOKS_PATH = '/books/find/author/';

export interface SearchOption {
    value: string;
    label: string;
}

/**
 * useSearchOptions lists the search modes the panel offers.
 *
 * The list is not state: it follows from where the reader is and what language
 * they read in, so it is derived on each render rather than mirrored into
 * useState and kept up to date by effects.
 *
 * Selecting a mode on the reader's behalf is a genuine side effect — it writes
 * to state owned by the search panel — so that part stays in an effect, and
 * fires only when the reader crosses in or out of an author's list. Doing it on
 * every mount would reset a mode they had chosen the moment they turned a page.
 */
const useSearchOptions = (setSelectedSearch: (value: string) => void): SearchOption[] => {
    const { t } = useTranslation();
    const location = useLocation();

    const insideAuthorBooks = location.pathname.startsWith(AUTHOR_BOOKS_PATH);

    // Starts at false so arriving straight on an author's list still counts as
    // a crossing, as it did when the option list was state and started empty.
    const wasInside = useRef(false);

    useEffect(() => {
        if (wasInside.current === insideAuthorBooks) {
            return;
        }
        wasInside.current = insideAuthorBooks;
        setSelectedSearch(insideAuthorBooks ? 'authorsBookSearch' : 'title');
    }, [insideAuthorBooks, setSelectedSearch]);

    return [
        { value: 'title', label: t('byTitle') },
        { value: 'author', label: t('byAuthor') },
        ...(insideAuthorBooks
            ? [{ value: 'authorsBookSearch', label: t('authorsBookSearch') }]
            : []),
    ];
};

export default useSearchOptions;
