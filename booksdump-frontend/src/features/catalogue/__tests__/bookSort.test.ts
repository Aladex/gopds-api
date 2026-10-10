import { canSortByAuthor, withSort } from '@/features/catalogue/bookSort';

/*
 * The server sorts an ordinary list by author: the whole catalogue and the
 * books of an author, a series or a genre. A search keeps its ranking, and
 * favourites and collections keep their own order, so the sort is neither
 * offered nor sent there.
 */
describe('which lists sort by author', () => {
    it.each([
        ['/books/page/3', ''],
        ['/books/find/author/7/1', ''],
        ['/books/find/category/3/2', '?sort=author'],
        ['/books/find/genre/1/1', ''],
    ])('the ordinary list %s%s can', (path, search) => {
        expect(canSortByAuthor(path, new URLSearchParams(search))).toBe(true);
    });

    it.each([
        ['/books/find/title/%D0%B4/1', ''],
        ['/books/page/1', '?title=дюна'],
        ['/books/find/author/7/1', '?book_id=5'],
        ['/books/favorite/1', ''],
        ['/books/users/favorites/1', ''],
        ['/collections/5/page/1', ''],
    ])('%s%s cannot', (path, search) => {
        expect(canSortByAuthor(path, new URLSearchParams(search))).toBe(false);
    });
});

describe('the sort in the address', () => {
    it('is added and removed, the rest of the query kept', () => {
        expect(withSort('', 'author')).toBe('?sort=author');
        expect(withSort('?x=1', 'author')).toBe('?x=1&sort=author');
        expect(withSort('?x=1&sort=author', null)).toBe('?x=1');
        expect(withSort('?sort=author', null)).toBe('');
    });
});
