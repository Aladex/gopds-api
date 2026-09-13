import React from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';

import {
    Pagination,
    PaginationContent,
    PaginationEllipsis,
    PaginationItem,
    PaginationLink,
} from '@/shared/ui/pagination';
import { cn } from '@/shared/lib/utils';

import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { LIST_COLUMN_ATTR, LIST_COLUMN_SELECTOR } from '@/shared/layout/breakpoints';
import {
    fitSiblingCount,
    pageHref,
    paginationRange,
    type PagerMetrics,
} from '@/features/catalogue/paginationRange';

/** How much clear space to leave between the numbers and each arrow. */
const BREATHING = 8;

/** Every digit a page number can be made of. */
const DIGITS = ['0', '1', '2', '3', '4', '5', '6', '7', '8', '9'];

/**
 * measureCellWidth asks how wide the widest page number the pager could ever
 * show would draw — not the widest one it happens to be showing.
 *
 * The difference is the whole of the fixed point. Reading the rendered cells
 * makes the shared width a function of the window, and the window is a
 * function of the shared width, so the two can chase each other: driven with
 * proportional advances, a 44 500-page list at page 40 903 in a 1064px row
 * settled nowhere. Four siblings read a 56px cell and fit five; the
 * five-sibling window admits page 40 898 — two of the widest digit where
 * 40 899 has one — which reads 57px and fits only four; dropping it returns
 * the reading to 56px. The reader sees a row flickering between nine and
 * eleven numbers.
 *
 * So the answer comes from a rig of probes instead, one per digit, each
 * holding as many copies of that digit as the last page has. None of them is a
 * page; all of them are boxes a page could need, and which one is widest does
 * not depend on where the reader is.
 *
 * This is not a precaution against a font nobody has. Measured in Chrome on
 * the stand, with the cells' computed font-variant-numeric reading
 * tabular-nums, `11111` draws 50.61px against 53.49px for every other digit
 * repeated five times — the request is made and the font does not honour it.
 * The worst case is therefore five of the widest digit, not the last page.
 * Where a font does honour it all ten probes measure the same and this costs
 * nothing; where it does not, the over-estimate against a real page number is
 * at most a pixel or two, which costs a sibling and never an overflow — the
 * same trade the ellipsis already makes.
 */
function measureCellWidth(rig: HTMLElement): number {
    const probes = [...rig.querySelectorAll<HTMLElement>('[data-slot="pagination-probe"]')];
    if (probes.length === 0) {
        return 0;
    }
    const widest = Math.max(...probes.map((probe) => probe.getBoundingClientRect().width));
    // Rounded up to a whole pixel: a fractional min-width leaves the widest
    // number a hair short of its own text and wraps it.
    return widest > 0 ? Math.ceil(widest) : 0;
}

/**
 * readMetrics measures a rendered pager.
 *
 * The row it has to fit inside is the nav, which spans the card column; the ul
 * only spans what it holds. Returns null when nothing has a width yet — under
 * jsdom every box measures zero, and a pager fitted to a zero-width row would
 * show one page number.
 *
 * Nothing here is read off the window the pager is currently drawing. The nav
 * is the column, the arrow and the gap do not move with the page numbers, and
 * the ellipsis comes from the rig rather than from a rendered one — a window
 * with no gap in it has no ellipsis to measure, and standing in the cell width
 * for it would be one more way for the answer to depend on the question.
 */
function readMetrics(
    nav: HTMLElement,
    group: HTMLElement,
    rig: HTMLElement,
    cell: number,
): PagerMetrics | null {
    const row = nav.getBoundingClientRect().width;
    if (row <= 0 || cell <= 0) {
        return null;
    }
    const arrow = nav.querySelector('a')?.getBoundingClientRect().width ?? cell;
    const ellipsis =
        rig.querySelector('[data-slot="pagination-ellipsis-probe"]')?.getBoundingClientRect()
            .width ?? cell;
    const gap = parseFloat(getComputedStyle(group).columnGap) || 0;
    return { row, arrow, cell, ellipsis, gap, breathing: BREATHING };
}

interface PaginationProps {
    totalPages: number;
    currentPage: number;
    /** Where the reader is, query string included — it carries the search. */
    baseUrl: string;
}

/**
 * pageCellMinWidth is what a pager falls back on where nothing can be measured
 * — jsdom, and any viewport that reports zero-width boxes.
 *
 * It sizes every numbered cell by the digit count of the last page, so the
 * rhythm stays even whether the reader is on page 7 or 44557 and the active
 * tile keeps one size. A tier is necessarily wider than the text it was chosen
 * for; where there is a layout to read, measureCellWidth replaces it with what
 * the digits actually draw.
 */
function pageCellMinWidth(digits: number, narrow: boolean): string {
    if (narrow) {
        if (digits >= 6) return 'min-w-14';
        if (digits === 5) return 'min-w-12';
        if (digits === 4) return 'min-w-10';
        if (digits === 3) return 'min-w-9';
        return 'min-w-7';
    }
    if (digits >= 6) return 'min-w-16';
    if (digits === 5) return 'min-w-14';
    if (digits === 4) return 'min-w-12';
    if (digits === 3) return 'min-w-10';
    return 'min-w-9';
}

/**
 * BookPagination pages through a list route.
 *
 * Every page is a real link, so a reader can open one in a new tab or copy its
 * address; the click handler only takes over to keep navigation client-side.
 */
const BookPagination: React.FC<PaginationProps> = ({ totalPages, currentPage, baseUrl }) => {
    const navigate = useNavigate();
    const { t } = useTranslation();

    // A narrow screen has no room for seven pages either side, and long page
    // numbers make wide cells, so how many neighbours fit depends on both.
    const isNarrow = useMediaQuery('(max-width: 779px)');
    const digits = String(totalPages).length;
    const compact = digits >= 4;
    const boundaryCount = isNarrow ? 1 : compact ? 2 : 3;
    // The window used to be guessed from the digit count, and the guess was
    // wrong three times running — arithmetic said 300px where the browser
    // measured 329, and the arrows ended up off the screen. So the pager
    // renders its narrowest window, measures itself, and opens up to whatever
    // the row actually takes. The fallback below is only for a viewport that
    // cannot be measured at all, jsdom among them.
    //
    // Both answers land in one piece of state because they are one answer: the
    // cell width decides how many cells fit, and setting them apart would
    // render a window fitted to a width it is not being drawn at.
    const [fitted, setFitted] = React.useState<{ siblings: number; cell: number } | null>(null);
    const navRef = React.useRef<HTMLElement | null>(null);
    const groupRef = React.useRef<HTMLLIElement | null>(null);
    const rigRef = React.useRef<HTMLSpanElement | null>(null);

    React.useLayoutEffect(() => {
        const nav = navRef.current;
        const group = groupRef.current;
        const rig = rigRef.current;
        if (!nav || !group || !rig || typeof ResizeObserver === 'undefined') {
            return;
        }
        const measure = () => {
            const cell = measureCellWidth(rig);
            const metrics = readMetrics(nav, group, rig, cell);
            // Leave the fallback in place rather than fitting to a zero-width
            // row: a hidden pager would come back showing one page number.
            if (!metrics) {
                return;
            }
            const siblings = fitSiblingCount(currentPage, totalPages, metrics, { boundaryCount });
            // Only on a real change, so a redundant callback costs a render
            // rather than starting one.
            setFitted((previous) =>
                previous?.siblings === siblings && previous.cell === cell
                    ? previous
                    : { siblings, cell },
            );
        };
        // Read once here and then whenever something it depends on moves. A
        // browser's observer also delivers one observation for every box the
        // moment it starts watching it, so a cold mount reads twice — this call
        // and that delivery — and the second reading returns what the first
        // did. Two readings, one answer; the point is that the answer stops
        // moving, not that it is taken once.
        measure();
        const observer = new ResizeObserver(measure);
        // Two things are watched, and neither of them is anything this state
        // resizes. The nav is the column the row has to fit inside. The probes
        // are the type: they carry no shared minimum, so unlike the numbered
        // cells they are free to get smaller as well as larger, and a text
        // zoom, a minimum-font-size setting or a webfont arriving late moves
        // them in both directions.
        //
        // The group of numbers is deliberately *not* watched. Its width is a
        // consequence of the answer rather than an input to it, and watching it
        // was what let a measurement taken off the rendered window wake the
        // window that produced it.
        observer.observe(nav);
        [...rig.children].forEach((probe) => observer.observe(probe));
        return () => observer.disconnect();
    }, [boundaryCount, currentPage, totalPages]);

    // Development only, and compiled out of a production build: the pager
    // fills its parent and fits its window to what that row measures, so a
    // caller that renders it beside the list's column instead of inside it
    // gets a row fitted to the wrong box with nothing else to show for it.
    //
    // No dependency list, on purpose. A list page's ordinary shape is to render
    // with no page count at all and raise it when its request comes back, and
    // until then this component returns null and there is no nav to look at. A
    // check that ran once at mount would find nothing and never look again —
    // which is the loading shape of most of the pages that call it. This one
    // runs after every commit until there is a nav, then once, and stops.
    const columnChecked = React.useRef(false);
    React.useEffect(() => {
        if (!import.meta.env.DEV || columnChecked.current) {
            return;
        }
        const nav = navRef.current;
        if (!nav) {
            return;
        }
        columnChecked.current = true;
        if (nav.closest(LIST_COLUMN_SELECTOR)) {
            return;
        }
        console.error(
            `BookPagination must be rendered inside the element marked ${LIST_COLUMN_ATTR} — ` +
                'the one box that also holds the list. It fills its parent and fits its window ' +
                'to what that row measures, so outside that column it measures the wrong width.',
            nav,
        );
    });

    const siblingCount = fitted?.siblings ?? (isNarrow ? (compact ? 0 : 1) : compact ? 2 : 3);
    const items = paginationRange(currentPage, totalPages, { boundaryCount, siblingCount });
    const cellMinWidth = pageCellMinWidth(digits, isNarrow);
    // Everything a numbered cell is, apart from which page it is. The sentinel
    // below wears it too, so the box it reports is the box a cell would have.
    const cellClassName = cn(
        // shadcn's button carries transition-all, which animates the width as
        // well. The pager measures itself whenever the numbers resize, so an
        // animated width means it reads a frame of the animation: forcing the
        // cells to 19px in Chrome and back left the grid at 63px for 13px
        // digits that draw 54, because the last reading it took was in flight.
        // Colours may fade; the grid may not.
        'h-8 w-auto px-2 font-semibold tabular-nums transition-colors',
        // The active page is shadcn's outline variant, which carries a border;
        // a ghost button does not. A border counts towards a shrink-to-fit
        // width, so without one on every cell the measured grid was 53px on
        // page 1 and 54px on page 22000 — the rhythm moving as the reader
        // paged. Same box on every cell; the colour is all the active one adds.
        'border border-transparent',
        isNarrow && 'h-7 px-1.5 text-xs',
    );
    // Whether the window is standing anything in. A pager that shows every page
    // has no window to open, and spreading such a row to the column edges would
    // leave the two arrows half a screen from the numbers they step through.
    const elides = items.includes('ellipsis');

    if (totalPages <= 1) {
        return null;
    }

    const href = (page: number) => pageHref(baseUrl, page);

    const go = (event: React.MouseEvent, page: number) => {
        // Leave the modified clicks to the browser: they mean "somewhere else".
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) {
            return;
        }
        event.preventDefault();
        navigate(href(page));
    };

    const step = (page: number, label: string, icon: React.ReactNode) => {
        const disabled = page < 1 || page > totalPages;
        return (
            <PaginationItem>
                <PaginationLink
                    href={disabled ? undefined : href(page)}
                    aria-label={label}
                    aria-disabled={disabled || undefined}
                    onClick={(event) => (disabled ? event.preventDefault() : go(event, page))}
                    className={cn(
                        'w-auto min-w-9 px-2',
                        isNarrow && 'h-7 min-w-7 px-1.5',
                        disabled && 'pointer-events-none opacity-40',
                    )}
                >
                    {icon}
                </PaginationLink>
            </PaginationItem>
        );
    };

    return (
        <Pagination ref={navRef}>
            {/*
             * When the window hides pages the row spans the whole column and
             * the arrows sit on its ends, level with the edges of the cards
             * above. The numbers stay together in the middle: justify-between
             * centres them because both arrows are the same width. A desktop
             * used to keep the row content-width even then, which put the
             * arrows a couple of hundred pixels inside the cards with nothing
             * but air on either side.
             *
             * A short catalogue is the other way round. Nothing is hidden, so
             * there is nothing for the arrows to be at the far end of, and the
             * row takes its content width and centres — the nav around it is
             * already justify-center.
             */}
            <PaginationContent
                className={cn(elides && 'w-full justify-between', isNarrow && 'gap-0.5')}
            >
                {step(currentPage - 1, t('previousPage'), <span aria-hidden="true">‹</span>)}

                <PaginationItem
                    ref={groupRef}
                    className={cn('relative flex items-center gap-1', isNarrow && 'gap-0.5')}
                >
                    {/*
                     * The measuring rig: one box per digit, each holding as
                     * many copies of that digit as the last page has, plus one
                     * ellipsis. It is what the pager measures itself against,
                     * and nothing in it depends on which pages are on screen —
                     * that independence is what makes the fitted answer a fixed
                     * point rather than one half of a feedback loop.
                     *
                     * These are also the only boxes in the row free to get
                     * *smaller*: every numbered cell is held at the fitted
                     * width, so when the type shrinks nothing else moves and an
                     * observer would never wake. Measured in Chrome, the grid
                     * stayed at 71px cells for 13px digits that draw 54.
                     *
                     * Out of flow, out of the accessibility tree and with no
                     * href, so nothing reads them and nothing can tab to them.
                     */}
                    <span
                        ref={rigRef}
                        aria-hidden="true"
                        className="pointer-events-none absolute top-0 left-0 -z-10 flex opacity-0"
                    >
                        {DIGITS.map((digit) => (
                            <PaginationLink
                                key={digit}
                                tabIndex={-1}
                                data-slot="pagination-probe"
                                size={null}
                                className={cellClassName}
                            >
                                {digit.repeat(digits)}
                            </PaginationLink>
                        ))}
                        <PaginationEllipsis
                            data-slot="pagination-ellipsis-probe"
                            className={cn(isNarrow && 'h-7 w-5')}
                        />
                    </span>
                    {items.map((item, index) =>
                        item === 'ellipsis' ? (
                            <PaginationEllipsis
                                key={`gap-${index}`}
                                // Nobody taps an ellipsis — it is an
                                // aria-hidden span holding a 16px glyph. At a
                                // button's 28px it costs a phone the very page
                                // neighbours it stands for, so on a phone it
                                // keeps the height and gives up the width.
                                className={cn(isNarrow && 'h-7 w-5')}
                            />
                        ) : (
                            <PaginationLink
                                key={item}
                                href={href(item)}
                                isActive={item === currentPage}
                                aria-label={t('goToPage', { page: item })}
                                onClick={(event) => go(event, item)}
                                // size="icon" carries size-10/sm:size-8, which
                                // beats w-auto in the stylesheet and froze every
                                // cell at 36px — five digits overflowed the
                                // button and the active tile. Null skips it; the
                                // shared min-width below keeps the grid.
                                size={null}
                                // An inline declaration outranks the utility
                                // class below by cascade priority, not by
                                // being the larger number — so the measured
                                // width replaces the fallback tier rather than
                                // competing with it.
                                style={fitted ? { minWidth: `${fitted.cell}px` } : undefined}
                                className={cn(
                                    // tabular-nums is asked for, and it is
                                    // not always given: measured in Chrome on
                                    // the stand, the computed value is
                                    // tabular-nums and the rendered digits are
                                    // still proportional — five copies of `1`
                                    // draw 50.61px where five of any other
                                    // digit draw 53.49. So the request is kept
                                    // for the platforms that honour it, and
                                    // nothing above it is allowed to assume
                                    // that they do.
                                    cellClassName,
                                    // Only until there is a measurement: the
                                    // tier is a floor, and leaving it on would
                                    // keep a cell from ever being narrower
                                    // than the tier it was handed.
                                    !fitted && cellMinWidth,
                                    // The active page uses shadcn's outline variant,
                                    // which carries a dark:bg-input/30 of its own.
                                    // tailwind-merge treats a variant class and a
                                    // plain one as different properties, so the
                                    // dark half has to be named explicitly or it
                                    // survives and wins.
                                    item === currentPage && [
                                        // The shared border above is
                                        // transparent; the active tile spends
                                        // it on its own colour rather than
                                        // changing the size of its box.
                                        'border-primary',
                                        'bg-primary text-primary-foreground dark:bg-primary',
                                        'hover:bg-primary hover:text-primary-foreground dark:hover:bg-primary',
                                    ],
                                )}
                            >
                                {item}
                            </PaginationLink>
                        ),
                    )}
                </PaginationItem>

                {step(currentPage + 1, t('nextPage'), <span aria-hidden="true">›</span>)}
            </PaginationContent>
        </Pagination>
    );
};

export default BookPagination;
