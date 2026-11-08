import { useCallback, useEffect, useRef } from 'react';
import { useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';

import * as authApi from '@/api/auth';
import * as booksApi from '@/api/books';
import type { Book } from '@/api/books';

import { useAuth } from '@/context/AuthContext';
import { useFav } from '@/context/FavContext';
import type { BooksListAction } from '@/features/catalogue/hooks/useBooksQuery';

/**
 * useFavouriteToggle owns adding and removing a favourite.
 *
 * The list updates before the request completes so the star responds
 * immediately, and the same action is dispatched again on failure to put it
 * back. The reader is re-read afterwards because the backend decides whether
 * any favourites remain. When the last one goes the reader stays where they are,
 * the list empties if it is still the one they did this on, and the page says
 * what favourites are for.
 *
 * The refreshed reader replaces the one held in AuthContext, which owns that
 * record. It used to be written to a copy of the flag kept beside it, leaving
 * the original saying the reader still had favourites after the last one went.
 */
export function useFavouriteToggle(
    dispatch: React.Dispatch<BooksListAction>,
    notify: (message: string) => void,
) {
    const { t } = useTranslation();
    const { updateUser } = useAuth();
    const { fav } = useFav();

    // The list on screen, as of now and not as of the click: the reader can leave
    // while the request is out, and one element serves every list route, so the
    // answer would otherwise land on whatever they went to.
    const { key } = useLocation();
    const shownList = useRef(key);
    useEffect(() => {
        shownList.current = key;
    }, [key]);

    return useCallback(
        async (book: Book, shownID?: number) => {
            const list = shownList.current;
            // A card of variants flips as one, by its key. Starring saves the
            // file the card shows; unstarring takes every file of the card off.
            const card = book.variants?.key ?? book.id;
            const target = shownID ?? book.id;
            try {
                dispatch({ type: 'TOGGLE_FAV', payload: card });

                if (book.variants && book.fav) {
                    await booksApi.toggleFavourite(target, false, true);
                } else {
                    await booksApi.toggleFavourite(target, !book.fav);
                }

                const currentUser = await authApi.getCurrentUser();
                updateUser(currentUser);
                // None left means nothing on this list either, wherever it was
                // paged to. Only the last one: a card that goes while others
                // remain would take the way to undo it with it.
                if (fav && !currentUser.have_favs && shownList.current === list) {
                    dispatch({ type: 'FETCH_SUCCESS', payload: { books: [], totalPages: 0 } });
                }
                notify(!book.fav ? t('bookFavAddedSuccessfully') : t('bookFavRemovedSuccessfully'));
            } catch (error) {
                console.error('Error favoriting book', error);
                dispatch({ type: 'TOGGLE_FAV', payload: card });
                notify(!book.fav ? t('errorAddingFavorite') : t('errorRemovingFavorite'));
            }
        },
        [dispatch, fav, notify, t, updateUser],
    );
}
