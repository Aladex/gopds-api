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

/**
 * stubGeometry lends jsdom the widths it cannot compute.
 *
 * The pager fits its window to the row it measures, and jsdom reports every
 * box as zero, so without this the tests can only reach the fallback. The
 * numbers passed in are the ones Chrome reported on the stand.
 */
function stubGeometry(sizes: { row: number; cell: number; arrow: number; ellipsis: number }) {
    const rect = Element.prototype.getBoundingClientRect;
    const styles = window.getComputedStyle;

    const widthOf = (el: Element): number => {
        if (el.tagName === 'NAV') return sizes.row;
        if (el.getAttribute('data-slot') === 'pagination-ellipsis') return sizes.ellipsis;
        if (el.tagName === 'A') {
            const label = el.getAttribute('aria-label') ?? '';
            return label.startsWith('goToPage') ? sizes.cell : sizes.arrow;
        }
        return 0;
    };

    Element.prototype.getBoundingClientRect = function (this: Element) {
        return { ...new DOMRect(), width: widthOf(this) } as DOMRect;
    };
    window.getComputedStyle = ((el: Element, pseudo?: string | null) => {
        const computed = styles.call(window, el, pseudo ?? undefined);
        return new Proxy(computed, {
            get: (target, key) => (key === 'columnGap' ? '2px' : Reflect.get(target, key, target)),
        });
    }) as typeof window.getComputedStyle;

    return () => {
        Element.prototype.getBoundingClientRect = rect;
        window.getComputedStyle = styles;
    };
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

    it('spreads the row to the card edges on a phone', () => {
        // Dropping the neighbours left a 268px row inside a 328px column, and
        // the leftover slack read as the pager having shrunk. Full width puts
        // the arrows level with the book cards above instead of floating.
        viewport.narrow = true;
        renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

        expect(screen.getByRole('list')).toHaveClass('w-full', 'justify-between');
    });

    it('keeps the row content-width on a desktop', () => {
        // A 1200px row with the arrows at its far ends would put them nowhere
        // near the numbers; the wide pager stays centred and compact.
        renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

        expect(screen.getByRole('list')).not.toHaveClass('w-full');
    });

    it('keeps the numbers together between the arrows', () => {
        // The numbers share one flex cell, so justify-between spaces the three
        // children — arrow, numbers, arrow — instead of prising the digits
        // apart across the whole width.
        viewport.narrow = true;
        renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

        const first = screen.getByRole('link', { name: 'goToPage:1' });
        const last = screen.getByRole('link', { name: 'goToPage:47377' });
        expect(first.parentElement).toBe(last.parentElement);
        expect(screen.getByRole('list').children).toHaveLength(3);
    });

    it('opens the window to what a measured row can take', () => {
        // jsdom has no layout, so the geometry is fed in from what Chrome
        // measured on the stand: a 328px column, 40px cells at the four-digit
        // tier, 20px ellipses. The fallback would show no neighbours here, so
        // seeing them proves the measurement is what decided.
        viewport.narrow = true;
        const restore = stubGeometry({ row: 328, cell: 40, arrow: 28, ellipsis: 20 });
        try {
            renderPager({ totalPages: 4737, currentPage: 2200, baseUrl: '/books/page/2200' });

            expect(screen.getByRole('link', { name: 'goToPage:2199' })).toBeInTheDocument();
            expect(screen.getByRole('link', { name: 'goToPage:2201' })).toBeInTheDocument();
        } finally {
            restore();
        }
    });

    it('keeps it shut when the same row holds wider numbers', () => {
        // Five-digit cells are 48px and their ellipses stay 28px wide, which
        // is 36px more than the row has.
        viewport.narrow = true;
        const restore = stubGeometry({ row: 328, cell: 48, arrow: 28, ellipsis: 28 });
        try {
            renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

            expect(screen.queryByRole('link', { name: 'goToPage:21999' })).not.toBeInTheDocument();
        } finally {
            restore();
        }
    });

    it('shuts it again when the column narrows to 320px', () => {
        viewport.narrow = true;
        const restore = stubGeometry({ row: 288, cell: 40, arrow: 28, ellipsis: 20 });
        try {
            renderPager({ totalPages: 4737, currentPage: 2200, baseUrl: '/books/page/2200' });

            expect(screen.queryByRole('link', { name: 'goToPage:2199' })).not.toBeInTheDocument();
        } finally {
            restore();
        }
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
