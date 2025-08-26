import { fetchWithAuth } from './config';

export interface AutocompleteSuggestion {
  value: string;
  type: 'book' | 'author';
  id?: number;
}

export interface AutocompleteResponse {
  suggestions: AutocompleteSuggestion[];
}

export const autocompleteService = {
  getSuggestions: async (query: string, type: 'all' | 'title' | 'author' = 'all', authorId?: string, lang?: string): Promise<AutocompleteSuggestion[]> => {
    try {
      // Strict validation for empty values
      if (!query || query.trim().length < 4) {
        return [];
      }

      const params: any = { query, type };
      if (authorId) {
        params.author = authorId;
      }
      if (lang) {
        params.lang = lang;
      }

      const response = await fetchWithAuth.get<AutocompleteResponse>('/books/autocomplete', {
        params
      });

      if (!response.data?.suggestions) {
        return [];
      }

      // Filter null/undefined values
      const validSuggestions = response.data.suggestions.filter(suggestion =>
        suggestion && suggestion.value && suggestion.value.trim() !== ''
      );

      // Additional frontend deduplication for better reliability
      const uniqueSuggestions = new Map<string, AutocompleteSuggestion>();

      validSuggestions.forEach(suggestion => {
        // Normalize value for comparison (lowercase, remove extra spaces)
        const normalizedValue = suggestion.value.toLowerCase().trim();

        // If this normalized value doesn't exist yet, add it
        if (!uniqueSuggestions.has(normalizedValue)) {
          uniqueSuggestions.set(normalizedValue, suggestion);
        }
      });

      // Return array of unique suggestions
      return Array.from(uniqueSuggestions.values());

    } catch (error) {
      console.error('Error fetching autocomplete suggestions:', error);
      return [];
    }
  }
};
