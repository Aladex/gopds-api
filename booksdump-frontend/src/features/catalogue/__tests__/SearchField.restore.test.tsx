import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router';

import { SearchBarProvider, useSearchBar } from '@/context/SearchBarContext';
import AuthorSearch from '@/features/catalogue/AuthorSearch';
import { CatalogueSearchProvider } from '@/features/catalogue/CatalogueSearchContext';
import HeaderSearchField from '@/features/catalogue/HeaderSearchField';
import ListLine from '@/features/catalogue/ListLine';

// The field restored from an address, through the real context and the real
// scope hook. There is no mode to keep in step any more: what the next search
// looks for is read off the page, and these hold that a reader cannot tell.

// The second argument is a default text or, for the author list, interpolation
// values; only the text can stand in for the key.
const translate = (key: string, fallback?: unknown) =>
    typeof fallback === 'string' ? fallback : key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

// No author answers the name, so the list offers its link to the books.
vi.mock('@/api/books', () => ({
    listAuthors: vi.fn().mockResolvedValue({ authors: [], length: 0, total: 0 }),
    getAuthor: vi.fn(),
}));

vi.mock('@/context/FavContext', () => ({
    useFav: () => ({ fav: false, favEnabled: true, setFavEnabled: vi.fn() }),
}));

const authorState = {
    authorId: '',
    authorName: '',
    setAuthorId: vi.fn(),
    setAuthorName: vi.fn(),
    clearAuthorId: vi.fn(),
};
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

const authState = { user: null, isAuthenticated: false, updateLang: vi.fn() };
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));

vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => false,
    default: () => false,
}));

vi.mock('@/shared/layout/BookLanguage', () => ({ default: () => null }));

vi.mock('@/features/catalogue/AutocompleteSearch', () => ({
    default: ({
        value,
        onChange,
        onEnterPressed,
        runTag,
    }: {
        value: string;
        onChange: (v: string) => void;
        onEnterPressed: () => void;
        runTag?: string;
    }) => (
        <>
            <span data-testid="next-search">{runTag}</span>
            <input
                aria-label="searchItem"
                value={value}
                onChange={(event) => onChange(event.target.value)}
                onKeyDown={(event) => {
                    if (event.key === 'Enter') onEnterPressed();
                }}
            />
        </>
    ),
}));

const NAME = 'кинг';
const AUTHORS = `/search?q=${encodeURIComponent(NAME)}&type=authors`;
const BOOKS = `/search?q=${encodeURIComponent(NAME)}&type=books`;

/** What the author list and the browser do around the panel. */
const Journey: React.FC = () => {
    const navigate = useNavigate();
    const { pathname, search } = useLocation();
    const { setSearchItem } = useSearchBar();
    return (
        <>
            <span data-testid="address">{pathname + search}</span>
            <button
                type="button"
                onClick={() => {
                    // A card clears the box on its way through.
                    setSearchItem('');
                    navigate('/author/42');
                }}
            >
                follow-card
            </button>
            <button type="button" onClick={() => navigate(`${AUTHORS}&page=2`)}>
                next-page
            </button>
            <button type="button" onClick={() => navigate(`${BOOKS}&page=2`)}>
                next-books-page
            </button>
            <button type="button" onClick={() => navigate(pathname + search)}>
                same-address
            </button>
            <button type="button" onClick={() => navigate('/author/42')}>
                plain-link
            </button>
            <button type="button" onClick={() => navigate('/books/find/genre/7/1')}>
                genre-link
            </button>
            <button type="button" onClick={() => navigate('/author/42?title=x')}>
                filtered-author
            </button>
            <button type="button" onClick={() => navigate(-1)}>
                back
            </button>
            <button type="button" onClick={() => navigate(1)}>
                forward
            </button>
        </>
    );
};

const openAt = (...entries: string[]) =>
    render(
        <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
            <SearchBarProvider>
                <CatalogueSearchProvider>
                    <HeaderSearchField />
                    <ListLine />
                </CatalogueSearchProvider>
                <Journey />
            </SearchBarProvider>
        </MemoryRouter>,
    );

/** The results under the panel: the real author list, or a stand-in for the books. */
const Results: React.FC = () =>
    useLocation().search.includes('type=authors') ? (
        <AuthorSearch />
    ) : (
        <p data-testid="book-results">Book results</p>
    );

const openWithResults = (...entries: string[]) =>
    render(
        <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
            <SearchBarProvider>
                <CatalogueSearchProvider>
                    <HeaderSearchField />
                    <ListLine />
                </CatalogueSearchProvider>
                <Routes>
                    <Route path="/search" element={<Results />} />
                </Routes>
                <Journey />
            </SearchBarProvider>
        </MemoryRouter>,
    );

const box = () => screen.getByLabelText('searchItem');
const press = (name: string) => userEvent.click(screen.getByRole('button', { name }));

const nextSearch = () => screen.getByTestId('next-search');

const expectAuthorsMode = async (name: string) => {
    await waitFor(() => expect(nextSearch()).toHaveTextContent('search.runAuthors'));
    await waitFor(() => expect(box()).toHaveValue(name));
};

const expectBooksOrHere = async (text: string) => {
    await waitFor(() => expect(nextSearch()).not.toHaveTextContent('search.runAuthors'));
    await waitFor(() => expect(box()).toHaveValue(text));
};

describe('The search field restored from the address', () => {
    it('stays among authors and shows the name on a direct open', async () => {
        openAt(AUTHORS);

        await expectAuthorsMode(NAME);
    });

    it('reads a name made of punctuation back whole', async () => {
        const name = 'а/б?в%г+д';
        openAt(`/search?q=${encodeURIComponent(name)}&type=authors`);

        await expectAuthorsMode(name);
    });

    it('sends the next search to authors, as after typing', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await userEvent.clear(box());
        await userEvent.type(box(), 'толстой{Enter}');

        expect(screen.getByTestId('address')).toHaveTextContent(
            `/search?q=${encodeURIComponent('толстой')}&type=authors`,
        );
    });

    it('goes to books from a book search', async () => {
        openAt(`/search?q=${encodeURIComponent('дюна')}&type=books`);

        await expectBooksOrHere('дюна');
    });

    it('opens on an author with the scope on and an empty box', async () => {
        openAt('/author/42');

        await expectBooksOrHere('');
        expect(screen.getByText('searchWithinThisAuthor')).toBeInTheDocument();
    });

    it('leaves the reader their own words until the address changes', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await userEvent.clear(box());
        await userEvent.type(box(), 'новое');

        expect(box()).toHaveValue('новое');
    });

    it('stays on the name while paging', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await press('next-page');

        await expectAuthorsMode(NAME);
    });
});

describe('The search field on the way through an author', () => {
    it('enters the author with an empty box', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await press('follow-card');

        await expectBooksOrHere('');
        expect(screen.getByText('searchWithinThisAuthor')).toBeInTheDocument();
    });

    it('brings the name back on Back', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);
        await press('follow-card');
        await expectBooksOrHere('');

        await press('back');

        await expectAuthorsMode(NAME);
    });

    it('does not carry the name back in as a title on Forward', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);
        await press('follow-card');
        await expectBooksOrHere('');
        await press('back');
        await expectAuthorsMode(NAME);

        await press('forward');

        await expectBooksOrHere('');
        expect(screen.getByText('searchWithinThisAuthor')).toBeInTheDocument();
    });

    it('drops the name on a link into an author that did not clear the box', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await press('plain-link');

        await expectBooksOrHere('');
    });

    it('keeps the words of a book search on the way into an author, as before', async () => {
        openAt(`/search?q=${encodeURIComponent('дюна')}&type=books`);
        await expectBooksOrHere('дюна');

        await press('plain-link');

        await expectBooksOrHere('дюна');
    });

    it('keeps a title the address of the author carries', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await press('filtered-author');

        await expectBooksOrHere('x');
    });

    it('keeps the words on the way into another kind of list, as before', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await press('genre-link');

        await expectBooksOrHere(NAME);
    });
});

describe('The search field returning to a book search through history', () => {
    /** The empty author list, its link to the books, Back and Forward. */
    const returnToBooks = async () => {
        openWithResults(AUTHORS);
        await userEvent.click(
            await screen.findByRole('link', { name: 'authorSearch.searchBooks' }),
        );
        expect(screen.getByTestId('book-results')).toBeInTheDocument();
        await expectBooksOrHere(NAME);

        await press('back');
        await screen.findByRole('link', { name: 'authorSearch.searchBooks' });
        await expectAuthorsMode(NAME);

        await press('forward');
        expect(screen.getByTestId('book-results')).toBeInTheDocument();
    };

    it('sends the next search to the books after the empty-state link, Back and Forward', async () => {
        await returnToBooks();

        expect(box()).toHaveValue(NAME);
        await userEvent.type(box(), '{Enter}');

        expect(screen.getByTestId('address')).toHaveTextContent('type=books');
    });

    it('leaves the reader their own words after coming back', async () => {
        await returnToBooks();
        await expectBooksOrHere(NAME);

        await userEvent.clear(box());
        await userEvent.type(box(), 'дюна');

        expect(box()).toHaveValue('дюна');
        await expectBooksOrHere('дюна');
    });

    it('keeps to books through a turn of the book pages', async () => {
        openAt(BOOKS);
        await expectBooksOrHere(NAME);

        await press('next-books-page');

        await expectBooksOrHere(NAME);
    });

    it('keeps a draft when an address opens again, even after a cold open', async () => {
        openAt(BOOKS);
        await expectBooksOrHere(NAME);
        await userEvent.clear(box());
        await userEvent.type(box(), 'дюна');

        await press('same-address');

        expect(box()).toHaveValue('дюна');
    });
});
