import React from 'react';
import { render, renderHook, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useNavigate } from 'react-router';

import useAuthorScope from '@/features/catalogue/hooks/useAuthorScope';
import * as booksApi from '@/api/books';

// A search made inside one author's list is confined to that author. The scope
// used to be a third entry in the search-mode dropdown, unnamed and all but
// invisible; it is now shown beside the query box, which means this hook has to
// get two things right — when the scope comes on, and whose name it carries.

vi.mock('@/api/books', () => ({ getAuthor: vi.fn() }));

const authorState = {
    authorId: '',
    authorName: '',
    setAuthorName: vi.fn((name: string) => {
        authorState.authorName = name;
    }),
};
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

const getAuthor = vi.mocked(booksApi.getAuthor);

const renderAt = (path: string) =>
    renderHook(() => useAuthorScope(), {
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

describe('useAuthorScope', () => {
    it('is unavailable off an author list', () => {
        const { result } = renderAt('/books/page/1');

        expect(result.current.available).toBe(false);
        expect(result.current.active).toBe(false);
    });

    it("comes on when the reader arrives on an author's list", () => {
        authorState.authorId = '42';
        const { result } = renderAt('/books/find/author/42/1');

        expect(result.current.available).toBe(true);
        expect(result.current.active).toBe(true);
        expect(result.current.id).toBe('42');
    });

    it('puts the panel in the mode the scope belongs to on arrival', () => {
        // A reader who got here by searching for a name is still in "authors by
        // name", where a scope means nothing and is not even shown. They had to
        // know to switch modes themselves to search the books in front of them.
        authorState.authorId = '42';
        const onEnter = vi.fn();

        renderHook(() => useAuthorScope(onEnter), {
            wrapper: ({ children }: { children: React.ReactNode }) => (
                <MemoryRouter initialEntries={['/books/find/author/42/1']}>{children}</MemoryRouter>
            ),
        });

        expect(onEnter).toHaveBeenCalledTimes(1);
    });

    it('does not announce an arrival that did not happen', () => {
        const onEnter = vi.fn();

        renderHook(() => useAuthorScope(onEnter), {
            wrapper: ({ children }: { children: React.ReactNode }) => (
                <MemoryRouter initialEntries={['/books/page/1']}>{children}</MemoryRouter>
            ),
        });

        expect(onEnter).not.toHaveBeenCalled();
    });

    it('can be taken back after being released', async () => {
        // Releasing it used to be one-way: the chip vanished and the only route
        // back to a confined search was out of the author's list and in again.
        authorState.authorId = '42';
        const { result } = renderAt('/books/find/author/42/1');

        result.current.release();
        await waitFor(() => expect(result.current.active).toBe(false));

        result.current.reclaim();

        await waitFor(() => expect(result.current.active).toBe(true));
        expect(result.current.id).toBe('42');
    });

    it('stays off once released', async () => {
        authorState.authorId = '42';
        const { result } = renderAt('/books/find/author/42/1');

        result.current.release();

        await waitFor(() => expect(result.current.active).toBe(false));
        // Still available — the reader is on the list, they just asked to look
        // wider — so the panel can offer the scope back rather than hiding it.
        expect(result.current.available).toBe(true);
    });

    it('does not offer the scope on the authors index', () => {
        const { result } = renderAt('/authors/Пришвин/1');

        expect(result.current.available).toBe(false);
    });

    it('uses the name already in hand without asking the server', () => {
        authorState.authorId = '42';
        authorState.authorName = 'Пришвин Михаил';
        const { result } = renderAt('/books/find/author/42/1');

        expect(result.current.name).toBe('Пришвин Михаил');
        expect(getAuthor).not.toHaveBeenCalled();
    });

    it('looks the name up when arriving without one, as a pasted URL does', async () => {
        authorState.authorId = '42';
        getAuthor.mockResolvedValue({ id: 42, full_name: 'Пришвин Михаил' });

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
        expect(result.current.id).toBe('42');
        expect(result.current.name).toBe('');
    });

    it('comes back on after crossing out of an author list and into another', async () => {
        const user = userEvent.setup();
        authorState.authorId = '42';
        let scope = { active: false, release: () => {} } as ReturnType<typeof useAuthorScope>;

        const Probe: React.FC = () => {
            scope = useAuthorScope();
            const navigate = useNavigate();
            return (
                <button type="button" onClick={() => navigate('/books/page/1')}>
                    away
                </button>
            );
        };

        render(
            <MemoryRouter initialEntries={['/books/find/author/42/1']}>
                <Probe />
            </MemoryRouter>,
        );
        expect(scope.active).toBe(true);

        // Releasing then leaving must not leave the release stuck on: the next
        // author's list is a fresh decision.
        scope.release();
        await waitFor(() => expect(scope.active).toBe(false));

        await user.click(screen.getByRole('button', { name: 'away' }));

        await waitFor(() => expect(scope.available).toBe(false));
    });
});
