import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';

import * as authApi from '@/api/auth';
import * as booksApi from '@/api/books';
import type { Book } from '@/api/books';

import { useAuth } from '@/context/AuthContext';
import type { BooksListAction } from '@/features/catalogue/hooks/useBooksQuery';

/**
 * useFavouriteToggle owns adding and removing a favourite.
 *
 * The list updates before the request completes so the star responds
 * immediately, and the same action is dispatched again on failure to put it
 * back. The reader is re-read afterwards because the backend decides whether
 * any favourites remain. When the last one goes the reader stays where they are:
 * the page says what favourites are for, and the section is still in the row.
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

    return useCallback(
        async (book: Book) => {
            try {
                dispatch({ type: 'TOGGLE_FAV', payload: book.id });

                await booksApi.toggleFavourite(book.id, !book.fav);

                const currentUser = await authApi.getCurrentUser();
                updateUser(currentUser);
                notify(!book.fav ? t('bookFavAddedSuccessfully') : t('bookFavRemovedSuccessfully'));
            } catch (error) {
                console.error('Error favoriting book', error);
                dispatch({ type: 'TOGGLE_FAV', payload: book.id });
                notify(!book.fav ? t('errorAddingFavorite') : t('errorRemovingFavorite'));
            }
        },
        [dispatch, notify, t, updateUser],
    );
}
