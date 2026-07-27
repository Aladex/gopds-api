import { useCallback } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';

import * as authApi from '@/api/auth';
import * as booksApi from '@/api/books';
import type { Book } from '@/api/books';

import { useFav } from '@/context/FavContext';
import type { BooksListAction } from '@/features/catalogue/hooks/useBooksQuery';

/**
 * useFavouriteToggle owns adding and removing a favourite.
 *
 * The list updates before the request completes so the star responds
 * immediately, and the same action is dispatched again on failure to put it
 * back. The reader is re-read afterwards because the backend decides whether
 * any favourites remain, which is what enables the favourites filter — and if
 * the last one just went, staying on /books/favorite would show an empty page.
 *
 * Extracted from BooksList unchanged.
 */
export function useFavouriteToggle(
    dispatch: React.Dispatch<BooksListAction>,
    notify: (message: string) => void,
) {
    const { t } = useTranslation();
    const location = useLocation();
    const navigate = useNavigate();
    const fav = useFav();

    return useCallback(
        async (book: Book) => {
            try {
                dispatch({ type: 'TOGGLE_FAV', payload: book.id });

                await booksApi.toggleFavourite(book.id, !book.fav);

                const currentUser = await authApi.getCurrentUser();
                fav.setFavEnabled(Boolean(currentUser.have_favs));
                if (location.pathname.includes('/books/favorite') && !currentUser.have_favs) {
                    navigate('/books/page/1');
                }
                notify(!book.fav ? t('bookFavAddedSuccessfully') : t('bookFavRemovedSuccessfully'));
            } catch (error) {
                console.error('Error favoriting book', error);
                dispatch({ type: 'TOGGLE_FAV', payload: book.id });
                notify(!book.fav ? t('errorAddingFavorite') : t('errorRemovingFavorite'));
            }
        },
        [dispatch, fav, location.pathname, navigate, notify, t],
    );
}
