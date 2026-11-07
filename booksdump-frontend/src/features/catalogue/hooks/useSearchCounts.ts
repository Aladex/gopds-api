import { useEffect, useState } from 'react';

import * as booksApi from '@/api/books';

export interface SearchCounts {
    books?: number;
    authors?: number;
}

/**
 * useSearchCounts says how big each answer to a search is, for the tabs that
 * offer them.
 *
 * Asked with a limit of one, the list's page count is its exact total, so no
 * endpoint had to learn anything. Both follow the books language, which is why
 * the answer is held with the search and language it answers and shown only
 * while they are the ones on screen: a language change must not leave the old
 * number standing beside the new list. A count that cannot be had is left out.
 */
export function useSearchCounts(query: string, lang: string): SearchCounts {
    const key = `${lang}:${query}`;
    const [answer, setAnswer] = useState<{ key: string; counts: SearchCounts }>({
        key: '',
        counts: {},
    });

    useEffect(() => {
        if (!query) {
            return;
        }
        let cancelled = false;
        const keep = (counts: SearchCounts) => {
            if (cancelled) return;
            setAnswer((previous) => ({
                key,
                counts: previous.key === key ? { ...previous.counts, ...counts } : counts,
            }));
        };
        booksApi
            .listBooks({ limit: 1, offset: 0, title: query, lang })
            .then(({ length }) => keep({ books: length }))
            .catch(() => {});
        booksApi
            .listAuthors({ limit: 1, offset: 0, author: query, lang })
            .then(({ total }) => keep({ authors: total ?? 0 }))
            .catch(() => {});
        return () => {
            cancelled = true;
        };
    }, [key, lang, query]);

    return answer.key === key && query ? answer.counts : {};
}
