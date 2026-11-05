import {
    authorPath,
    legacyAuthorAddress,
    legacySearchAddress,
    pageFromQuery,
    searchPath,
} from '@/features/catalogue/addresses';

describe('searchPath', () => {
    it('writes the address a search is shared by', () => {
        expect(searchPath('война')).toBe(`/search?q=${encodeURIComponent('война')}&type=books`);
        expect(searchPath('кинг', 'authors')).toBe(
            `/search?q=${encodeURIComponent('кинг')}&type=authors`,
        );
    });

    it.each(['а/б', '100%', 'что?', 'c++', 'a b', '%D0%B0', '&q=x', '#7'])(
        'carries %s through the query whole',
        (query) => {
            const address = new URL(searchPath(query), 'https://booksdump.test');

            expect(address.pathname).toBe('/search');
            expect(address.hash).toBe('');
            expect(address.searchParams.get('q')).toBe(query);
        },
    );
});

describe('authorPath', () => {
    it('takes a legacy number and an entity id alike', () => {
        const entity = '01a168d0-a113-741a-b69d-53b91f3c7eb9';

        expect(authorPath(42)).toBe('/author/42');
        expect(authorPath(entity)).toBe(`/author/${entity}`);
    });
});

describe('pageFromQuery', () => {
    it('reads a later page', () => {
        expect(pageFromQuery(new URLSearchParams('page=3'))).toBe(3);
    });

    it.each(['', 'page=', 'page=0', 'page=-1', 'page=abc', 'page=2x', 'page=1.5'])(
        'treats "%s" as the first page',
        (search) => {
            expect(pageFromQuery(new URLSearchParams(search))).toBe(1);
        },
    );
});

describe('legacySearchAddress', () => {
    it('moves a book search', () => {
        expect(legacySearchAddress('война', 'books', '1', '')).toEqual({
            pathname: '/search',
            search: `?q=${encodeURIComponent('война')}&type=books`,
        });
    });

    it('moves an author search and its page', () => {
        expect(legacySearchAddress('кинг', 'authors', '2', '')).toEqual({
            pathname: '/search',
            search: `?q=${encodeURIComponent('кинг')}&type=authors&page=2`,
        });
    });

    it('keeps the parameters the old address carried', () => {
        const { search } = legacySearchAddress('дюна', 'books', '3', '?book_id=555&title=other');
        const params = new URLSearchParams(search);

        expect(params.get('book_id')).toBe('555');
        expect(params.get('title')).toBe('other');
        expect(params.get('page')).toBe('3');
    });

    it('lets the path win over a parameter that says otherwise', () => {
        const { search } = legacySearchAddress('дюна', 'books', '1', '?q=x&type=authors&page=9');
        const params = new URLSearchParams(search);

        expect(params.get('q')).toBe('дюна');
        expect(params.get('type')).toBe('books');
        expect(params.has('page')).toBe(false);
    });

    it('reads a page that is no number as the first', () => {
        const { search } = legacySearchAddress('дюна', 'books', 'abc', '');

        expect(new URLSearchParams(search).has('page')).toBe(false);
    });
});

describe('legacyAuthorAddress', () => {
    it('moves an author with no page to carry', () => {
        expect(legacyAuthorAddress('42', '1', '')).toEqual({ pathname: '/author/42', search: '' });
    });

    it('keeps the page, the search and the pinned book', () => {
        const { pathname, search } = legacyAuthorAddress(
            '01a168d0-a113-741a-b69d-53b91f3c7eb9',
            '3',
            `?title=${encodeURIComponent('х')}&book_id=5`,
        );
        const params = new URLSearchParams(search);

        expect(pathname).toBe('/author/01a168d0-a113-741a-b69d-53b91f3c7eb9');
        expect(params.get('title')).toBe('х');
        expect(params.get('book_id')).toBe('5');
        expect(params.get('page')).toBe('3');
    });
});
