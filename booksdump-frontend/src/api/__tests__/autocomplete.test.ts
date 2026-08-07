import { autocompleteService } from '@/api/autocomplete';
import { ApiError } from '@/api/errors';

// The autocomplete client is a thin transport: the server gates short
// prefixes, dedupes and ranks. The client stays quiet on a blank query and
// otherwise passes everything through — failures included, because a picker
// that shows nothing for a dead backend must not look like "no results".

let fetchSpy: ReturnType<typeof vi.fn>;

function jsonResponse(body: unknown, status = 200) {
    return new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
    });
}

beforeEach(() => {
    fetchSpy = vi.fn();
    globalThis.fetch = fetchSpy as unknown as typeof fetch;
});

describe('autocompleteService.getSuggestions', () => {
    it('asks the server from three characters', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ suggestions: [] }));

        await autocompleteService.getSuggestions('ёжи');

        expect(fetchSpy).toHaveBeenCalledTimes(1);
    });

    it('stays quiet for a blank query', async () => {
        const result = await autocompleteService.getSuggestions('   ');

        expect(result).toEqual([]);
        expect(fetchSpy).not.toHaveBeenCalled();
    });

    it('passes type, author and the all-languages code through', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ suggestions: [] }));

        await autocompleteService.getSuggestions(
            'война',
            'author',
            { kind: 'author', id: '42' },
            'all',
        );

        const url = fetchSpy.mock.calls[0][0] as string;
        expect(url).toContain('type=author');
        expect(url).toContain('author=42');
        expect(url).toContain('lang=all');
    });

    it('names each list the picker can be confined to', async () => {
        // Favourites belong to the reader, so they travel as a flag rather
        // than an id; every other list is one the backend already knows by id.
        for (const [scope, expected] of [
            [{ kind: 'author', id: '42' }, 'author=42'],
            [{ kind: 'series', id: '7' }, 'series=7'],
            [{ kind: 'genre', id: '9' }, 'genre=9'],
            [{ kind: 'collection', id: '3' }, 'collection=3'],
            [{ kind: 'curated', id: '5' }, 'curated_collection=5'],
            [{ kind: 'favorites', id: '' }, 'fav=true'],
        ] as const) {
            fetchSpy.mockClear();
            fetchSpy.mockResolvedValue(jsonResponse({ suggestions: [] }));

            await autocompleteService.getSuggestions('война', 'title', scope, 'ru');

            expect(fetchSpy.mock.calls[0][0] as string).toContain(expected);
        }
    });

    it('forwards the abort signal to the transport', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ suggestions: [] }));
        const controller = new AbortController();

        await autocompleteService.getSuggestions(
            'война',
            'all',
            undefined,
            undefined,
            controller.signal,
        );

        const init = fetchSpy.mock.calls[0][1] as RequestInit;
        expect(init.signal).toBe(controller.signal);
    });

    it('keeps same-titled suggestions from different authors distinct', async () => {
        fetchSpy.mockResolvedValue(
            jsonResponse({
                suggestions: [
                    { value: 'Сто лет одиночества', type: 'book', id: 1, secondary: 'Толстой Лев' },
                    {
                        value: 'Сто лет одиночества',
                        type: 'book',
                        id: 2,
                        secondary: 'Булгаков Михаил',
                    },
                ],
            }),
        );

        const result = await autocompleteService.getSuggestions('сто лет одиночества');

        expect(result).toHaveLength(2);
        expect(result[0].secondary).toBe('Толстой Лев');
        expect(result[1].secondary).toBe('Булгаков Михаил');
    });

    it('keeps the author books count', async () => {
        fetchSpy.mockResolvedValue(
            jsonResponse({
                suggestions: [{ value: 'Толстой Лев', type: 'author', id: 2, books_count: 10 }],
            }),
        );

        const result = await autocompleteService.getSuggestions('толстой', 'author');

        expect(result[0].books_count).toBe(10);
    });

    it('lets a transport failure reach the caller instead of becoming an empty list', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ message: 'boom' }, 500));

        await expect(autocompleteService.getSuggestions('война')).rejects.toBeInstanceOf(ApiError);
    });
});
