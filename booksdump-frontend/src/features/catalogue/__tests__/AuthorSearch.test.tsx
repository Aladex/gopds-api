import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router';

import AuthorSearch from '@/features/catalogue/AuthorSearch';
import * as booksApi from '@/api/books';

// The page is a stop on the way to a book list: what matters is that it asks
// for the authors under the reader's own books language, says how many books
// each one holds, and carries the right author through on a click.

// A stable t, so effects keyed on it do not loop. Counts are rendered through
// i18next plurals in the application; here the key and the count are enough to
// tell the number apart from the name.
const translate = (key: string, options?: Record<string, unknown> | string) => {
    if (options && typeof options === 'object') {
        if ('count' in options) return `${key}:${String(options.count)}`;
        if ('query' in options) return `${key}:${String(options.query)}`;
    }
    return typeof options === 'string' ? options : key;
};
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/api/books', () => ({ listAuthors: vi.fn() }));

const authState: { user: { books_lang?: string } | null } = { user: { books_lang: 'ru' } };
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));

const authorState = {
    setAuthorName: vi.fn(),
};
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

const searchBarState = { setSearchItem: vi.fn(), clearSelectedSearch: vi.fn() };
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchBarState }));

// The pager is reduced to the number it was given, so a stale page count is
// directly visible to an assertion.
vi.mock('@/features/catalogue/BookPagination', () => ({
    default: ({
        totalPages,
        currentPage,
        baseUrl,
        pending,
    }: {
        totalPages: number;
        currentPage: number;
        baseUrl: string;
        pending?: boolean;
    }) => (
        <div
            data-testid="pager"
            data-page={currentPage}
            data-url={baseUrl}
            data-pending={pending ? 'yes' : 'no'}
        >
            {totalPages}
        </div>
    ),
}));

const listAuthors = vi.mocked(booksApi.listAuthors);

const Landing: React.FC = () => {
    const { pathname, search } = useLocation();
    return <div data-testid="landing">{pathname + search}</div>;
};

/** The list the type names, the way SearchPage picks it. */
const SearchRoute: React.FC = () =>
    useLocation().search.includes('type=authors') ? <AuthorSearch /> : <Landing />;

const renderPage = (path = '/search?q=%D0%A2%D0%BE%D0%BB%D1%81%D1%82%D0%BE%D0%B9&type=authors') =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <Routes>
                <Route path="/search" element={<SearchRoute />} />
                <Route path="/author/:id" element={<Landing />} />
            </Routes>
        </MemoryRouter>,
    );

beforeEach(() => {
    vi.clearAllMocks();
    authState.user = { books_lang: 'ru' };
    listAuthors.mockResolvedValue({
        authors: [
            { id: 1, full_name: 'Достоевский Федор', books_count: 184 },
            { id: 2, full_name: 'Достоевский Ф.', books_count: 1 },
        ],
        length: 1,
    });
});

describe('AuthorSearch', () => {
    it('asks for authors in the language the reader is browsing', async () => {
        renderPage();

        await waitFor(() => expect(listAuthors).toHaveBeenCalled());
        expect(listAuthors).toHaveBeenCalledWith(expect.objectContaining({ lang: 'ru' }));
    });

    // Without a language the book list shows everything, and so must the count.
    it('asks for every language when the reader has chosen none', async () => {
        authState.user = {};
        renderPage();

        await waitFor(() => expect(listAuthors).toHaveBeenCalled());
        expect(listAuthors).toHaveBeenCalledWith(expect.objectContaining({ lang: '' }));
    });

    it('decodes the name from the URL before searching', async () => {
        renderPage();

        await waitFor(() => expect(listAuthors).toHaveBeenCalled());
        expect(listAuthors).toHaveBeenCalledWith(expect.objectContaining({ author: 'Толстой' }));
    });

    it('reads a name made of punctuation exactly, once', async () => {
        const name = 'а/б?в%г+д';
        renderPage(`/search?q=${encodeURIComponent(name)}&type=authors`);

        await waitFor(() => expect(listAuthors).toHaveBeenCalled());
        expect(listAuthors).toHaveBeenCalledWith(expect.objectContaining({ author: name }));
    });

    it('asks for the page the query names', async () => {
        renderPage('/search?q=x&type=authors&page=3');

        await waitFor(() => expect(listAuthors).toHaveBeenCalled());
        expect(listAuthors).toHaveBeenCalledWith(expect.objectContaining({ offset: 20 }));
    });

    it('tells the pager when its total belongs to another language', async () => {
        // A search with pages keeps its pager through the next load, and the
        // jump sheet must not open on a total that is about to change.
        const found = {
            authors: [{ id: 1, full_name: 'Достоевский Федор', books_count: 184 }],
            length: 3,
            total: 60,
        };
        listAuthors.mockResolvedValue(found);
        const tree = () => (
            <MemoryRouter initialEntries={['/search?q=%D0%A2&type=authors']}>
                <Routes>
                    <Route path="/search" element={<SearchRoute />} />
                </Routes>
            </MemoryRouter>
        );
        const view = render(tree());
        await waitFor(() => expect(screen.getByTestId('pager').dataset.pending).toBe('no'));
        await waitFor(() => expect(screen.getByTestId('pager')).toHaveTextContent('3'));

        let finish: (value: typeof found) => void = () => {};
        listAuthors.mockReturnValueOnce(
            new Promise((resolve) => {
                finish = resolve;
            }),
        );
        authState.user = { books_lang: 'en' };
        view.rerender(tree());

        await waitFor(() => expect(screen.getByTestId('pager').dataset.pending).toBe('yes'));

        finish(found);
        await waitFor(() => expect(screen.getByTestId('pager').dataset.pending).toBe('no'));
    });

    it('does not call its own total stale while paging through it', async () => {
        listAuthors.mockResolvedValue({
            authors: [{ id: 1, full_name: 'Достоевский Федор', books_count: 184 }],
            length: 3,
            total: 60,
        });
        const Turn: React.FC = () => {
            const navigate = useNavigate();
            return (
                <button
                    type="button"
                    onClick={() => navigate('/search?q=%D0%A2&type=authors&page=2')}
                >
                    next
                </button>
            );
        };
        render(
            <MemoryRouter initialEntries={['/search?q=%D0%A2&type=authors']}>
                <Turn />
                <Routes>
                    <Route path="/search" element={<SearchRoute />} />
                </Routes>
            </MemoryRouter>,
        );
        await waitFor(() => expect(screen.getByTestId('pager')).toHaveTextContent('3'));

        let finish: (value: never) => void = () => {};
        listAuthors.mockReturnValueOnce(
            new Promise((resolve) => {
                finish = resolve as (value: never) => void;
            }),
        );
        await userEvent.click(screen.getByRole('button', { name: 'next' }));

        await waitFor(() => expect(screen.getByTestId('pager').dataset.page).toBe('2'));
        expect(screen.getByTestId('pager').dataset.pending).toBe('no');
        finish({ authors: [], length: 3, total: 60 } as never);
    });

    it('hands the pager the search it is paging through', async () => {
        // The pager builds the next address from this; without the query,
        // page two of a search is a search for nothing.
        renderPage('/search?q=x&type=authors&page=3');

        const pager = await screen.findByTestId('pager');
        expect(pager).toHaveAttribute('data-url', '/search?q=x&type=authors&page=3');
        expect(pager).toHaveAttribute('data-page', '3');
    });

    it('says how many books each author holds', async () => {
        renderPage();

        expect(await screen.findByText('Достоевский Федор')).toBeInTheDocument();
        expect(screen.getByText('bookCount:184')).toBeInTheDocument();
        expect(screen.getByText('bookCount:1')).toBeInTheDocument();
    });

    // The count is what the row promises; a missing one is left unsaid rather
    // than shown as a zero the server never counted.
    it('says nothing where there is no count', async () => {
        listAuthors.mockResolvedValue({
            authors: [{ id: 3, full_name: 'Толстой А' }],
            length: 1,
        });
        renderPage();

        expect(await screen.findByRole('link', { name: /Толстой А/ })).toBeInTheDocument();
        expect(screen.queryByText(/^bookCount:/)).not.toBeInTheDocument();
    });

    it('carries the chosen author through to their books', async () => {
        renderPage();

        const row = await screen.findByRole('link', { name: /Достоевский Федор/ });
        await userEvent.click(row);

        expect(screen.getByTestId('landing')).toHaveTextContent('/author/1');

        expect(authorState.setAuthorName).toHaveBeenCalledWith('Достоевский Федор');
        // A stale query in the box would fight the filter on the next page.
        expect(searchBarState.setSearchItem).toHaveBeenCalledWith('');
    });

    it('reports an empty search rather than an empty list', async () => {
        listAuthors.mockResolvedValue({ authors: [], length: 0 });
        renderPage();

        expect(await screen.findByText('noAuthorsFound:Толстой')).toBeInTheDocument();
    });

    it('survives the request failing', async () => {
        listAuthors.mockRejectedValue(new Error('nope'));
        vi.spyOn(console, 'error').mockImplementation(() => {});
        renderPage();

        expect(await screen.findByText('noAuthorsFound:Толстой')).toBeInTheDocument();
    });

    it('asks for ten authors a page', async () => {
        renderPage();

        await waitFor(() => expect(listAuthors).toHaveBeenCalled());
        expect(listAuthors).toHaveBeenCalledWith(expect.objectContaining({ limit: 10 }));
    });

    it('offers the same words in the book search from an empty result', async () => {
        listAuthors.mockResolvedValue({ authors: [], length: 0 });
        renderPage();

        await userEvent.click(
            await screen.findByRole('link', { name: 'authorSearch.searchBooks' }),
        );

        const landing = await screen.findByTestId('landing');
        expect(new URL(landing.textContent ?? '', 'http://x').searchParams.get('q')).toBe(
            'Толстой',
        );
        expect(landing).toHaveTextContent('type=books');
        // The panel would otherwise keep answering the next search with authors.
        expect(searchBarState.clearSelectedSearch).toHaveBeenCalled();
    });

    it('offers no book search while there are authors', async () => {
        renderPage();

        await screen.findByText('Достоевский Федор');
        expect(screen.queryByText('authorSearch.searchBooks')).toBeNull();
    });
});

describe('AuthorSearch head line', () => {
    const answer = (total?: number) => ({
        authors: [{ id: 1, full_name: 'Толстой Лев', books_count: 700 }],
        length: 1,
        total,
    });

    it('says how many authors were found', async () => {
        listAuthors.mockResolvedValue(answer(38));
        renderPage();

        expect(await screen.findByText('authorSearch.total:38')).toBeInTheDocument();
    });

    // Today's production has no total; the line's place stays held for it.
    it('keeps the line empty without a total', async () => {
        listAuthors.mockResolvedValue(answer());
        renderPage();

        await screen.findByRole('link', { name: /Толстой Лев/ });
        expect(screen.getByTestId('author-head')).toHaveTextContent('');
    });

    const hint = () => [
        screen.queryByText('authorSearch.narrowDown'),
        screen.queryByText('authorSearch.narrowDownShort'),
    ];

    it('asks for more of the name on a huge result of one word', async () => {
        listAuthors.mockResolvedValue(answer(201));
        renderPage();

        await screen.findByText('authorSearch.total:201');
        hint().forEach((text) => expect(text).toBeInTheDocument());
    });

    // Spaces around the word add no word to the query.
    it('counts the spaces around one word as nothing', async () => {
        listAuthors.mockResolvedValue(answer(201));
        renderPage(`/search?q=${encodeURIComponent(' Толстой ')}&type=authors`);

        await screen.findByText('authorSearch.total:201');
        hint().forEach((text) => expect(text).toBeInTheDocument());
    });

    // «Алексей Иванов» already gave the name the hint would ask for.
    it('asks nothing more of two words, and still says how many', async () => {
        listAuthors.mockResolvedValue(answer(201));
        renderPage(`/search?q=${encodeURIComponent('Алексей Иванов')}&type=authors`);

        await screen.findByText('authorSearch.total:201');
        hint().forEach((text) => expect(text).toBeNull());
    });

    it('leaves a result of one word that can be paged through alone', async () => {
        listAuthors.mockResolvedValue(answer(200));
        renderPage();

        await screen.findByText('authorSearch.total:200');
        hint().forEach((text) => expect(text).toBeNull());
    });
});

describe('AuthorSearch loading', () => {
    const never = () => listAuthors.mockReturnValue(new Promise(() => {}));

    it('draws ten author cards, in the grid the authors land in', async () => {
        never();
        renderPage();

        const skeletons = screen.getAllByTestId('author-card-skeleton');
        expect(skeletons).toHaveLength(10);
        expect(skeletons[0].parentElement).toHaveAttribute('data-testid', 'author-grid');
    });

    it('holds the places of the head line and the pager', () => {
        never();
        renderPage();

        expect(screen.getByTestId('author-head')).toBeInTheDocument();
        expect(screen.getByTestId('author-pager-box')).toBeInTheDocument();
    });

    it('puts the authors in the same grid', async () => {
        renderPage();

        const row = await screen.findByRole('link', { name: /Достоевский Федор/ });
        expect(row.closest('[data-testid="author-grid"]')).not.toBeNull();
    });

    it('states no total of the search before it while loading the next one', async () => {
        listAuthors.mockResolvedValueOnce({
            authors: [{ id: 1, full_name: 'Толстой Лев' }],
            length: 1,
            total: 38,
        });
        const NextSearch: React.FC = () => {
            const navigate = useNavigate();
            return (
                <button type="button" onClick={() => navigate('/search?q=zzz&type=authors')}>
                    search-again
                </button>
            );
        };
        render(
            <MemoryRouter initialEntries={['/search?q=abc&type=authors']}>
                <Routes>
                    <Route path="/search" element={<AuthorSearch />} />
                </Routes>
                <NextSearch />
            </MemoryRouter>,
        );
        await screen.findByText('authorSearch.total:38');

        never();
        await userEvent.click(screen.getByRole('button', { name: 'search-again' }));

        expect(screen.queryByText('authorSearch.total:38')).toBeNull();
    });
});

// The pager sits under the grid, so a grid of the wrong height moves it the
// moment the authors land. What is left of a known total is the height to draw.
describe('AuthorSearch skeleton on a partial page', () => {
    const page = (id: number) => ({
        authors: [{ id, full_name: `Автор ${id}` }],
        length: 2,
    });
    const never = () => listAuthors.mockReturnValue(new Promise(() => {}));
    const skeletons = () => screen.queryAllByTestId('author-card-skeleton');

    const Go: React.FC<{ to: string }> = ({ to }) => {
        const navigate = useNavigate();
        return (
            <button type="button" onClick={() => navigate(to)}>
                go
            </button>
        );
    };

    const openAndGo = async (from: string, to: string, total?: number) => {
        listAuthors.mockResolvedValueOnce({ ...page(1), total });
        render(
            <MemoryRouter initialEntries={[from]}>
                <Routes>
                    <Route path="/search" element={<AuthorSearch />} />
                </Routes>
                <Go to={to} />
            </MemoryRouter>,
        );
        await screen.findByRole('link', { name: /Автор 1/ });
        never();
        await userEvent.click(screen.getByRole('button', { name: 'go' }));
    };

    it('draws only what is left on the last page', async () => {
        await openAndGo('/search?q=abc&type=authors', '/search?q=abc&type=authors&page=2', 11);

        expect(skeletons()).toHaveLength(1);
    });

    it('draws a full page while more than a page is left', async () => {
        await openAndGo('/search?q=abc&type=authors&page=2', '/search?q=abc&type=authors', 11);

        expect(skeletons()).toHaveLength(10);
    });

    it('draws a full page when exactly a page is left', async () => {
        await openAndGo('/search?q=abc&type=authors', '/search?q=abc&type=authors&page=2', 20);

        expect(skeletons()).toHaveLength(10);
    });

    it('does not size a new search by the total of the one before', async () => {
        await openAndGo('/search?q=abc&type=authors', '/search?q=zzz&type=authors', 3);

        expect(skeletons()).toHaveLength(10);
    });

    it('draws a full page without a total', async () => {
        await openAndGo('/search?q=abc&type=authors', '/search?q=abc&type=authors&page=2');

        expect(skeletons()).toHaveLength(10);
    });

    it('draws a full page past the end of the result', async () => {
        await openAndGo('/search?q=abc&type=authors', '/search?q=abc&type=authors&page=9', 11);

        expect(skeletons()).toHaveLength(10);
    });

    it('does not trust a total of another books language', async () => {
        listAuthors.mockResolvedValueOnce({ ...page(1), total: 11 });
        const { rerender } = render(
            <MemoryRouter initialEntries={['/search?q=abc&type=authors&page=2']}>
                <Routes>
                    <Route path="/search" element={<AuthorSearch />} />
                </Routes>
            </MemoryRouter>,
        );
        await screen.findByRole('link', { name: /Автор 1/ });

        never();
        authState.user = { books_lang: 'en' };
        rerender(
            <MemoryRouter initialEntries={['/search?q=abc&type=authors&page=2']}>
                <Routes>
                    <Route path="/search" element={<AuthorSearch />} />
                </Routes>
            </MemoryRouter>,
        );

        await waitFor(() => expect(skeletons()).toHaveLength(10));
    });
});

// A zero-hit search answers authors: null (a nil Go slice marshals to null).
// Skipping the state update on that answer would leave the previous search's
// authors — and its page count — on screen behind the new query.
describe('AuthorSearch zero-hit search', () => {
    const NextSearch: React.FC = () => {
        const navigate = useNavigate();
        return (
            <button type="button" onClick={() => navigate('/search?q=zzz&type=authors')}>
                search-again
            </button>
        );
    };

    it('clears the previous result and its page count instead of keeping them', async () => {
        listAuthors.mockResolvedValue({
            authors: [{ id: 1, full_name: 'Толстой Лев', books_count: 700 }],
            length: 3,
        });
        render(
            <MemoryRouter initialEntries={['/search?q=%D0%A2%D0%BE%D0%BB&type=authors']}>
                <Routes>
                    <Route path="/search" element={<AuthorSearch />} />
                </Routes>
                <NextSearch />
            </MemoryRouter>,
        );
        expect(await screen.findByRole('link', { name: /Толстой Лев/ })).toBeInTheDocument();
        expect(screen.getByTestId('pager')).toHaveTextContent('3');

        listAuthors.mockResolvedValue({ authors: null, length: 0 } as unknown as Awaited<
            ReturnType<typeof booksApi.listAuthors>
        >);
        await userEvent.click(screen.getByRole('button', { name: 'search-again' }));

        expect(await screen.findByText('noAuthorsFound:zzz')).toBeInTheDocument();
        await waitFor(() => expect(screen.queryByRole('link', { name: /Толстой Лев/ })).toBeNull());
        expect(screen.getByTestId('pager')).toHaveTextContent('0');
    });
});
