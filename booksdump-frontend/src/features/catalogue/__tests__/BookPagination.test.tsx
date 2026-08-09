import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router';

import BookPagination from '@/features/catalogue/BookPagination';

// The pager is how a reader reaches anything past the first ten books, so what
// matters is that its links are real addresses and that clicking one moves the
// router rather than reloading the application.

const translate = (key: string, options?: { page?: number }) =>
    options?.page !== undefined ? `${key}:${options.page}` : key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

// The pager's shape depends on the width, so the tests drive it rather than
// leaving it to whatever jsdom reports.
const viewport = { narrow: false };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => viewport.narrow,
    default: () => viewport.narrow,
}));

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
    viewport.narrow = props.totalPages === undefined ? viewport.narrow : viewport.narrow;
    return render(
        <MemoryRouter initialEntries={['/books/page/5']}>
            <PathProbe />
            <BookPagination totalPages={100} currentPage={5} baseUrl="/books/page/5" {...props} />
        </MemoryRouter>,
    );
}

beforeEach(() => {
    viewport.narrow = false;
});

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

    it('keeps the search of a scoped list on every page address', () => {
        render(
            <MemoryRouter initialEntries={['/books/find/author/42/3?title=%D1%85&book_id=5']}>
                <PathProbe />
                <BookPagination
                    totalPages={20}
                    currentPage={3}
                    baseUrl="/books/find/author/42/3?title=%D1%85&book_id=5"
                />
            </MemoryRouter>,
        );

        // A scoped search puts its query in the URL; page two without it is a
        // different, wider list than the one the reader was paging through.
        expect(screen.getByRole('link', { name: 'goToPage:4' })).toHaveAttribute(
            'href',
            '/books/find/author/42/4?title=%D1%85&book_id=5',
        );
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

    it('gives every page cell the width of the longest page number', () => {
        // Five-digit totals made short numbers airy and long ones cramped: the
        // air came from the min-width slack, not from a shared grid. All cells
        // of one pager take the tier of the longest number instead.
        renderPager({ totalPages: 47377, currentPage: 23000, baseUrl: '/books/page/23000' });

        expect(screen.getByRole('link', { name: 'goToPage:23000' })).toHaveClass('min-w-14');
        expect(screen.getByRole('link', { name: 'goToPage:1' })).toHaveClass('min-w-14');
    });

    it('sizes the cell tier by the last page', () => {
        renderPager({ totalPages: 995, currentPage: 500, baseUrl: '/books/page/500' });
        expect(screen.getByRole('link', { name: 'goToPage:500' })).toHaveClass('min-w-10');

        renderPager({ totalPages: 8, currentPage: 4, baseUrl: '/books/page/4' });
        expect(screen.getByRole('link', { name: 'goToPage:4' })).toHaveClass('min-w-9');
    });

    it('lets a long page number grow past the icon square', () => {
        // size="icon" carries size-10/sm:size-8, which beats w-auto in the
        // stylesheet and froze every cell at 36px — five digits overflowed the
        // button and the active tile. Numbered cells must not carry it.
        renderPager({ totalPages: 47377, currentPage: 23000, baseUrl: '/books/page/23000' });

        const cell = screen.getByRole('link', { name: 'goToPage:23000' });
        expect(cell.className).not.toMatch(/size-(10|8)/);
        expect(cell).toHaveClass('w-auto');
    });

    it('shows fewer neighbours when page numbers run to four digits', () => {
        // Thirteen wide cells overflow a desktop row, so the window tightens
        // once numbers get long — just like it already does on narrow screens.
        renderPager({ totalPages: 47377, currentPage: 23000, baseUrl: '/books/page/23000' });

        expect(screen.queryByRole('link', { name: 'goToPage:3' })).not.toBeInTheDocument();
        expect(screen.getByRole('link', { name: 'goToPage:22999' })).toBeInTheDocument();
        expect(screen.queryByRole('link', { name: 'goToPage:22997' })).not.toBeInTheDocument();
    });

    it('keeps the wide window for short page numbers', () => {
        renderPager({ totalPages: 100, currentPage: 50, baseUrl: '/books/page/50' });

        expect(screen.getByRole('link', { name: 'goToPage:3' })).toBeInTheDocument();
        expect(screen.getByRole('link', { name: 'goToPage:47' })).toBeInTheDocument();
    });

    it('drops the neighbours on a phone once numbers reach four digits', () => {
        // Measured at 360px inside the 328px content column: with neighbours
        // the row was 368px at five digits and 329px at four — the arrows hung
        // off the screen in one case and sat flush against the edge in the
        // other, and `main` clips rather than scrolls. Without them the row is
        // 268px and 216px, and centring turns the slack back into margins.
        viewport.narrow = true;
        renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

        expect(screen.getByRole('link', { name: 'goToPage:1' })).toBeInTheDocument();
        expect(screen.getByRole('link', { name: 'goToPage:22000' })).toBeInTheDocument();
        expect(screen.getByRole('link', { name: 'goToPage:47377' })).toBeInTheDocument();
        expect(screen.queryByRole('link', { name: 'goToPage:21999' })).not.toBeInTheDocument();
        expect(screen.queryByRole('link', { name: 'goToPage:22001' })).not.toBeInTheDocument();
    });

    it('drops them at four digits too, where the row measured 329px', () => {
        viewport.narrow = true;
        renderPager({ totalPages: 4737, currentPage: 2200, baseUrl: '/books/page/2200' });

        expect(screen.queryByRole('link', { name: 'goToPage:2199' })).not.toBeInTheDocument();
        expect(screen.getByRole('link', { name: 'goToPage:2200' })).toBeInTheDocument();
    });

    it('keeps the neighbours on a phone while the numbers still fit', () => {
        // Three digits are 36px cells and the full narrow window measures
        // 280px. Shrinking it here would cost navigation for nothing.
        viewport.narrow = true;
        renderPager({ totalPages: 473, currentPage: 220, baseUrl: '/books/page/220' });

        expect(screen.getByRole('link', { name: 'goToPage:219' })).toBeInTheDocument();
        expect(screen.getByRole('link', { name: 'goToPage:221' })).toBeInTheDocument();
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
