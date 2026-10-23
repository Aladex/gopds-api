import { getAuthor, listBooks } from '@/api/books';

// listBooks is the boundary every catalogue screen reads through, so the
// wire's one dishonesty is absorbed here: a nil Go slice marshals to JSON
// null, and a zero-hit search answers { books: null, length: 0 }. Consumers
// are promised an array.

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

describe('listBooks', () => {
    it('normalises a null books field into an empty array', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ books: null, length: 0 }));

        const page = await listBooks({ limit: 10, offset: 0 });

        expect(page.books).toEqual([]);
        expect(page.length).toBe(0);
    });

    it('passes a real page of books through untouched', async () => {
        const book = { id: 1, title: 'Война и мир' };
        fetchSpy.mockResolvedValue(jsonResponse({ books: [book], length: 5 }));

        const page = await listBooks({ limit: 10, offset: 0 });

        expect(page.books).toEqual([book]);
        expect(page.length).toBe(5);
    });
});

describe('getAuthor', () => {
    // The server reads a legacy author as a number and an entity as its UUID
    // string; a UUID through Number() would arrive as null.
    const sentBody = () => JSON.parse(String((fetchSpy.mock.calls[0][1] as RequestInit).body));

    it('sends a legacy author as a number', async () => {
        fetchSpy.mockResolvedValue(jsonResponse({ id: 42, full_name: 'Пришвин Михаил' }));

        await getAuthor('42');

        expect(sentBody()).toEqual({ author_id: 42 });
    });

    it('sends an entity as its UUID', async () => {
        const id = '01a168d0-a113-741a-b69d-53b91f3c7eb9';
        fetchSpy.mockResolvedValue(jsonResponse({ id, full_name: 'Стивен Кинг' }));

        await getAuthor(id);

        expect(sentBody()).toEqual({ author_id: id });
    });
});
