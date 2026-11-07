import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router';

import SectionRow from '@/shared/layout/SectionRow';
import { CONTENT_COLUMN } from '@/shared/layout/breakpoints';
import * as booksApi from '@/api/books';

// The row under the header is the one place the sections live, on a phone and on
// a desktop. jsdom lays nothing out, so where the mark ends up was measured in a
// browser; these hold which link is current, what each one's address keeps, and
// who is offered what.

const translation = { t: (key: string) => key };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/api/books', () => ({ listBooks: vi.fn(), listAuthors: vi.fn() }));
const listBooks = vi.mocked(booksApi.listBooks);
const listAuthors = vi.mocked(booksApi.listAuthors);

const authState = { user: { username: 'reader', is_superuser: false, books_lang: 'ru' } };
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
    authState.user = { username: 'reader', is_superuser: false, books_lang: 'ru' };
    listBooks.mockResolvedValue({ books: [], length: 1158 });
    listAuthors.mockResolvedValue({ authors: [], length: 22, total: 213 });
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
    ])('stays on Книги through %s, a route of no section', (_, path) => {
        // The reader came from the catalogue and the row should say so.
        renderRow(path);
        expect(current()).toHaveTextContent('booksTab');
    });

    it('does not light Книги in a section the row does not draw', () => {
        // Matched against every section, drawn from a phone's own. Matching only
        // what is drawn would fall through to Книги inside the admin panel.
        phone.current = true;
        authState.user = { username: 'root', is_superuser: true, books_lang: 'ru' };
        renderRow('/admin/users');

        expect(screen.queryByRole('link', { current: 'page' })).toBeNull();
    });
});

describe('SectionRow width', () => {
    it('keeps its sections in the list column, on a band as wide as the screen', () => {
        renderRow('/books/page/1');

        const row = screen.getByRole('navigation');
        expect(row).toHaveClass(...CONTENT_COLUMN.split(' '));
        expect(row.parentElement?.className).not.toContain('max-w');
        expect(row.parentElement).toHaveClass('bg-card');
    });
});

describe('SectionRow sections', () => {
    it('gives a phone the three a reader has', () => {
        phone.current = true;
        authState.user = { username: 'root', is_superuser: true, books_lang: 'ru' };
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

        authState.user = { username: 'root', is_superuser: true, books_lang: 'ru' };
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
        // Книги is the way out of a scoped list: left in the box, the words
        // would read as a search the list is not showing.
        searchState.searchItem = 'дюна';
        renderRow('/author/42?title=x');

        await userEvent.click(link('booksTab'));

        expect(where).toBe('/books/page/1');
        expect(searchState.setSearchItem).toHaveBeenCalledWith('');
    });

    it('leaves a new tab’s click alone', async () => {
        searchState.searchItem = 'дюна';
        renderRow('/author/42?title=x');
        const user = userEvent.setup();

        const books = link('booksTab');
        books.addEventListener('click', (event) => event.preventDefault());
        await user.keyboard('{Control>}');
        await user.click(books);
        await user.keyboard('{/Control}');

        expect(searchState.setSearchItem).not.toHaveBeenCalled();
    });
});

describe('SectionRow on a search', () => {
    const SEARCH = `/search?q=${encodeURIComponent('кинг')}&type=books`;
    const AUTHORS = `/search?q=${encodeURIComponent('кинг')}&type=authors`;
    const tab = (name: RegExp) => screen.getByRole('link', { name });

    it('turns its first places into the two answers, with their sizes', async () => {
        renderRow(SEARCH);

        expect(await screen.findByRole('link', { name: /booksTab.*1\D?158/ })).toBeInTheDocument();
        expect(await screen.findByRole('link', { name: /authorsTab.*213/ })).toBeInTheDocument();
    });

    it('marks the tab of the list on screen', () => {
        const { unmount } = renderRow(SEARCH);
        expect(current()).toHaveTextContent('booksTab');
        unmount();

        renderRow(AUTHORS);
        expect(current()).toHaveTextContent('authorsTab');
    });

    it('goes between the tabs on the search’s own words, not the box’s', async () => {
        searchState.searchItem = 'черновик';
        renderRow(SEARCH);

        await userEvent.click(tab(/authorsTab/));

        expect(where).toBe(AUTHORS);
    });

    it('leaves out Подборки on a phone, where the row has no room', () => {
        phone.current = true;
        renderRow(SEARCH);

        expect(screen.queryByRole('link', { name: 'collectionsTab' })).toBeNull();
        expect(link('favouritesTab')).toBeInTheDocument();
    });

    it('keeps Подборки and Админ from 601 up', () => {
        authState.user = { username: 'root', is_superuser: true, books_lang: 'ru' };
        renderRow(SEARCH);

        expect(link('collectionsTab')).toBeInTheDocument();
        expect(link('adminTab')).toBeInTheDocument();
    });

    it('carries the words into favourites from a search', async () => {
        searchState.searchItem = 'кинг';
        renderRow(SEARCH);

        await userEvent.click(link('favouritesTab'));

        expect(where).toBe(`/books/favorite/1?title=${encodeURIComponent('кинг')}`);
    });

    it('takes no other list’s address for a search', () => {
        renderRow('/books/page/1?q=x');

        expect(screen.queryByRole('link', { name: /authorsTab/ })).toBeNull();
        expect(listBooks).not.toHaveBeenCalled();
    });

    it('asks for no counts away from a search', () => {
        renderRow('/books/page/1');

        expect(listBooks).not.toHaveBeenCalled();
        expect(listAuthors).not.toHaveBeenCalled();
    });

    it('gives way in steps when the row does not fit', () => {
        // jsdom has no widths, so the row reports overflow until it has been
        // tightened as far as this asks.
        const width = vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get');
        const client = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get');
        width.mockImplementation(function (this: HTMLElement) {
            return this.dataset.squeeze === '2' ? 300 : 340;
        });
        client.mockReturnValue(312);
        try {
            renderRow(SEARCH);

            expect(screen.getByRole('navigation').dataset.squeeze).toBe('2');
        } finally {
            width.mockRestore();
            client.mockRestore();
        }
    });

    it('stays as drawn where the row fits', () => {
        renderRow(SEARCH);

        expect(screen.getByRole('navigation').dataset.squeeze).toBe('0');
    });
});
