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
import { pageHref, paginationRange } from '@/features/catalogue/paginationRange';

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

    // A narrow screen has no room for seven pages either side, so the window
    // shrinks rather than wrapping onto a second line. Long page numbers make
    // wide cells, so the window tightens for them too — thirteen five-digit
    // cells would not fit a desktop row either.
    const isNarrow = useMediaQuery('(max-width: 779px)');
    const digits = String(totalPages).length;
    const compact = digits >= 4;
    const items = paginationRange(currentPage, totalPages, {
        boundaryCount: isNarrow ? 1 : compact ? 2 : 3,
        siblingCount: isNarrow ? 1 : compact ? 2 : 3,
    });
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
        <Pagination>
            <PaginationContent className={cn(isNarrow && 'gap-0.5')}>
                {step(currentPage - 1, t('previousPage'), <span aria-hidden="true">‹</span>)}

                {items.map((item, index) =>
                    item === 'ellipsis' ? (
                        <PaginationItem key={`gap-${index}`}>
                            <PaginationEllipsis className={cn(isNarrow && 'size-7')} />
                        </PaginationItem>
                    ) : (
                        <PaginationItem key={item}>
                            <PaginationLink
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
                        </PaginationItem>
                    ),
                )}

                {step(currentPage + 1, t('nextPage'), <span aria-hidden="true">›</span>)}
            </PaginationContent>
        </Pagination>
    );
};

export default BookPagination;
