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
import {
    fitSiblingCount,
    pageHref,
    paginationRange,
    type PagerMetrics,
} from '@/features/catalogue/paginationRange';

/** How much clear space to leave between the numbers and each arrow. */
const BREATHING = 8;
/** Never open the window wider than this, however much room there is. */
const MAX_SIBLINGS = 3;

/**
 * readMetrics measures a rendered pager.
 *
 * The row it has to fit inside is the nav, which spans the column; the ul only
 * spans what it holds. Returns null when nothing has a width yet — under jsdom
 * every box measures zero, and a pager fitted to a zero-width row would show
 * one page number.
 */
function readMetrics(nav: HTMLElement, group: HTMLElement): PagerMetrics | null {
    const row = nav.getBoundingClientRect().width;
    const cell = group.querySelector('a')?.getBoundingClientRect().width ?? 0;
    if (row <= 0 || cell <= 0) {
        return null;
    }
    const arrow = nav.querySelector('a')?.getBoundingClientRect().width ?? cell;
    // A window with no gap in it has no ellipsis to measure. Standing in the
    // cell width overestimates, which only ever costs a sibling — never an
    // overflow.
    const ellipsis =
        group.querySelector('[data-slot="pagination-ellipsis"]')?.getBoundingClientRect().width ??
        cell;
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
 * pageCellMinWidth sizes every numbered cell of a pager by the digit count of
 * its last page: the rhythm then stays even whether the reader is on page 7
 * or 44557, and the active tile keeps one size. The tiers assume the widest
 * digit mix — the UI falls back to the system sans, and tabular figures
 * cannot be relied on across platforms (measured: 5 digits ≈ 39px at text-sm,
 * ≈ 34px at text-xs, plus horizontal padding).
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
    const [fitted, setFitted] = React.useState<number | null>(null);
    const navRef = React.useRef<HTMLElement | null>(null);
    const groupRef = React.useRef<HTMLLIElement | null>(null);

    React.useLayoutEffect(() => {
        const nav = navRef.current;
        const group = groupRef.current;
        if (!nav || !group || typeof ResizeObserver === 'undefined') {
            return;
        }
        const measure = () => {
            const metrics = readMetrics(nav, group);
            // Leave the fallback in place rather than fitting to a zero-width
            // row: a hidden pager would come back showing one page number.
            if (!metrics) {
                return;
            }
            setFitted(
                fitSiblingCount(currentPage, totalPages, metrics, {
                    boundaryCount,
                    maxSiblings: MAX_SIBLINGS,
                }),
            );
        };
        measure();
        const observer = new ResizeObserver(measure);
        observer.observe(nav);
        return () => observer.disconnect();
    }, [boundaryCount, currentPage, totalPages]);

    const siblingCount = fitted ?? (isNarrow ? (compact ? 0 : 1) : compact ? 2 : 3);
    const items = paginationRange(currentPage, totalPages, { boundaryCount, siblingCount });
    const cellMinWidth = pageCellMinWidth(digits, isNarrow);

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
             * On a phone the row spans the whole column and the arrows sit on
             * its ends, level with the edges of the book cards above. The
             * numbers stay together in the middle: justify-between centres
             * them because both arrows are the same width.
             */}
            <PaginationContent className={cn(isNarrow && 'w-full justify-between gap-0.5')}>
                {step(currentPage - 1, t('previousPage'), <span aria-hidden="true">‹</span>)}

                <PaginationItem
                    ref={groupRef}
                    className={cn('flex items-center gap-1', isNarrow && 'gap-0.5')}
                >
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
                                // shared min-width tier below keeps the grid.
                                size={null}
                                className={cn(
                                    'h-8 w-auto px-2 font-semibold',
                                    cellMinWidth,
                                    isNarrow && 'h-7 px-1.5 text-xs',
                                    // The active page uses shadcn's outline variant,
                                    // which carries a dark:bg-input/30 of its own.
                                    // tailwind-merge treats a variant class and a
                                    // plain one as different properties, so the
                                    // dark half has to be named explicitly or it
                                    // survives and wins.
                                    item === currentPage && [
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
