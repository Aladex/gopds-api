import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';

import BooksList from '@/features/catalogue/BooksList';
import * as booksApi from '@/api/books';
import * as authApi from '@/api/auth';
import type { Book } from '@/api/books';
import * as previewApi from '@/api/preview';

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
    user: {
        username: 'reader',
        first_name: '',
        last_name: '',
        is_superuser: false,
        books_lang: 'ru',
    },
};
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));

const favState = { fav: false, favEnabled: true, setFavEnabled: vi.fn() };
vi.mock('@/context/FavContext', () => ({ useFav: () => favState }));

const authorState = {
    authorId: '',
    setAuthorId: vi.fn(),
};
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

// The card clears the search box when a reader clicks through to a filtered
// list, so the filter applies rather than a stale query.
const searchBarState = {
    searchItem: '',
    setSearchItem: vi.fn(),
    selectedSearch: 'title',
    setSelectedSearch: vi.fn(),
    languages: ['ru'],
    selectedLanguage: 'ru',
    setSelectedLanguage: vi.fn(),
    scopeName: '',
    setScopeName: vi.fn(),
};
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchBarState }));

// The real hook returns { state, dispatch }; BooksList derives
// isBookConverting itself from state.convertingBooks.
const conversionState: {
    convertingBooks: { bookID: number; format: string }[];
    conversionErrors: { bookID: number; format: string; message: string }[];
} = { convertingBooks: [], conversionErrors: [] };
const conversionValue = { state: conversionState, dispatch: vi.fn() };
vi.mock('@/context/BookConversionContext', () => ({
    useBookConversion: () => conversionValue,
}));

vi.mock('@/shared/lib/downloadViaIframe', () => ({ downloadViaIframe: vi.fn() }));

// The preview client is mocked at the boundary the dialog uses, so the tests
// below exercise the real dialog: whether the catalogue can reach it at all
// is precisely what they are here to hold.
vi.mock('@/api/preview', async () => {
    const actual = await vi.importActual<typeof import('@/api/preview')>('@/api/preview');
    return {
        ...actual,
        previewClient: { getPreview: vi.fn(), getChunk: vi.fn(), getImage: vi.fn() },
    };
});

// The conversion backdrop is a full-screen MUI modal. It is a separate concern,
// and leaving it in hides the card behind an overlay whenever a conversion is
// in flight.
vi.mock('@/features/catalogue/ConversionBackdrop', () => ({ default: () => null }));

const getPreview = vi.mocked(previewApi.previewClient.getPreview);
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
        renderAt(
            `/books/find/title/${encodeURIComponent('дюна')}/1`,
            '/books/find/title/:title/:page',
        );

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(expect.objectContaining({ title: 'дюна' }));
    });

    it('asks only for favourites on the favourites route', async () => {
        renderAt('/books/favorite/1', '/books/favorite/:page');

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(expect.objectContaining({ fav: true }));
    });
});

// A scoped search keeps its query in the URL, so every one of these is also
// the reload case: a cold render with no React context in hand must derive
// the same API query.
describe('BooksList scoped search', () => {
    it('filters an author list by the query in the URL', async () => {
        renderAt(
            `/books/find/author/42/1?title=${encodeURIComponent('война')}`,
            '/books/find/author/:id/:page',
        );

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(
            expect.objectContaining({ author: '42', title: 'война' }),
        );
    });

    it('filters a series list by the query in the URL', async () => {
        renderAt(
            `/books/find/category/9/1?title=${encodeURIComponent('война')}`,
            '/books/find/category/:id/:page',
        );

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(
            expect.objectContaining({ series: '9', title: 'война' }),
        );
    });

    it('filters a genre list by the query in the URL', async () => {
        renderAt(
            `/books/find/genre/5/1?title=${encodeURIComponent('война')}`,
            '/books/find/genre/:id/:page',
        );

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(
            expect.objectContaining({ genre: '5', title: 'война' }),
        );
    });

    it('filters a collection by the query in the URL', async () => {
        renderAt(
            `/collections/7/page/1?title=${encodeURIComponent('война')}`,
            '/collections/:id/page/:page',
        );

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(
            expect.objectContaining({ curated_collection: '7', title: 'война' }),
        );
    });

    it('filters favourites by the query in the URL', async () => {
        renderAt(`/books/favorite/1?title=${encodeURIComponent('война')}`, '/books/favorite/:page');

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(
            expect.objectContaining({ fav: true, title: 'война' }),
        );
    });

    it('pins an exact book when the URL carries one', async () => {
        renderAt(
            `/books/find/author/42/1?title=${encodeURIComponent('х')}&book_id=555`,
            '/books/find/author/:id/:page',
        );

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        expect(listBooks).toHaveBeenCalledWith(
            expect.objectContaining({ author: '42', title: 'х', book_id: '555' }),
        );
    });

    it('pins an exact book on a broad title search without borrowing an author', async () => {
        // The context may still hold the author whose list the reader came
        // from; a broad title search is broad, and must not inherit it.
        authorState.authorId = '42';
        renderAt(
            `/books/find/title/${encodeURIComponent('дюна')}/1?book_id=555`,
            '/books/find/title/:title/:page',
        );

        await waitFor(() => expect(listBooks).toHaveBeenCalled());
        const query = listBooks.mock.calls[0][0];
        expect(query).toEqual(expect.objectContaining({ title: 'дюна', book_id: '555' }));
        expect(query.author).toBeUndefined();
    });
});

describe('BooksList language change', () => {
    afterEach(() => {
        authState.user.books_lang = 'ru';
    });

    it('resets a deep page to page one of the same list, keeping its search', async () => {
        authState.user.books_lang = 'ru';
        let currentUrl = '';
        const UrlProbe: React.FC = () => {
            const { pathname, search } = useLocation();
            React.useEffect(() => {
                currentUrl = pathname + search;
            }, [pathname, search]);
            return null;
        };
        const tree = () => (
            <MemoryRouter
                initialEntries={[`/books/find/author/42/7?title=${encodeURIComponent('х')}`]}
            >
                <Routes>
                    <Route path="/books/find/author/:id/:page" element={<BooksList />} />
                    <Route path="*" element={<BooksList />} />
                </Routes>
                <UrlProbe />
            </MemoryRouter>
        );
        const view = render(tree());

        await waitFor(() => expect(listBooks).toHaveBeenCalled());

        // The reader switches the books language: in the real app that arrives
        // as a context update, here as a mutation plus a render pass. A fresh
        // element each pass — re-rendering the identical reference bails out.
        authState.user.books_lang = 'en';
        view.rerender(tree());

        // Used to drop the reader onto the global first page, losing both the
        // author and the query the deep page belonged to.
        await waitFor(() =>
            expect(currentUrl).toBe(`/books/find/author/42/1?title=${encodeURIComponent('х')}`),
        );
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
            expect(
                screen.getByRole('button', { name: new RegExp(`^\\W*${format}$`) }),
            ).toBeInTheDocument();
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

// A book's own language is only worth saying when the list is not already
// filtered to one. Reading the whole library is exactly when it is.
describe('BooksList book language', () => {
    afterEach(() => {
        authState.user.books_lang = 'ru';
    });

    it('stays quiet while the catalogue is filtered to one language', async () => {
        authState.user.books_lang = 'ru';
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        expect(screen.queryByText(/Русский/)).toBeNull();
    });

    it('names the language of each book once the whole library is on show', async () => {
        authState.user.books_lang = 'all';
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        expect(screen.getByText(/Русский/)).toBeInTheDocument();
    });

    it('treats a reader who was never asked as reading everything', async () => {
        authState.user.books_lang = '';
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        expect(screen.getByText(/Русский/)).toBeInTheDocument();
    });
});

/**
 * The dialog and the card were built separately and each was green on its own
 * while nothing in the application rendered the dialog at all — a reader had
 * no way to open a book. These tests are about the join, not about either
 * side: they click what a reader clicks and watch for the book's own text.
 */
describe('BooksList opens the reader', () => {
    beforeEach(() => {
        getPreview.mockResolvedValue({
            revision: 'rev-1',
            chunk_count: 1,
            toc: [],
            images: [],
            first_chunk: '<p data-testid="opened-portion">Первые слова книги.</p>',
        });
    });

    it('opens the book the reader asked for, in its own language', async () => {
        // Two books, and the second one is the one clicked. With a single
        // book on the page, wiring that always opens the first — or passes a
        // hardcoded id and language — is indistinguishable from wiring that
        // works.
        listBooks.mockResolvedValue({
            books: [
                makeBook(),
                makeBook({ id: 42, title: 'The Other Book', lang: 'en' }),
            ],
            length: 2,
        });
        renderAt('/books/page/1');
        await screen.findByText('The Other Book');

        const [, second] = screen.getAllByRole('button', { name: 'bookRead' });
        await userEvent.click(second);

        expect(await screen.findByTestId('opened-portion')).toBeInTheDocument();
        expect(getPreview).toHaveBeenCalledWith(42, expect.anything());
        // And the column is marked with that book's language, which is what
        // hyphenation and screen readers go by.
        expect(screen.getByTestId('preview-text-column')).toHaveAttribute('lang', 'en');
    });

    it('closes the book, and can open it again', async () => {
        // Every other test here only opens the dialog, so the whole closing
        // path was unobserved: replacing onOpenChange with a no-op left the
        // suite green while the book could not be put down.
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        await userEvent.click(screen.getByRole('button', { name: 'bookRead' }));
        await screen.findByTestId('opened-portion');

        await userEvent.click(screen.getByRole('button', { name: 'previewClose' }));
        await waitFor(() => expect(screen.queryByTestId('opened-portion')).toBeNull());

        // And the catalogue still works afterwards: a dialog that closes but
        // leaves its book behind would open the next reader on the wrong one.
        await userEvent.click(screen.getByRole('button', { name: 'bookRead' }));
        expect(await screen.findByTestId('opened-portion')).toBeInTheDocument();
        expect(getPreview).toHaveBeenCalledTimes(2);
    });

    it('hands the keyboard back to the control that opened the book', async () => {
        // Measured in Chrome before this was written: closing the dialog left
        // focus on <body>, which drops a keyboard reader at the top of the
        // catalogue. The dialog library is supposed to restore it and did
        // not, so the page does it itself — which is also why this is
        // testable here at all.
        renderAt('/books/page/1');
        await screen.findByText('Заклятые в любви');

        const trigger = screen.getByRole('button', { name: 'bookRead' });
        trigger.focus();
        await userEvent.click(trigger);
        await screen.findByTestId('opened-portion');

        await userEvent.click(screen.getByRole('button', { name: 'previewClose' }));
        await waitFor(() => expect(screen.queryByTestId('opened-portion')).toBeNull());

        expect(trigger).toHaveFocus();
    });

    it('offers the reader only where the pipeline can read the book', async () => {
        // A mixed page, so the rule is observed as "exactly the FB2 one" and
        // not as "not EPUB": a condition like format !== 'epub' would pass a
        // page of EPUBs and fail here on the MOBI.
        listBooks.mockResolvedValue({
            books: [
                makeBook({ id: 1, title: 'An EPUB', format: 'epub' }),
                makeBook({ id: 2, title: 'An FB2', format: 'fb2' }),
                makeBook({ id: 3, title: 'A MOBI', format: 'mobi' }),
            ],
            length: 3,
        });
        renderAt('/books/page/1');
        await screen.findByText('A MOBI');

        const offered = screen.getAllByRole('button', { name: 'bookRead' });
        expect(offered).toHaveLength(1);

        await userEvent.click(offered[0]);
        await screen.findByTestId('opened-portion');
        expect(getPreview).toHaveBeenCalledWith(2, expect.anything());
    });
});
