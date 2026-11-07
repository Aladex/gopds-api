import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router';

import SectionRow from '@/shared/layout/SectionRow';

// The row under the header is the one place the sections live, on a phone and on
// a desktop. jsdom lays nothing out, so where the mark ends up was measured in a
// browser; these hold which link is current, what each one's address keeps, and
// who is offered what.

const translation = { t: (key: string) => key };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const authState = { user: { username: 'reader', is_superuser: false } };
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));

const searchState = {
    searchItem: '',
    setSearchItem: vi.fn((value: string) => {
        searchState.searchItem = value;
    }),
};
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchState }));

const phone = { current: false };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => phone.current,
    default: () => phone.current,
}));

let where = '';
const Where: React.FC = () => {
    const { pathname, search } = useLocation();
    React.useEffect(() => {
        where = pathname + search;
    }, [pathname, search]);
    return null;
};

const renderRow = (path: string) =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <SectionRow />
            <Where />
        </MemoryRouter>,
    );

const current = () => screen.getByRole('link', { current: 'page' });
const link = (name: string) => screen.getByRole('link', { name });

beforeEach(() => {
    vi.clearAllMocks();
    phone.current = false;
    authState.user = { username: 'reader', is_superuser: false };
    searchState.searchItem = '';
});

describe('SectionRow marks', () => {
    it('draws exactly one mark, kept out of the accessibility tree', () => {
        const { container } = renderRow('/books/page/1');

        const marks = container.querySelectorAll('nav span[aria-hidden]');
        expect(marks).toHaveLength(1);
        expect(current()).toHaveTextContent('booksTab');
    });

    it('marks the section a deep route belongs to', () => {
        renderRow('/books/page/7');
        expect(current()).toHaveTextContent('booksTab');
    });

    it('marks Подборки inside a collection', () => {
        renderRow('/collections/48/page/1');
        expect(current()).toHaveTextContent('collectionsTab');
    });

    it('marks Избранное on the favourites list', () => {
        renderRow('/books/favorite/2');
        expect(current()).toHaveTextContent('favouritesTab');
    });

    it.each([
        ['an author', '/author/42'],
        ['a series', '/books/find/category/7/1'],
        ['a genre', '/books/find/genre/9/1'],
        ['a search', '/search?q=x&type=books'],
    ])('stays on Книги through %s, a route of no section', (_, path) => {
        // The reader came from the catalogue and the row should say so.
        renderRow(path);
        expect(current()).toHaveTextContent('booksTab');
    });

    it('does not light Книги in a section the row does not draw', () => {
        // Matched against every section, drawn from a phone's own. Matching only
        // what is drawn would fall through to Книги inside the admin panel.
        phone.current = true;
        authState.user = { username: 'root', is_superuser: true };
        renderRow('/admin/users');

        expect(screen.queryByRole('link', { current: 'page' })).toBeNull();
    });
});

describe('SectionRow sections', () => {
    it('gives a phone the three a reader has', () => {
        phone.current = true;
        authState.user = { username: 'root', is_superuser: true };
        renderRow('/books/page/1');

        expect(screen.getAllByRole('link').map((item) => item.textContent)).toEqual([
            'booksTab',
            'favouritesTab',
            'collectionsTab',
        ]);
    });

    it('gives a superuser the admin section from 601 up, and a reader none', () => {
        const { unmount } = renderRow('/books/page/1');
        expect(screen.queryByRole('link', { name: 'adminTab' })).toBeNull();
        unmount();

        authState.user = { username: 'root', is_superuser: true };
        renderRow('/admin');
        expect(current()).toHaveTextContent('adminTab');
    });

    it('keeps Избранное a link for a reader who has none yet', () => {
        // The word stays and explains itself, where the heart went dead.
        renderRow('/books/page/1');
        expect(link('favouritesTab')).toHaveAttribute('href', '/books/favorite/1');
    });
});

describe('SectionRow and the words in the box', () => {
    it('carries the words into favourites', async () => {
        searchState.searchItem = 'дюна';
        renderRow('/books/page/1');

        await userEvent.click(link('favouritesTab'));

        expect(where).toBe(`/books/favorite/1?title=${encodeURIComponent('дюна')}`);
    });

    it('hands the words back to a broad search on leaving favourites', async () => {
        searchState.searchItem = 'дюна';
        renderRow(`/books/favorite/1?title=${encodeURIComponent('дюна')}`);

        await userEvent.click(link('booksTab'));

        expect(where).toBe(`/search?q=${encodeURIComponent('дюна')}&type=books`);
        expect(searchState.setSearchItem).not.toHaveBeenCalled();
    });

    it('goes plainly to favourites with an empty box', async () => {
        renderRow('/books/page/1');

        await userEvent.click(link('favouritesTab'));

        expect(where).toBe('/books/favorite/1');
    });

    it('takes the words with it to the catalogue', async () => {
        // Книги is the way out of a search: left in the box, the words would
        // read as a search the list is not showing.
        searchState.searchItem = 'дюна';
        renderRow('/search?q=x&type=books');

        await userEvent.click(link('booksTab'));

        expect(where).toBe('/books/page/1');
        expect(searchState.setSearchItem).toHaveBeenCalledWith('');
    });

    it('leaves a new tab’s click alone', async () => {
        searchState.searchItem = 'дюна';
        renderRow('/search?q=x&type=books');
        const user = userEvent.setup();

        const books = link('booksTab');
        books.addEventListener('click', (event) => event.preventDefault());
        await user.keyboard('{Control>}');
        await user.click(books);
        await user.keyboard('{/Control}');

        expect(searchState.setSearchItem).not.toHaveBeenCalled();
    });
});
