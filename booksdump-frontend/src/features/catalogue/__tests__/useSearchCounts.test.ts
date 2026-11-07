import { act, renderHook, waitFor } from '@testing-library/react';

import * as booksApi from '@/api/books';
import { useSearchCounts } from '@/features/catalogue/hooks/useSearchCounts';

vi.mock('@/api/books', () => ({ listBooks: vi.fn(), listAuthors: vi.fn() }));
const listBooks = vi.mocked(booksApi.listBooks);
const listAuthors = vi.mocked(booksApi.listAuthors);

const empty = { books: [], authors: [] };

beforeEach(() => {
    vi.clearAllMocks();
    listBooks.mockResolvedValue({ books: empty.books, length: 1158 });
    listAuthors.mockResolvedValue({ authors: empty.authors, length: 22, total: 213 });
});

describe('useSearchCounts', () => {
    it('reads each tab’s exact size from a one-row request in the reader’s language', async () => {
        const { result } = renderHook(() => useSearchCounts('кинг', 'ru'));

        await waitFor(() => expect(result.current).toEqual({ books: 1158, authors: 213 }));
        expect(listBooks).toHaveBeenCalledWith({ limit: 1, offset: 0, title: 'кинг', lang: 'ru' });
        expect(listAuthors).toHaveBeenCalledWith({
            limit: 1,
            offset: 0,
            author: 'кинг',
            lang: 'ru',
        });
    });

    it('asks nothing with no search', () => {
        const { result } = renderHook(() => useSearchCounts('', 'ru'));

        expect(listBooks).not.toHaveBeenCalled();
        expect(result.current).toEqual({});
    });

    it('shows nothing of the old search while the new one is on its way', async () => {
        const { result, rerender } = renderHook(({ q }) => useSearchCounts(q, 'ru'), {
            initialProps: { q: 'кинг' },
        });
        await waitFor(() => expect(result.current.books).toBe(1158));

        listBooks.mockReturnValue(new Promise(() => {}));
        listAuthors.mockReturnValue(new Promise(() => {}));
        rerender({ q: 'война' });

        expect(result.current).toEqual({});
    });

    it('shows nothing of another language', async () => {
        const { result, rerender } = renderHook(({ lang }) => useSearchCounts('кинг', lang), {
            initialProps: { lang: 'ru' },
        });
        await waitFor(() => expect(result.current.books).toBe(1158));

        listBooks.mockReturnValue(new Promise(() => {}));
        listAuthors.mockReturnValue(new Promise(() => {}));
        rerender({ lang: 'en' });

        expect(result.current).toEqual({});
    });

    it('drops an answer that comes back after the search moved on', async () => {
        let late: (value: never) => void = () => {};
        listBooks.mockReturnValueOnce(new Promise((resolve) => (late = resolve as never)));
        listAuthors.mockResolvedValueOnce({ authors: [], length: 0, total: 1 });
        const { result, rerender } = renderHook(({ q }) => useSearchCounts(q, 'ru'), {
            initialProps: { q: 'старое' },
        });

        rerender({ q: 'новое' });
        await waitFor(() => expect(result.current.books).toBe(1158));
        await act(async () => late({ books: [], length: 7 } as never));

        expect(result.current.books).toBe(1158);
    });

    it('leaves out a count it could not get, and keeps the other', async () => {
        listAuthors.mockRejectedValue(new Error('down'));
        const { result } = renderHook(() => useSearchCounts('кинг', 'ru'));

        await waitFor(() => expect(result.current.books).toBe(1158));
        expect(result.current.authors).toBeUndefined();
    });

    it('reads zero as zero', async () => {
        listBooks.mockResolvedValue({ books: [], length: 0 });
        listAuthors.mockResolvedValue({ authors: [], length: 0, total: 0 });
        const { result } = renderHook(() => useSearchCounts('кинг', 'en'));

        await waitFor(() => expect(result.current).toEqual({ books: 0, authors: 0 }));
    });
});
