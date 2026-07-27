import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';

import BooksList from '../BooksList';
import * as booksApi from '@/api/books';
import * as authApi from '@/api/auth';
import type { Book } from '@/api/books';

// Characterisation tests written before the list is rebuilt on shadcn. They
// cover behaviour rather than markup: which query the route produces, what the
// favourite toggle does on success and on failure, and which controls a
// superuser sees that an ordinary reader does not.

// A stable t: useTranslation must not hand back a fresh function each render,
// or effects keyed on it loop forever.
const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/api/books', () => ({
    listBooks: vi.fn(),
    toggleFavourite: vi.fn(),
}));
vi.mock('@/api/auth', () => ({ getCurrentUser: vi.fn() }));
vi.mock('@/api/admin', () => ({ updateBook: vi.fn() }));

const authState = {
    user: { username: 'reader', first_name: '', last_name: '', is_superuser: false, books_lang: 'ru' },
};
vi.mock('../../../context/AuthContext', () => ({ useAuth: () => authState }));

const favState = { fav: false, favEnabled: true, setFavEnabled: vi.fn() };
vi.mock('../../../context/FavContext', () => ({ useFav: () => favState }));

const authorState = {
    authorId: '',
    authorBook: '',
    setAuthorId: vi.fn(),
    clearAuthorBook: vi.fn(),
};
vi.mock('../../../context/AuthorContext', () => ({ useAuthor: () => authorState }));

// AuthorsList, CategotiesList and GenresList all reach for the search context to
// seed the box when a reader clicks through to a filtered list.
const searchBarState = {
    searchItem: '',
    setSearchItem: vi.fn(),
    selectedSearch: 'title',
    setSelectedSearch: vi.fn(),
    languages: ['ru'],
    selectedLanguage: 'ru',
    setSelectedLanguage: vi.fn(),
};
vi.mock('../../../context/SearchBarContext', () => ({ useSearchBar: () => searchBarState }));

// The real hook returns { state, dispatch }; BooksList derives
// isBookConverting itself from state.convertingBooks.
const conversionState: {
    convertingBooks: { bookID: number; format: string }[];
    conversionErrors: { bookID: number; format: string; message: string }[];
} = { convertingBooks: [], conversionErrors: [] };
const conversionValue = { state: conversionState, dispatch: vi.fn() };
vi.mock('../../../context/BookConversionContext', () => ({
    useBookConversion: () => conversionValue,
}));

vi.mock('../../helpers/downloadViaIframe', () => ({ downloadViaIframe: vi.fn() }));

// The conversion backdrop is a full-screen MUI modal. It is a separate concern,
// and leaving it in hides the card behind an overlay whenever a conversion is
// in flight.
vi.mock('../../hooks/convertingBooks', () => ({ default: () => null }));

const listBooks = vi.mocked(booksApi.listBooks);
const toggleFavourite = vi.mocked(booksApi.toggleFavourite);
const getCurrentUser = vi.mocked(authApi.getCurrentUser);

function makeBook(over: Partial<Book> = {}): Book {
    return {
        id: 1,
        title: 'Заклятые в любви',
        authors: [{ id: 7, full_name: 'Райнер Анастасия' }],
        series: [],
        genres: [{ id: 3, genre: 'love_contemporary' }],
        annotation: 'Атмосфера студенческой жизни и уют кампуса.',
        filename: 'book-1',
        cover: true,
        registerdate: '2026-07-07T20:18:00Z',
        docdate: '2024-08-26',
        lang: 'ru',
        fav: false,
        approved: true,
        path: 'fb2-1-2.zip',
        format: 'fb2',
        favorite_count: 0,
        ...over,
    };
}

function renderAt(path: string, route = '/books/page/:page') {
    return render(
        <MemoryRouter initialEntries={[path]}>
            <Routes>
                <Route path={route} element={<BooksList />} />
                <Route path="*" element={<BooksList />} />
            </Routes>
        </MemoryRouter>,
    );
}

beforeEach(() => {
    vi.clearAllMocks();
    authState.user.is_superuser = false;
    conversionState.convertingBooks = [];
    conversionState.conversionErrors = [];
    listBooks.mockResolvedValue({ books: [makeBook()], length: 1 });
    toggleFavourite.mockResolvedValue({ have_favs: true });
    getCurrentUser.mockResolvedValue({
        username: 'reader',
        first_name: '',
        last_name: '',
        is_superuser: false,
        have_favs: true,
    });
});

describe('BooksList query building', () => {
    it('asks for a page worth of books with the reader language', async () => {
        renderAt('/books/page/3');

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(
            expect.objectContaining({ limit: 10, offset: 20, lang: 'ru' }),
        );
    });

    it('filters by author on the author route', async () => {
        renderAt('/books/find/author/42/1', '/books/find/author/:id/:page');

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(expect.objectContaining({ author: '42' }));
    });

    it('filters by series on the category route', async () => {
        renderAt('/books/find/category/9/1', '/books/find/category/:id/:page');

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(expect.objectContaining({ series: '9' }));
    });

    it('filters by genre on the genre route', async () => {
        renderAt('/books/find/genre/5/1', '/books/find/genre/:id/:page');

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(expect.objectContaining({ genre: '5' }));
    });

    it('decodes the title from the url', async () => {
        renderAt(`/books/find/title/${encodeURIComponent('дюна')}/1`, '/books/find/title/:title/:page');

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(expect.objectContaining({ title: 'дюна' }));
    });

    it('asks only for favourites on the favourites route', async () => {
        renderAt('/books/favorite/1', '/books/favorite/:page');

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(expect.objectContaining({ fav: true }));
    });
});

describe('BooksList rendering', () => {
    it('renders the books it received', async () => {
        renderAt('/books/page/1');

        expect(await screen.findByText('Заклятые в любви')).toBeInTheDocument();
    });

    it('reports an empty result instead of an empty page', async () => {
        listBooks.mockResolvedValue({ books: [], length: 0 });
        renderAt('/books/page/1');

        expect(await screen.findByText('noBooksFound')).toBeInTheDocument();
    });

    it('offers all four download formats', async () => {
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        // ZIP is the archived FB2; the label is short so the four sit in an even
        // block under the cover.
        for (const format of ['ZIP', 'FB2', 'EPUB', 'MOBI']) {
            expect(screen.getByRole('button', { name: new RegExp(`^\\W*${format}$`) }))
                .toBeInTheDocument();
        }
    });

    it('disables a format while that conversion is running', async () => {
        conversionState.convertingBooks = [{ bookID: 1, format: 'epub' }];
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        // Real disabled buttons now, so the guarantee is in the semantics rather
        // than in a pointer-events rule. The name carries a spinner while the
        // conversion runs.
        expect(screen.getByRole('button', { name: /EPUB$/ })).toBeDisabled();
        expect(screen.getByRole('button', { name: /^MOBI$/ })).toBeEnabled();
    });

    it('says so when a book has no annotation', async () => {
        listBooks.mockResolvedValue({ books: [makeBook({ annotation: '' })], length: 1 });
        renderAt('/books/page/1');

        expect(await screen.findByText('noAnnotation')).toBeInTheDocument();
    });
});

describe('BooksList moderation controls', () => {
    it('shows an ordinary reader only the favourite toggle', async () => {
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        expect(screen.queryByRole('button', { name: 'rescanBook' })).not.toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'editBook' })).not.toBeInTheDocument();
    });

    it('shows a superuser the rescan and edit controls', async () => {
        authState.user.is_superuser = true;
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        expect(screen.getByRole('button', { name: 'rescanBook' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'editBook' })).toBeInTheDocument();
    });
});

describe('BooksList favourite toggle', () => {
    /** The favourite control now carries an accessible name, so ask for it. */
    function favouriteButton() {
        return screen.getByRole('button', { name: /bookFav(Add|Remove)/ });
    }

    it('sends the new state and refreshes the reader', async () => {
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        await userEvent.click(favouriteButton());

        await waitFor(() => expect(toggleFavourite).toHaveBeenCalledWith(1, true));
        await waitFor(() => expect(getCurrentUser).toHaveBeenCalled());
    });

    it('keeps the list usable when the toggle fails', async () => {
        toggleFavourite.mockRejectedValue(new Error('nope'));
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        await userEvent.click(favouriteButton());

        await waitFor(() => expect(toggleFavourite).toHaveBeenCalled());
        // The book is still listed: a failed toggle must not drop the row.
        expect(screen.getByText('Заклятые в любви')).toBeInTheDocument();
    });
});
