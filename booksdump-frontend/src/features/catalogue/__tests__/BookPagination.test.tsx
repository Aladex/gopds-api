import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';

import BookPagination from '@/features/catalogue/BookPagination';

// The pager is how a reader reaches anything past the first ten books, so what
// matters is that its links are real addresses and that clicking one moves the
// router rather than reloading the application.

const translate = (key: string, options?: { page?: number }) =>
    options?.page !== undefined ? `${key}:${options.page}` : key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

let currentPath = '';
const PathProbe: React.FC = () => {
    const { pathname } = useLocation();
    // Recorded in an effect, not during render: writing to the outside world
    // while rendering is the side effect React asks components not to have.
    React.useEffect(() => {
        currentPath = pathname;
    }, [pathname]);
    return null;
};

function renderPager(props: Partial<React.ComponentProps<typeof BookPagination>> = {}) {
    currentPath = '';
    return render(
        <MemoryRouter initialEntries={['/books/page/5']}>
            <PathProbe />
            <BookPagination totalPages={100} currentPage={5} baseUrl="/books/page/5" {...props} />
        </MemoryRouter>,
    );
}

describe('BookPagination', () => {
    it('gives every page a real address', () => {
        renderPager();

        expect(screen.getByRole('link', { name: 'goToPage:7' })).toHaveAttribute(
            'href',
            '/books/page/7',
        );
    });

    it('marks where the reader is', () => {
        renderPager();

        const current = screen.getByRole('link', { name: 'goToPage:5' });
        expect(current).toHaveAttribute('aria-current', 'page');
    });

    it('moves the router instead of reloading the page', async () => {
        const user = userEvent.setup();
        renderPager();

        await user.click(screen.getByRole('link', { name: 'goToPage:7' }));

        expect(currentPath).toBe('/books/page/7');
    });

    it('builds sibling pages from a filtered route', async () => {
        const user = userEvent.setup();
        render(
            <MemoryRouter initialEntries={['/books/find/author/42/3']}>
                <PathProbe />
                <BookPagination totalPages={20} currentPage={3} baseUrl="/books/find/author/42/3" />
            </MemoryRouter>,
        );

        await user.click(screen.getByRole('link', { name: 'goToPage:4' }));

        // The author id has to survive; dropping it would silently widen the search.
        expect(currentPath).toBe('/books/find/author/42/4');
    });

    it('offers no way back from the first page', () => {
        renderPager({ currentPage: 1, baseUrl: '/books/page/1' });

        const previous = screen.getByLabelText('previousPage');
        expect(previous).toHaveAttribute('aria-disabled', 'true');
        expect(previous).not.toHaveAttribute('href');
    });

    it('offers no way on from the last page', () => {
        renderPager({ currentPage: 100, baseUrl: '/books/page/100' });

        expect(screen.getByLabelText('nextPage')).toHaveAttribute('aria-disabled', 'true');
    });

    it('steps one page at a time', async () => {
        const user = userEvent.setup();
        renderPager();

        await user.click(screen.getByLabelText('nextPage'));
        expect(currentPath).toBe('/books/page/6');
    });

    it('stays out of the way when there is only one page', () => {
        renderPager({ totalPages: 1, currentPage: 1 });

        expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
    });

    it('leaves a modified click to the browser', async () => {
        const user = userEvent.setup();
        renderPager();

        // Ctrl-click means "open elsewhere"; intercepting it would break that.
        await user.keyboard('{Control>}');
        await user.click(screen.getByRole('link', { name: 'goToPage:7' }));
        await user.keyboard('{/Control}');

        expect(currentPath).toBe('/books/page/5');
    });
});
