import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';

import AuthorSearch from '@/features/catalogue/AuthorSearch';
import * as booksApi from '@/api/books';

// The page is a stop on the way to a book list: what matters is that it asks
// for the authors under the reader's own books language, says how many books
// each one holds, and carries the right author through on a click.

// A stable t, so effects keyed on it do not loop. Counts are rendered through
// i18next plurals in the application; here the key and the count are enough to
// tell the number apart from the name.
const translate = (key: string, options?: Record<string, unknown> | string) => {
    if (options && typeof options === 'object' && 'count' in options) {
        return `${key}:${String(options.count)}`;
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

const searchBarState = { setSearchItem: vi.fn() };
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchBarState }));

const listAuthors = vi.mocked(booksApi.listAuthors);

const renderPage = (path = '/books/find/authors/%D0%A2%D0%BE%D0%BB%D1%81%D1%82%D0%BE%D0%B9/1') =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <Routes>
                <Route path="/books/find/authors/:author/:page" element={<AuthorSearch />} />
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

        expect(await screen.findByText('Толстой А')).toBeInTheDocument();
        expect(screen.queryByText(/^bookCount:/)).not.toBeInTheDocument();
    });

    it('carries the chosen author through to their books', async () => {
        renderPage();

        const row = await screen.findByRole('button', { name: /Достоевский Федор/ });
        await userEvent.click(row);

        expect(authorState.setAuthorName).toHaveBeenCalledWith('Достоевский Федор');
        // A stale query in the box would fight the filter on the next page.
        expect(searchBarState.setSearchItem).toHaveBeenCalledWith('');
    });

    it('reports an empty search rather than an empty list', async () => {
        listAuthors.mockResolvedValue({ authors: [], length: 0 });
        renderPage();

        expect(await screen.findByText('noAuthorsFound')).toBeInTheDocument();
    });

    it('survives the request failing', async () => {
        listAuthors.mockRejectedValue(new Error('nope'));
        vi.spyOn(console, 'error').mockImplementation(() => {});
        renderPage();

        expect(await screen.findByText('noAuthorsFound')).toBeInTheDocument();
    });
});
