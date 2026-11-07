import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { ChevronLeft, ChevronRight } from 'lucide-react';

import { Pagination, PaginationLink } from '@/shared/ui/pagination';
import { cn } from '@/shared/lib/utils';
import { formatCount } from '@/shared/lib/formatCount';
import { isPlainClick } from '@/shared/lib/isPlainClick';
import { PAGE_TURN } from '@/shared/layout/landOnList';
import JumpSheet from '@/features/catalogue/JumpSheet';
import { pageHref } from '@/features/catalogue/paginationRange';

type PhonePagerProps = {
    totalPages: number;
    currentPage: number;
    /** Where the reader is, query string included — it carries the search. */
    baseUrl: string;
    /** The total belongs to another list and a new one is on its way. */
    pending?: boolean;
};

/**
 * PhonePager is the end of a list on a phone: one row saying where the reader
 * is, with the next and the previous page either side.
 *
 * There are no page numbers. A row of them left 28px targets and five-digit
 * cells that had to take turns to fit, and readers go a page at a time: nobody
 * in the logged sample went past page eight. Any other page is one sheet away,
 * from the text in the middle.
 *
 * The arrows are real links, so a page can be opened in a new tab or copied,
 * and 44px square, because at the foot of a list they are what a thumb came for.
 */
const PhonePager: React.FC<PhonePagerProps> = ({
    totalPages,
    currentPage,
    baseUrl,
    pending = false,
}) => {
    const { t, i18n } = useTranslation();
    const navigate = useNavigate();
    const [jumping, setJumping] = useState(false);

    if (totalPages <= 1) {
        return null;
    }

    const turn = (page: number) => navigate(pageHref(baseUrl, page), { state: PAGE_TURN });

    const step = (page: number, label: string, icon: React.ReactNode) => {
        const disabled = page < 1 || page > totalPages;
        return (
            <PaginationLink
                href={disabled ? undefined : pageHref(baseUrl, page)}
                aria-label={label}
                aria-disabled={disabled || undefined}
                size={null}
                onClick={(event) => {
                    if (disabled) {
                        event.preventDefault();
                    } else if (isPlainClick(event)) {
                        event.preventDefault();
                        turn(page);
                    }
                }}
                className={cn(
                    'size-11 flex-none transition-colors',
                    disabled && 'pointer-events-none opacity-40',
                )}
            >
                {icon}
            </PaginationLink>
        );
    };

    const where = {
        page: formatCount(currentPage, i18n.language),
        total: formatCount(totalPages, i18n.language),
    };

    return (
        <>
            <Pagination aria-label={t('pager.label')} className="items-center justify-between">
                {step(currentPage - 1, t('previousPage'), <ChevronLeft className="size-5" />)}
                <button
                    type="button"
                    disabled={pending}
                    onClick={() => setJumping(true)}
                    aria-label={t('pager.whereLabel', where)}
                    className={cn(
                        'h-11 min-w-0 flex-1 rounded-md px-2 text-sm whitespace-nowrap tabular-nums',
                        'outline-none focus-visible:ring-[3px] focus-visible:ring-focus',
                    )}
                >
                    {t('pager.where', where)}
                </button>
                {step(currentPage + 1, t('nextPage'), <ChevronRight className="size-5" />)}
            </Pagination>
            <JumpSheet
                open={jumping}
                onOpenChange={setJumping}
                currentPage={currentPage}
                totalPages={totalPages}
                onJump={turn}
            />
        </>
    );
};

export default PhonePager;
