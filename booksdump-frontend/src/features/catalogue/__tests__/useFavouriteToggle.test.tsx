import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';

import * as authApi from '@/api/auth';
import * as booksApi from '@/api/books';
import type { Book } from '@/api/books';
import { FavProvider, useFav } from '@/context/FavContext';
import { useFavouriteToggle } from '@/features/catalogue/hooks/useFavouriteToggle';

/*
 * The favourites filter may only be offered while the reader actually has
 * favourites, and the backend is what decides that. These go through the real
 * FavProvider rather than a mocked context, so they assert what the reader ends
 * up seeing — the filter on or off, the page they land on — and not which
 * setter happened to be called on the way.
 */

vi.mock('@/api/auth', () => ({ getCurrentUser: vi.fn(), updateCurrentUser: vi.fn() }));
vi.mock('@/api/books', () => ({ toggleFavourite: vi.fn() }));

const translate = (key: string) => key;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translate }) }));

/*
 * The reader record has to be reactive, as the real AuthContext is: updating it
 * must re-render whoever reads it. A plain mutable object would let the toggle
 * write a new record that nothing ever notices.
 */
const store = vi.hoisted(() => {
    let user: Record<string, unknown> | null = null;
    const listeners = new Set<() => void>();
    const setResetFavCallback = () => {};
    return {
        get: () => user,
        set: (next: Record<string, unknown> | null) => {
            user = next;
            listeners.forEach((listener) => listener());
        },
        subscribe: (listener: () => void) => {
            listeners.add(listener);
            return () => listeners.delete(listener);
        },
        setResetFavCallback,
    };
});

vi.mock('@/context/AuthContext', async () => {
    const React = await import('react');
    return {
        useAuth: () => ({
            user: React.useSyncExternalStore(store.subscribe, store.get, store.get),
            updateUser: store.set,
            setResetFavCallback: store.setResetFavCallback,
        }),
    };
});

const mockedGetUser = vi.mocked(authApi.getCurrentUser);
const mockedToggle = vi.mocked(booksApi.toggleFavourite);

const BOOK = { id: 7, fav: false } as Book;

let currentPath = '';
const dispatch = vi.fn();
const notify = vi.fn();

const Probe: React.FC<{ book?: Book }> = ({ book = BOOK }) => {
    const { favEnabled, fav } = useFav();
    const toggle = useFavouriteToggle(dispatch, notify);
    const { pathname } = useLocation();
    React.useEffect(() => {
        currentPath = pathname;
    }, [pathname]);
    return (
        <>
            <span data-testid="enabled">{String(favEnabled)}</span>
            <span data-testid="on-fav-page">{String(fav)}</span>
            <button type="button" onClick={() => toggle(book)}>
                toggle
            </button>
        </>
    );
};

function renderAt(path: string, book?: Book) {
    currentPath = path;
    return render(
        <MemoryRouter initialEntries={[path]}>
            <FavProvider>
                <Probe book={book} />
            </FavProvider>
        </MemoryRouter>,
    );
}

beforeEach(() => {
    vi.clearAllMocks();
    store.set({ username: 'reader', have_favs: true });
    mockedToggle.mockResolvedValue(undefined as never);
    mockedGetUser.mockResolvedValue({ username: 'reader', have_favs: true } as never);
});

describe('useFavouriteToggle', () => {
    it('shows the star before the server has answered', async () => {
        const user = userEvent.setup();
        renderAt('/books/page/1');

        await user.click(screen.getByRole('button', { name: 'toggle' }));

        // The list is told first, so the star responds to the tap rather than to
        // the round trip.
        expect(dispatch).toHaveBeenCalledWith({ type: 'TOGGLE_FAV', payload: 7 });
        await waitFor(() => expect(mockedToggle).toHaveBeenCalledWith(7, true));
    });

    it('turns the filter off once the last favourite is gone', async () => {
        const user = userEvent.setup();
        mockedGetUser.mockResolvedValue({ username: 'reader', have_favs: false } as never);
        renderAt('/books/page/1', { id: 7, fav: true } as Book);
        expect(screen.getByTestId('enabled')).toHaveTextContent('true');

        await user.click(screen.getByRole('button', { name: 'toggle' }));

        await waitFor(() => expect(screen.getByTestId('enabled')).toHaveTextContent('false'));
    });

    it('turns the filter on with the first favourite', async () => {
        const user = userEvent.setup();
        store.set({ username: 'reader', have_favs: false });
        mockedGetUser.mockResolvedValue({ username: 'reader', have_favs: true } as never);
        renderAt('/books/page/1');
        expect(screen.getByTestId('enabled')).toHaveTextContent('false');

        await user.click(screen.getByRole('button', { name: 'toggle' }));

        await waitFor(() => expect(screen.getByTestId('enabled')).toHaveTextContent('true'));
    });

    it('carries the reader off the favourites page when it empties', async () => {
        const user = userEvent.setup();
        mockedGetUser.mockResolvedValue({ username: 'reader', have_favs: false } as never);
        renderAt('/books/favorite/1', { id: 7, fav: true } as Book);

        await user.click(screen.getByRole('button', { name: 'toggle' }));

        // Staying would leave them looking at an empty list.
        await waitFor(() => expect(currentPath).toBe('/books/page/1'));
    });

    it('leaves them on the favourites page while any remain', async () => {
        const user = userEvent.setup();
        renderAt('/books/favorite/1', { id: 7, fav: true } as Book);

        await user.click(screen.getByRole('button', { name: 'toggle' }));

        await waitFor(() => expect(notify).toHaveBeenCalled());
        expect(currentPath).toBe('/books/favorite/1');
    });

    it('puts the star back when the request fails', async () => {
        const user = userEvent.setup();
        mockedToggle.mockRejectedValue(new Error('nope'));
        renderAt('/books/page/1');

        await user.click(screen.getByRole('button', { name: 'toggle' }));

        // Dispatched twice with the same action: once hopefully, once to undo.
        await waitFor(() => expect(dispatch).toHaveBeenCalledTimes(2));
        expect(notify).toHaveBeenCalledWith('errorAddingFavorite');
    });

    it('knows when it is on the favourites page', () => {
        renderAt('/books/favorite/1');

        expect(screen.getByTestId('on-fav-page')).toHaveTextContent('true');
    });
});
