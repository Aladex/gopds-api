import { http } from '@/api/http';

export interface AutocompleteSuggestion {
    /** Display text: the book title or the author name. */
    value: string;
    /** What the suggestion opens. */
    type: 'book' | 'author';
    id?: number;
    /** Book lane only: the author, telling identical titles apart. */
    secondary?: string;
    /** Author lane only: how many visible books the author holds. */
    books_count?: number;
}

export interface AutocompleteResponse {
    suggestions: AutocompleteSuggestion[];
}

/**
 * SuggestionScope is the list the reader is standing in, so the picker can
 * offer titles they can reach from it. Naming the kind rather than passing a
 * bare id keeps "what is being searched" and "where" apart — folding them
 * together is what made the second question invisible when only authors had
 * an answer.
 */
export interface SuggestionScope {
    kind: 'author' | 'series' | 'genre' | 'collection' | 'curated' | 'favorites';
    id: string;
}

const scopeParams = (scope?: SuggestionScope): Record<string, string> => {
    if (!scope) {
        return {};
    }
    switch (scope.kind) {
        case 'author':
            return { author: scope.id };
        case 'series':
            return { series: scope.id };
        case 'genre':
            return { genre: scope.id };
        case 'collection':
            return { collection: scope.id };
        case 'curated':
            return { curated_collection: scope.id };
        case 'favorites':
            // Favourites belong to the reader, not to an id.
            return { fav: 'true' };
    }
};

export const autocompleteService = {
    /**
     * getSuggestions is a thin transport: the server gates short prefixes,
     * dedupes and ranks, so the client only stays quiet on a blank query and
     * otherwise passes everything through — failures included. A picker that
     * swallowed an error would render a dead backend as "no results", and the
     * reader must be able to tell those two states apart.
     */
    getSuggestions: async (
        query: string,
        type: 'all' | 'title' | 'author' = 'all',
        scope?: SuggestionScope,
        lang?: string,
        signal?: AbortSignal,
    ): Promise<AutocompleteSuggestion[]> => {
        if (!query || query.trim().length === 0) {
            return [];
        }

        const response = await http.get<AutocompleteResponse>('/books/autocomplete', {
            query: { query, type, lang, ...scopeParams(scope) },
            signal,
        });

        return response?.suggestions ?? [];
    },
};
