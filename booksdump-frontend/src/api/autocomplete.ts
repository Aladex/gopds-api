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
        authorId?: string,
        lang?: string,
        signal?: AbortSignal,
    ): Promise<AutocompleteSuggestion[]> => {
        if (!query || query.trim().length === 0) {
            return [];
        }

        const response = await http.get<AutocompleteResponse>('/books/autocomplete', {
            query: { query, type, author: authorId, lang },
            signal,
        });

        return response?.suggestions ?? [];
    },
};
