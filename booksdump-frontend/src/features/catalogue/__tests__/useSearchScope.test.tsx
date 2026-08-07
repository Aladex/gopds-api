import React from 'react';
import { render, renderHook, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useNavigate } from 'react-router';

import useSearchScope, { searchTitleFromLocation } from '@/features/catalogue/hooks/useSearchScope';
import * as booksApi from '@/api/books';

// A scoped search used to exist only inside one author's list, and its state
// lived in React context — gone on every reload. The scope is now read from
// the route itself, so this hook has to recognise every scoped list (author,
// series, genre, collection, favourites) and say where its first page lives.

vi.mock('@/api/books', () => ({ getAuthor: vi.fn() }));

const authorState = {
    authorId: '',
    authorName: '',
    setAuthorName: vi.fn((name: string) => {
        authorState.authorName = name;
    }),
};
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

// A stable t() matters as much as a faithful one: a fresh function per render
// would re-run the effects that depend on it. Interpolation options come back
// as the key — the assertions below only care which key was chosen.
const translate = (key: string, options?: unknown) => (typeof options === 'string' ? options : key);
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const getAuthor = vi.mocked(booksApi.getAuthor);

const renderAt = (path: string) =>
    renderHook(() => useSearchScope(), {
        wrapper: ({ children }: { children: React.ReactNode }) => (
            <MemoryRouter initialEntries={[path]}>{children}</MemoryRouter>
        ),
    });

beforeEach(() => {
    vi.clearAllMocks();
    authorState.authorId = '';
    authorState.authorName = '';
    // clearAllMocks drops implementations, and an unarmed mock returns
    // undefined where the real call returns a promise.
    getAuthor.mockResolvedValue({ id: 42, full_name: 'Пришвин Михаил' });
});

describe('useSearchScope', () => {
    it('is nowhere off a scoped list', () => {
        const { result } = renderAt('/books/page/1');

        expect(result.current.kind).toBeNull();
        expect(result.current.active).toBe(false);
        expect(result.current.firstPagePath).toBe('');
    });

    it('does not offer a scope on the authors index', () => {
        // Searching authors by name is a library-wide search by definition.
        const { result } = renderAt('/authors/Пришвин/1');

        expect(result.current.kind).toBeNull();
    });

    it("does not treat another user's favourites as a scope of its own", () => {
        const { result } = renderAt('/books/users/favorites/2');

        expect(result.current.kind).toBeNull();
    });

    it("scopes an author's book list to that author", () => {
        const { result } = renderAt('/books/find/author/42/3');

        expect(result.current.kind).toBe('author');
        expect(result.current.id).toBe('42');
        expect(result.current.active).toBe(true);
        expect(result.current.firstPagePath).toBe('/books/find/author/42/1');
    });

    it('scopes a series list to that series', () => {
        const { result } = renderAt('/books/find/category/7/2');

        expect(result.current.kind).toBe('series');
        expect(result.current.id).toBe('7');
        expect(result.current.firstPagePath).toBe('/books/find/category/7/1');
        expect(result.current.label).toBe('searchWithinThisSeries');
    });

    it('scopes a genre list to that genre', () => {
        const { result } = renderAt('/books/find/genre/9/3');

        expect(result.current.kind).toBe('genre');
        expect(result.current.id).toBe('9');
        expect(result.current.firstPagePath).toBe('/books/find/genre/9/1');
        expect(result.current.label).toBe('searchWithinThisGenre');
    });

    it('scopes a collection to that collection', () => {
        const { result } = renderAt('/collections/5/page/4');

        expect(result.current.kind).toBe('collection');
        expect(result.current.id).toBe('5');
        expect(result.current.firstPagePath).toBe('/collections/5/page/1');
        expect(result.current.label).toBe('searchWithinThisCollection');
    });

    it('scopes the favourites list, which has no id', () => {
        const { result } = renderAt('/books/favorite/2');

        expect(result.current.kind).toBe('favorites');
        expect(result.current.id).toBe('');
        expect(result.current.firstPagePath).toBe('/books/favorite/1');
        expect(result.current.label).toBe('searchWithinFavorites');
    });

    it('can be released and taken back', async () => {
        const { result } = renderAt('/books/find/genre/9/1');

        result.current.release();
        await waitFor(() => expect(result.current.active).toBe(false));
        // Still on the list — the reader just asked to look wider — so the
        // panel can offer the scope back rather than hiding it.
        expect(result.current.kind).toBe('genre');

        result.current.reclaim();
        await waitFor(() => expect(result.current.active).toBe(true));
    });

    it('treats crossing into a different scope as a fresh decision', async () => {
        const user = userEvent.setup();
        let scope = { active: false, id: '', release: () => {} } as ReturnType<
            typeof useSearchScope
        >;

        const Probe: React.FC = () => {
            scope = useSearchScope();
            const navigate = useNavigate();
            return (
                <button type="button" onClick={() => navigate('/books/find/author/7/1')}>
                    other
                </button>
            );
        };

        render(
            <MemoryRouter initialEntries={['/books/find/author/42/1']}>
                <Probe />
            </MemoryRouter>,
        );
        expect(scope.active).toBe(true);

        // Releasing one scope must not silence the next: arriving on another
        // author's list turns the scope back on.
        scope.release();
        await waitFor(() => expect(scope.active).toBe(false));

        await user.click(screen.getByRole('button', { name: 'other' }));

        await waitFor(() => expect(scope.id).toBe('7'));
        expect(scope.active).toBe(true);
    });

    it('puts the panel in the mode the scope belongs to on arrival', () => {
        const onEnter = vi.fn();

        renderHook(() => useSearchScope(onEnter), {
            wrapper: ({ children }: { children: React.ReactNode }) => (
                <MemoryRouter initialEntries={['/books/favorite/1']}>{children}</MemoryRouter>
            ),
        });

        expect(onEnter).toHaveBeenCalledTimes(1);
    });

    it('does not announce an arrival that did not happen', () => {
        const onEnter = vi.fn();

        renderHook(() => useSearchScope(onEnter), {
            wrapper: ({ children }: { children: React.ReactNode }) => (
                <MemoryRouter initialEntries={['/books/page/1']}>{children}</MemoryRouter>
            ),
        });

        expect(onEnter).not.toHaveBeenCalled();
    });

    it('names the author from the cache without asking the server', () => {
        authorState.authorId = '42';
        authorState.authorName = 'Пришвин Михаил';
        const { result } = renderAt('/books/find/author/42/1');

        expect(result.current.label).toBe('searchWithinAuthor');
        expect(getAuthor).not.toHaveBeenCalled();
    });

    it('does not wear a cached name that belongs to another author', () => {
        // The context caches one name per id; on a different author's list the
        // cache is simply not this scope's business.
        authorState.authorId = '7';
        authorState.authorName = 'Кто-то другой';
        const { result } = renderAt('/books/find/author/42/1');

        expect(result.current.label).toBe('searchWithinThisAuthor');
    });

    it('looks the name up on a cold arrival, as a pasted URL does', async () => {
        authorState.authorId = '42';

        renderAt('/books/find/author/42/1');

        await waitFor(() => expect(getAuthor).toHaveBeenCalledWith('42'));
        await waitFor(() =>
            expect(authorState.setAuthorName).toHaveBeenCalledWith('Пришвин Михаил'),
        );
    });

    it('still scopes the search when the name cannot be fetched', async () => {
        authorState.authorId = '42';
        getAuthor.mockRejectedValue(new Error('nope'));

        const { result } = renderAt('/books/find/author/42/1');

        await waitFor(() => expect(getAuthor).toHaveBeenCalled());
        expect(result.current.active).toBe(true);
        expect(result.current.label).toBe('searchWithinThisAuthor');
    });
});

describe('searchTitleFromLocation', () => {
    it('reads the query out of a title route', () => {
        expect(
            searchTitleFromLocation('/books/find/title/%D0%B2%D0%BE%D0%B9%D0%BD%D0%B0/3', ''),
        ).toBe('война');
    });

    it('reads the query out of the search params on a scoped route', () => {
        expect(
            searchTitleFromLocation(
                '/books/find/author/42/1',
                '?title=%D0%B2%D0%BE%D0%B9%D0%BD%D0%B0&book_id=5',
            ),
        ).toBe('война');
    });

    it('finds no query on a plain list', () => {
        expect(searchTitleFromLocation('/books/page/1', '')).toBe('');
    });

    it('prefers the route segment when both are present', () => {
        // A book suggestion keeps the broad title route and pins the book by
        // id — the route carries the words, the param only the pointer.
        expect(
            searchTitleFromLocation('/books/find/title/%D0%BC%D0%B8%D1%80/1', '?title=other'),
        ).toBe('мир');
    });
});
