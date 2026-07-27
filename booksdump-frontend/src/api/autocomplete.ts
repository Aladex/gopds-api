import { http } from './http';

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

      const response = await http.get<AutocompleteResponse>('/books/autocomplete', {
        query: { query, type, author: authorId, lang },
      });

      if (!response?.suggestions) {
        return [];
      }

      // Filter null/undefined values
      const validSuggestions = response.suggestions.filter(suggestion =>
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
