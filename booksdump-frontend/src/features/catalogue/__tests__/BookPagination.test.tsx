import React from 'react';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router';

import BookPagination from '@/features/catalogue/BookPagination';
import { listColumn } from '@/shared/layout/breakpoints';

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
            {/* The pager fills its parent and expects that parent to be the
                column the list occupies, so the tests give it one rather than
                exercising it in a shape no page renders. */}
            <div {...listColumn}>
                <BookPagination
                    totalPages={100}
                    currentPage={5}
                    baseUrl="/books/page/5"
                    {...props}
                />
            </div>
        </MemoryRouter>,
    );
}

beforeEach(() => {
    viewport.narrow = false;
});

interface Geometry {
    row: number;
    /** What the digits alone draw, before any minimum is applied. */
    cell: number;
    arrow: number;
    ellipsis: number;
    gap?: number;
    /**
     * Per-digit advance and horizontal padding. Given, a cell is as wide as its
     * own number rather than as the widest one — which is the only way to see
     * whether the shared width follows the longest number or the current page.
     */
    digit?: number;
    /**
     * One advance per digit, for a font with no tabular set. `digit` is a
     * single scalar and so can only describe tabular figures; a stub that
     * cannot express proportional ones cannot express the failure they cause,
     * which is that a number a wider window admits can outmeasure the last
     * page. Takes precedence over `digit` for the characters it names.
     */
    advances?: Record<string, number>;
    pad?: number;
    /** What a border adds to a shrink-to-fit width, on each side together. */
    border?: number;
}

/** Whether a cell carries a border width, as opposed to only a border colour. */
function hasBorder(el: Element): boolean {
    return /(?:^|\s)border(?:-\d+)?(?:\s|$)/.test(el.className);
}

/** Tailwind writes min-w-N as N/4 rem, and the test root keeps the 16px default. */
function tierMinWidth(el: Element): number {
    const tier = /(?:^|\s)min-w-(\d+)(?:\s|$)/.exec(el.className);
    return tier ? Number(tier[1]) * 4 : 0;
}

/**
 * effectiveMinWidth is the minimum a browser would actually apply to a cell.
 *
 * An inline declaration outranks a utility class by cascade priority — not by
 * being the larger number — so an inline `min-width: 0` wins over `min-w-14`.
 * The measured width is applied inline for exactly that reason, and a stub
 * blind to it would report a cell's digits where the browser reports whichever
 * minimum is in force — which is how a fallback tier once measured itself.
 */
function effectiveMinWidth(el: HTMLElement): number {
    const inline = el.style.minWidth;
    return inline === '' ? tierMinWidth(el) : parseFloat(inline) || 0;
}

/**
 * stubGeometry lends jsdom the widths it cannot compute.
 *
 * The pager fits its window to the row it measures, and jsdom reports every
 * box as zero, so without this the tests can only reach the fallback. The
 * numbers passed in are the ones Chrome reported on the stand.
 *
 * A numbered cell reports the wider of its digits and its effective minimum; a
 * probe reports its digits alone, having no minimum to be held up by; and any
 * row reports the sum of its in-flow children. Those are the three things a
 * real layout does that decide whether this pager is right.
 */
function stubGeometry(sizes: Geometry) {
    const rect = Element.prototype.getBoundingClientRect;
    const styles = window.getComputedStyle;
    const gap = sizes.gap ?? 2;

    const widthOf = (el: Element): number => {
        if (el.tagName === 'NAV') return sizes.row;
        const slot = el.getAttribute('data-slot') ?? '';
        if (slot.startsWith('pagination-ellipsis')) return sizes.ellipsis;
        if (el.tagName === 'A') {
            const label = el.getAttribute('aria-label') ?? '';
            // A probe is measured exactly like the cell it stands in for: same
            // typography, same padding, same border, and no minimum of its own
            // — which is what lets it shrink when the type does.
            const probe = slot === 'pagination-probe';
            if (!probe && !label.startsWith('goToPage')) return sizes.arrow;

            const glyphs = [...(el.textContent ?? '').trim()];
            const advance = (glyph: string) => sizes.advances?.[glyph] ?? sizes.digit ?? Number.NaN;
            const perGlyph = glyphs.reduce((sum, glyph) => sum + advance(glyph), 0);
            const text = Number.isNaN(perGlyph) ? sizes.cell : perGlyph + (sizes.pad ?? 0);
            const natural = text + (hasBorder(el) ? (sizes.border ?? 0) : 0);
            // No special case for a probe: it is an element like any other, and
            // a minimum applied to it would hold it up like any other. Whether
            // it has one is the production code's business, and giving it one
            // has to be a mutation this stub can still see.
            return Math.max(natural, effectiveMinWidth(el as HTMLElement));
        }
        if (el.tagName === 'LI') {
            // Out-of-flow children take no part in their parent's width, which
            // is what keeps the measuring rig from sizing the row it sits in:
            // the probes can be as wide as the widest page number ever could be
            // without pushing a single page number along.
            const children = [...el.children].filter(
                (child) => !/(?:^|\s)absolute(?:\s|$)/.test(child.className),
            );
            const content = children.reduce((sum, child) => sum + widthOf(child), 0);
            return children.length === 0 ? 0 : content + gap * (children.length - 1);
        }
        return 0;
    };

    Element.prototype.getBoundingClientRect = function (this: Element) {
        return { ...new DOMRect(), width: widthOf(this) } as DOMRect;
    };
    window.getComputedStyle = ((el: Element, pseudo?: string | null) => {
        const computed = styles.call(window, el, pseudo ?? undefined);
        return new Proxy(computed, {
            get: (target, key) =>
                key === 'columnGap' ? `${gap}px` : Reflect.get(target, key, target),
        });
    }) as typeof window.getComputedStyle;

    return () => {
        Element.prototype.getBoundingClientRect = rect;
        window.getComputedStyle = styles;
    };
}

/**
 * stubResizeObserver replaces the setup file's no-op observer with one a test
 * can drive, so a content-size change — a text zoom, a late webfont — can be
 * played out rather than assumed.
 */
function stubResizeObserver() {
    const original = globalThis.ResizeObserver;
    interface Watch {
        owner: object;
        target: Element;
        seen: number;
        /**
         * A real observer delivers once for every box as soon as it starts
         * watching it, whatever its size, and does so asynchronously — so a
         * cold mount reads its metrics twice: the layout effect's own call and
         * then this. Modelling it is the difference between "the pager settles"
         * and "the pager is read once", and only the first is true.
         */
        owed: boolean;
        notify: () => void;
    }
    const watches: Watch[] = [];
    const widthOf = (target: Element) => target.getBoundingClientRect().width;
    /** How many times a callback has actually been handed a batch. */
    let deliveries = 0;

    class Recording {
        constructor(private readonly callback: ResizeObserverCallback) {}
        observe(target: Element) {
            watches.push({
                owner: this,
                target,
                seen: widthOf(target),
                owed: true,
                notify: () => {
                    deliveries += 1;
                    this.callback([], this as unknown as ResizeObserver);
                },
            });
        }
        unobserve(target: Element) {
            for (let i = watches.length - 1; i >= 0; i -= 1) {
                if (watches[i].owner === this && watches[i].target === target) watches.splice(i, 1);
            }
        }
        disconnect() {
            for (let i = watches.length - 1; i >= 0; i -= 1) {
                if (watches[i].owner === this) watches.splice(i, 1);
            }
        }
    }

    globalThis.ResizeObserver = Recording as unknown as typeof ResizeObserver;

    /**
     * deliver does what a browser's observer does: it pays the observation it
     * owes each box when it starts watching, and after that notifies only for
     * the boxes whose size actually changed. An element that never changes
     * width never wakes its callback again, which is the whole of the defect
     * being tested — on a desktop the nav is a fixed column and only the
     * numbers grow.
     *
     * One callback per observer per round, not one per box. A browser hands
     * its callback the whole batch of entries at once, and a double that calls
     * it once per due box would run the measurement twelve times on a cold
     * mount while claiming the pager reads twice.
     */
    const deliver = () => {
        const due = watches.filter((watch) => watch.owed || widthOf(watch.target) !== watch.seen);
        due.forEach((watch) => {
            watch.seen = widthOf(watch.target);
            watch.owed = false;
        });
        const batches = [...new Set(due.map((watch) => watch.owner))];
        if (batches.length > 0) {
            act(() => {
                batches.forEach((owner) => {
                    due.find((watch) => watch.owner === owner)?.notify();
                });
            });
        }
        return due.length;
    };

    return {
        watched: () => watches.map((watch) => watch.target),
        /** How many times a callback has been handed a batch since the start. */
        deliveries: () => deliveries,
        /**
         * One delivery round; returns how many boxes were due — those that
         * resized, plus any still owed the observation every box gets when it
         * is first watched. Not the same as how many times a callback ran,
         * which is once per observer.
         */
        fire: deliver,
        /**
         * settle plays the feedback path out: deliver, and deliver again for
         * as long as anything the pager did resized something it watches. It
         * returns how many rounds that took, and throws rather than spinning if
         * the pager and the observer start handing work back to each other.
         *
         * A cold mount costs two rounds even when nothing is wrong: one for the
         * observation every box is owed the moment it is watched, and one to
         * find that it changed nothing. That is what a browser does too, and it
         * is why the claim below is that the pager settles rather than that it
         * measures once.
         */
        settle: (limit = 8) => {
            for (let rounds = 1; rounds <= limit; rounds += 1) {
                if (deliver() === 0) return rounds;
            }
            throw new Error(`the pager never settled in ${limit} rounds`);
        },
        restore: () => {
            globalThis.ResizeObserver = original;
        },
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

    it('spreads the row to the card edges on a desktop too', () => {
        // The row used to stay content-width here, which left the arrows
        // floating ~200px inside the book cards above with nothing but air on
        // either side. The pager now owns the same column the cards do, and
        // the arrows sit on its edges at every width — as they already did on
        // a phone.
        renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

        expect(screen.getByRole('list')).toHaveClass('w-full', 'justify-between');
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

    it('sizes the cell from what the digits measure, not from a tier', () => {
        // The ladder handed five digits a 56px tier where Chrome measured the
        // text and its padding at 54. A pager's worth of that slack is a page
        // number's worth of column thrown away, so the shared width is
        // whatever the widest number actually draws. The geometry here is
        // stubbed a shade under a whole pixel as well, because a fractional
        // min-width leaves the widest number short of its own text.
        const restore = stubGeometry({ row: 1200, cell: 52.2, arrow: 36, ellipsis: 36 });
        try {
            renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

            const cell = screen.getByRole('link', { name: 'goToPage:22000' });
            const first = screen.getByRole('link', { name: 'goToPage:1' });
            // Read off the style rather than matched, so a failure names the
            // width that was applied: 56 is the tier having measured itself,
            // 52.2 is a fractional minimum the widest number would wrap under.
            expect(cell.style.minWidth).toBe('53px');
            // One shared width, active page included: the rhythm has to stay
            // even whichever page the reader is on.
            expect(first.style.minWidth).toBe('53px');
            // Markup hygiene rather than a behaviour guard: the inline minimum
            // already outranks the tier by cascade priority, so leaving the
            // class on would change nothing a reader could see. It goes anyway
            // — a class that can never apply is a claim about the layout that
            // is not true.
            expect(cell).not.toHaveClass('min-w-14');
            // Also markup rather than behaviour, and said so: jsdom runs no
            // transitions, so nothing here can observe what transition-all
            // does to a pager that measures itself whenever its numbers
            // resize. The browser can, and did — see the report.
            expect(cell).not.toHaveClass('transition-all');
            expect(cell).toHaveClass('transition-colors');
        } finally {
            restore();
        }
    });

    it('fills a desktop column instead of stopping at three neighbours', () => {
        // Measured on the stand at 1440px: 1200px of column, 36px arrows, 54px
        // cells, 4px gaps. Nineteen elements measure 1150px of that; the old
        // cap of three siblings stopped at thirteen and left the rest as air.
        const restore = stubGeometry({ row: 1200, cell: 54, arrow: 36, ellipsis: 36 });
        try {
            renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

            expect(screen.getByRole('link', { name: 'goToPage:21994' })).toBeInTheDocument();
            expect(screen.getByRole('link', { name: 'goToPage:22006' })).toBeInTheDocument();
            expect(screen.queryByRole('link', { name: 'goToPage:21993' })).not.toBeInTheDocument();
        } finally {
            restore();
        }
    });

    it('re-fits when the text grows without the column moving', () => {
        // The column is a fixed 1200px box on a desktop, so a reader who turns
        // the text size up — or a webfont that swaps in late — grows the cells
        // without the nav ever changing width. Watching only the nav would
        // leave a nineteen-item window drawn at a size it no longer fits, and
        // the browser measurements leave only 50px of that column spare.
        const observer = stubResizeObserver();
        let geometry = stubGeometry({ row: 1200, cell: 54, arrow: 36, ellipsis: 36, gap: 4 });
        try {
            renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });
            // Drained first, so what follows is the growth being observed and
            // not a delivery still owed from mount.
            observer.settle();
            expect(screen.getAllByRole('link', { name: /goToPage/ })).toHaveLength(17);

            // Same 1200px row, bigger digits.
            geometry();
            geometry = stubGeometry({ row: 1200, cell: 80, arrow: 36, ellipsis: 36, gap: 4 });
            observer.fire();

            expect(screen.getAllByRole('link', { name: /goToPage/ })).toHaveLength(11);
            expect(screen.getByRole('link', { name: 'goToPage:22000' })).toHaveStyle({
                minWidth: '80px',
            });
        } finally {
            geometry();
            observer.restore();
        }
    });

    it('watches the column and the type, and not the row it draws', () => {
        // The nav is the column the row has to fit inside; the probes are the
        // type. The group of numbers is neither — its width is a consequence of
        // the answer, not an input to it — and watching it was what let a
        // measurement taken off the rendered window wake the window that
        // produced it. A test that only checked the refit would pass with the
        // group watched instead, which is the arrangement that oscillated.
        const observer = stubResizeObserver();
        const restore = stubGeometry({ row: 1200, cell: 54, arrow: 36, ellipsis: 36, gap: 4 });
        try {
            renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

            const watched = observer.watched();
            const nav = screen.getByRole('navigation');
            const group = screen.getByRole('link', { name: 'goToPage:22000' }).parentElement;
            const probes = [...document.querySelectorAll('[data-slot="pagination-probe"]')];

            expect(watched).toContain(nav);
            expect(probes).toHaveLength(10);
            for (const probe of probes) {
                expect(watched).toContain(probe);
            }
            expect(watched).not.toContain(group);
        } finally {
            restore();
            observer.restore();
        }
    });

    it('is read twice on a cold mount, and the second reading changes nothing', () => {
        // The count the comments have been claiming, now counted. The layout
        // effect reads once itself; the observer owes an observation for every
        // box it starts watching and pays them in one batch, as a browser does,
        // so the callback runs once and the measurement with it. Twelve boxes
        // are watched here — the nav, ten digit probes and the ellipsis — and
        // a double that notified per box rather than per batch would make that
        // twelve readings while the comment said two.
        const observer = stubResizeObserver();
        const restore = stubGeometry({ row: 1200, cell: 54, arrow: 36, ellipsis: 36, gap: 4 });
        try {
            renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

            expect(observer.watched()).toHaveLength(12);
            expect(observer.settle()).toBe(2);
            expect(observer.deliveries()).toBe(1);
        } finally {
            restore();
            observer.restore();
        }
    });

    it('settles rather than chasing what its own answer moved', () => {
        // Nothing the pager draws is watched: the nav is the column it has to
        // fit inside and the probes are the type, and neither of them is
        // resized by choosing a window. So opening the fallback window to the
        // fitted one moves nothing that could report it back, and the pager
        // arrives at its answer and stays there. settle() throws rather than
        // spinning if that is wrong.
        const observer = stubResizeObserver();
        let geometry = stubGeometry({ row: 1200, cell: 54, arrow: 36, ellipsis: 36, gap: 4 });
        try {
            renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });

            // Two rounds from a cold mount, and both are ordinary. A browser's
            // observer owes every box it starts watching one observation, so
            // the metrics are read twice — once by the layout effect and once
            // by that delivery — and the second reading returns what the first
            // did. The round after it has nothing to report at all.
            expect(observer.settle()).toBe(2);

            geometry();
            geometry = stubGeometry({ row: 1200, cell: 80, arrow: 36, ellipsis: 36, gap: 4 });

            // Two again: the probes grow and the window closes to eleven in
            // answer, and the second round finds nothing left to report.
            // Bounded, not merely "it stopped this time" — settle() throws
            // past eight.
            expect(observer.settle()).toBe(2);
            expect(screen.getAllByRole('link', { name: /goToPage/ })).toHaveLength(11);
        } finally {
            geometry();
            observer.restore();
        }
    });

    it('re-fits when the text shrinks back again', () => {
        // The other direction, and the harder one: every numbered cell is held
        // at the shared minimum, so when the type gets smaller none of them
        // moves and nothing would wake an observer watching the numbers. The
        // row would keep the larger grid — measured in Chrome, 71px cells for
        // 13px digits that draw 54 — until the reader happened to change page.
        const observer = stubResizeObserver();
        let geometry = stubGeometry({ row: 1200, cell: 80, arrow: 36, ellipsis: 36, gap: 4 });
        try {
            renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });
            // Drained first, or the delivery still owed from mount would do the
            // re-measuring and the shrink would never be what was observed.
            observer.settle();
            expect(screen.getAllByRole('link', { name: /goToPage/ })).toHaveLength(11);

            geometry();
            geometry = stubGeometry({ row: 1200, cell: 54, arrow: 36, ellipsis: 36, gap: 4 });
            observer.settle();

            expect(screen.getByRole('link', { name: 'goToPage:22000' })).toHaveStyle({
                minWidth: '54px',
            });
            expect(screen.getAllByRole('link', { name: /goToPage/ })).toHaveLength(17);
        } finally {
            geometry();
            observer.restore();
        }
    });

    /*
     * A font with no tabular set: `1` is narrow, `8` is the widest digit, the
     * rest sit between. Every advance below is a plausible proportional metric
     * rather than a Chrome reading — the point is not the exact numbers but
     * that they are not all equal, which is the one thing the catalogue's
     * documented fallback to proportional figures guarantees.
     */
    const PROPORTIONAL = {
        '0': 7.3,
        '1': 4.0,
        '2': 7.3,
        '3': 7.3,
        '4': 7.3,
        '5': 7.3,
        '6': 7.3,
        '7': 7.3,
        '8': 8.5,
        '9': 7.3,
    };

    it('does not let the window it chose decide how wide a cell is', () => {
        // The shared width has to be a property of the type and the catalogue,
        // not of which pages happen to be on screen — otherwise measuring is a
        // feedback loop. Under proportional figures the widest five-digit
        // string is not the last page, 44500, but five of the widest digit:
        // 5 x 8.5 + 16 of padding + 1.3333 of border is 59.83, so 60px.
        const restore = stubGeometry({
            row: 1064,
            cell: 54,
            advances: PROPORTIONAL,
            pad: 16,
            border: 1.3333,
            arrow: 36,
            ellipsis: 36,
            gap: 4,
        });
        try {
            const early = renderPager({
                totalPages: 44500,
                currentPage: 1,
                baseUrl: '/books/page/1',
            });
            const onPageOne = screen.getByRole('link', { name: 'goToPage:44500' }).style.minWidth;
            early.unmount();

            renderPager({ totalPages: 44500, currentPage: 40903, baseUrl: '/books/page/40903' });
            const deepIn = screen.getByRole('link', { name: 'goToPage:44500' }).style.minWidth;

            expect(onPageOne).toBe('60px');
            expect(deepIn).toBe('60px');
        } finally {
            restore();
        }
    });

    it('settles on a font with no tabular figures', () => {
        // The scenario the review found, reproduced against the production
        // range and fitting functions: 44 500 pages, page 40 903, a 1064px row.
        // Measuring the rendered cells, four siblings read a 56px cell and fit
        // five; the five-sibling window admits page 40 898 — two of the widest
        // digit where 40 899 has one — which reads 57px and fits only four;
        // dropping it returns the reading to 56px. Production has no round cap,
        // so that is a pager that flickers between nine and eleven numbers for
        // as long as the reader looks at it.
        const observer = stubResizeObserver();
        const restore = stubGeometry({
            row: 1064,
            cell: 54,
            advances: PROPORTIONAL,
            pad: 16,
            border: 1.3333,
            arrow: 36,
            ellipsis: 36,
            gap: 4,
        });
        try {
            renderPager({ totalPages: 44500, currentPage: 40903, baseUrl: '/books/page/40903' });

            // Throws rather than spinning if the two keep waking each other.
            observer.settle();

            const cell = screen.getByRole('link', { name: 'goToPage:44500' }).style.minWidth;
            expect(cell).toBe('60px');
            // 128 x 4 + 484 = 996 of the 1064; five siblings would need 1124.
            expect(screen.getAllByRole('link', { name: /goToPage/ })).toHaveLength(13);

            // And it stays there: a second run changes nothing.
            expect(observer.settle()).toBe(1);
            expect(screen.getByRole('link', { name: 'goToPage:44500' }).style.minWidth).toBe(cell);
        } finally {
            restore();
            observer.restore();
        }
    });

    it('keeps a short catalogue at its content width', () => {
        // Nothing is hidden here, so there is no window to open and spreading
        // the row would put the two arrows half a column away from the numbers
        // they step through. Nineteen pages is where that holds at this
        // geometry — 19 cells of 54 and their gaps come to 1112px of the 1200.
        const restore = stubGeometry({ row: 1200, cell: 54, arrow: 36, ellipsis: 36, gap: 4 });
        try {
            renderPager({ totalPages: 19, currentPage: 10, baseUrl: '/books/page/10' });

            expect(screen.getAllByRole('link', { name: /goToPage/ })).toHaveLength(19);
            expect(document.querySelector('[data-slot="pagination-ellipsis"]')).toBeNull();
            const row = screen.getByRole('list');
            expect(row).not.toHaveClass('w-full');
            expect(row).not.toHaveClass('justify-between');
        } finally {
            restore();
        }
    });

    it('spreads to the column edges as soon as the window hides a page', () => {
        // One page more than the row can hold, and the window starts standing
        // pages in — which is when the arrows have somewhere to be.
        const restore = stubGeometry({ row: 1200, cell: 54, arrow: 36, ellipsis: 36, gap: 4 });
        try {
            renderPager({ totalPages: 20, currentPage: 10, baseUrl: '/books/page/10' });

            expect(document.querySelector('[data-slot="pagination-ellipsis"]')).not.toBeNull();
            expect(screen.getByRole('list')).toHaveClass('w-full', 'justify-between');
        } finally {
            restore();
        }
    });

    it('says so in development when it is rendered outside a list column', () => {
        // The pager fills its parent, so a caller that puts it beside the list
        // column instead of inside it gets a row fitted to the wrong box and
        // nothing says a word. That is how the author search shipped wrong.
        const complaints = vi.spyOn(console, 'error').mockImplementation(() => {});
        try {
            render(
                <MemoryRouter initialEntries={['/books/page/5']}>
                    <BookPagination totalPages={100} currentPage={5} baseUrl="/books/page/5" />
                </MemoryRouter>,
            );

            expect(complaints).toHaveBeenCalledWith(
                expect.stringContaining('data-list-column'),
                expect.anything(),
            );
        } finally {
            complaints.mockRestore();
        }
    });

    it('says so when the nav only appears after the request comes back', () => {
        // The ordinary loading shape of a list page: it renders with no page
        // count, and raises it when its request answers. There is no nav on the
        // first commit — the component returns null — so a check that ran once
        // at mount would look, find nothing, and never look again. Which is to
        // say it would never fire for most of the pages that call this.
        const complaints = vi.spyOn(console, 'error').mockImplementation(() => {});
        try {
            const view = render(
                <MemoryRouter initialEntries={['/books/page/1']}>
                    <BookPagination totalPages={0} currentPage={1} baseUrl="/books/page/1" />
                </MemoryRouter>,
            );
            expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
            expect(complaints).not.toHaveBeenCalled();

            view.rerender(
                <MemoryRouter initialEntries={['/books/page/1']}>
                    <BookPagination totalPages={9} currentPage={1} baseUrl="/books/page/1" />
                </MemoryRouter>,
            );

            expect(screen.getByRole('navigation')).toBeInTheDocument();
            expect(complaints).toHaveBeenCalledWith(
                expect.stringContaining('data-list-column'),
                expect.anything(),
            );
        } finally {
            complaints.mockRestore();
        }
    });

    it('complains once, not on every commit after it', () => {
        // It runs after every commit until there is a nav to look at, so it
        // has to stop of its own accord — a page that re-renders as a reader
        // types into the search box would otherwise fill the console.
        const complaints = vi.spyOn(console, 'error').mockImplementation(() => {});
        try {
            const view = render(
                <MemoryRouter initialEntries={['/books/page/1']}>
                    <BookPagination totalPages={9} currentPage={1} baseUrl="/books/page/1" />
                </MemoryRouter>,
            );
            for (const page of [2, 3, 4]) {
                view.rerender(
                    <MemoryRouter initialEntries={['/books/page/1']}>
                        <BookPagination totalPages={9} currentPage={page} baseUrl="/books/page/1" />
                    </MemoryRouter>,
                );
            }

            expect(complaints).toHaveBeenCalledTimes(1);
        } finally {
            complaints.mockRestore();
        }
    });

    it('stays quiet when the caller put it in one', () => {
        const complaints = vi.spyOn(console, 'error').mockImplementation(() => {});
        try {
            renderPager();

            expect(complaints).not.toHaveBeenCalled();
        } finally {
            complaints.mockRestore();
        }
    });

    it('keeps one grid whichever page the reader is on', () => {
        // The active tile is shadcn's outline variant and carries a border;
        // its neighbours are ghost buttons and do not. A border counts towards
        // a shrink-to-fit width, so the widest cell on page 1 was a five-digit
        // neighbour at 52.2 and on page 22000 it was the active tile at 53.5 —
        // measured in Chrome, and a pixel of grid that moved as the reader
        // paged through. Every numbered cell carries the same border now.
        const restore = stubGeometry({
            row: 1200,
            cell: 52.2,
            digit: 7.2312,
            pad: 16,
            border: 1.3333,
            arrow: 36,
            ellipsis: 36,
            gap: 4,
        });
        try {
            const early = renderPager({
                totalPages: 47377,
                currentPage: 1,
                baseUrl: '/books/page/1',
            });
            const onPageOne = screen.getByRole('link', { name: 'goToPage:47377' }).style.minWidth;
            early.unmount();

            renderPager({ totalPages: 47377, currentPage: 22000, baseUrl: '/books/page/22000' });
            const deepIn = screen.getByRole('link', { name: 'goToPage:47377' }).style.minWidth;

            expect(onPageOne).toBe(deepIn);
            expect(deepIn).toBe('54px');
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
