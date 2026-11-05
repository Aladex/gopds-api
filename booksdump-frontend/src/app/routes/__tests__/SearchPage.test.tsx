import React from 'react';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';

import SearchPage from '@/app/routes/SearchPage';

// Which of the two lists answers is the page's one decision; what each list
// asks the server is pinned in its own tests.
vi.mock('@/features/catalogue/BooksList', () => ({ default: () => <div>books list</div> }));
vi.mock('@/features/catalogue/AuthorSearch', () => ({ default: () => <div>author list</div> }));
vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: (key: string) => key }),
}));

const Probe: React.FC = () => {
    const { pathname, search } = useLocation();
    return <div data-testid="where">{pathname + search}</div>;
};

const renderAt = (path: string) =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <Routes>
                <Route path="/search" element={<SearchPage />} />
                <Route path="*" element={<Probe />} />
            </Routes>
        </MemoryRouter>,
    );

describe('SearchPage', () => {
    it('answers a search for books with the books', () => {
        renderAt('/search?q=x&type=books');

        expect(screen.getByText('books list')).toBeInTheDocument();
        expect(document.title).toBe('routeByBookName.books');
    });

    it('takes a search of no type for a search for books', () => {
        renderAt('/search?q=x');

        expect(screen.getByText('books list')).toBeInTheDocument();
    });

    it('answers a search for authors with the authors', () => {
        renderAt('/search?q=x&type=authors');

        expect(screen.getByText('author list')).toBeInTheDocument();
        expect(document.title).toBe('routeByAuthorName.books');
    });

    it.each(['/search', '/search?q=', '/search?q=%20%20', '/search?type=authors'])(
        'sends %s, which asks nothing, to the catalogue',
        (path) => {
            renderAt(path);

            expect(screen.getByTestId('where')).toHaveTextContent('/books/page/1');
        },
    );
});
