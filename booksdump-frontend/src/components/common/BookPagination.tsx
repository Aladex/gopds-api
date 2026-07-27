import React from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

import {
    Pagination,
    PaginationContent,
    PaginationEllipsis,
    PaginationItem,
    PaginationLink,
} from '@/components/ui/pagination';
import { cn } from '@/lib/utils';

import { useMediaQuery } from '../hooks/useMediaQuery';
import { pageBaseUrl, paginationRange } from './paginationRange';

interface PaginationProps {
    totalPages: number;
    currentPage: number;
    baseUrl: string;
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
    const base = pageBaseUrl(baseUrl);

    // A narrow screen has no room for seven pages either side, so the window
    // shrinks rather than wrapping onto a second line.
    const isNarrow = useMediaQuery('(max-width: 779px)');
    const items = paginationRange(currentPage, totalPages, {
        boundaryCount: isNarrow ? 1 : 3,
        siblingCount: isNarrow ? 1 : 3,
    });

    if (totalPages <= 1) {
        return null;
    }

    const href = (page: number) => `${base}/${page}`;

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
                                className={cn(
                                    // The icon size is a fixed square, which a
                                    // five-digit page number overflows — and this
                                    // catalogue runs to five digits. Keep the
                                    // square as a minimum and let wide numbers
                                    // grow instead of colliding.
                                    'w-auto min-w-9 px-2 font-semibold',
                                    isNarrow && 'h-7 min-w-7 px-1.5 text-xs',
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
