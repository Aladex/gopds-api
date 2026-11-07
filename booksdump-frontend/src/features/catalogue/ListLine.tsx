import React from 'react';
import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { ChevronLeft } from 'lucide-react';

import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { CONTENT_COLUMN, PHONE_QUERY } from '@/shared/layout/breakpoints';
import { cn } from '@/shared/lib/utils';
import BookLanguage from '@/shared/layout/BookLanguage';
import { booksLanguageApplies, isSearchPath } from '@/features/catalogue/addresses';
import { useCatalogueSearchContext } from '@/features/catalogue/CatalogueSearchContext';
import { authorNameFromLocation } from '@/features/catalogue/hooks/useSearchScope';
import ScopeChip from '@/features/catalogue/ScopeChip';
import { CATALOGUE_PATH } from '@/features/catalogue/hooks/useSearchScope';

/**
 * ListLine is the line between the sections and the list. It says what the list
 * is and what narrows it: on a scoped list the scope, which is the one thing that
 * costs a phone height, and from 601 up the books language as well, since only a
 * phone has it in the bar.
 *
 * The line stands whether the scope is on or released, so releasing moves
 * nothing. Entering a scope does move the list down by the line's height, with
 * the rest of the page changing around it.
 */
const ListLine: React.FC = () => {
    const { t } = useTranslation();
    const { pathname, search } = useLocation();
    const { scope } = useCatalogueSearchContext();
    const isPhone = useMediaQuery(PHONE_QUERY);

    const inList = scope.kind !== null && scope.kind !== 'favorites';
    const language = booksLanguageApplies(pathname);

    const upLink = (
        <Link
            to={CATALOGUE_PATH}
            aria-label={t('backToCatalogue')}
            title={t('backToCatalogue')}
            className="relative z-[1] -ml-1 flex size-10 flex-none items-start justify-center pt-1.5 text-muted-foreground min-[601px]:-ml-2 min-[601px]:size-8"
        >
            <ChevronLeft aria-hidden="true" className="size-5" />
        </Link>
    );

    if (isPhone) {
        return inList ? (
            <div
                data-testid="list-line"
                className="flex h-8 flex-none items-start px-2 text-[13px]"
            >
                {upLink}
                <ScopeChip className="mt-[3px]" />
            </div>
        ) : (
            <div className="h-2 flex-none" />
        );
    }

    if (!language) {
        return <div className="h-3 flex-none" />;
    }

    const heading = () => {
        if (scope.kind === 'favorites') return t('favouritesTab');
        if (authorNameFromLocation(pathname, search)) return t('authorsTab');
        if (isSearchPath(pathname)) return t('list.found');
        if (pathname.startsWith('/books/page/')) return t('list.newBooks');
        return t('booksTab');
    };

    return (
        <div className="mt-2 flex-none px-4">
            <div
                data-testid="list-line"
                className={cn(
                    CONTENT_COLUMN,
                    'flex h-8 items-center gap-1.5 text-[13px] text-muted-foreground',
                )}
            >
                {inList ? (
                    <>
                        {upLink}
                        <ScopeChip />
                        <span className="flex-1" />
                    </>
                ) : (
                    <>
                        <span className="pl-2 font-medium text-foreground">{heading()}</span>
                        <span aria-hidden="true">·</span>
                    </>
                )}
                <span>{t('booksLanguageLabel')}</span>
                <BookLanguage where="line" className="h-8" />
            </div>
        </div>
    );
};

export default ListLine;
