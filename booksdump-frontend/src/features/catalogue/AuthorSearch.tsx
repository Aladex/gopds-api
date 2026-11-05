import React, { useState, useEffect } from 'react';
import { Link, useLocation, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';

import { cn } from '@/shared/lib/utils';
import { isPlainClick } from '@/shared/lib/isPlainClick';
import { CONTENT_COLUMN, listColumn } from '@/shared/layout/breakpoints';
import * as booksApi from '@/api/books';
import { pageFromQuery, searchPath } from '@/features/catalogue/addresses';
import type { AuthorCardData } from '@/api/books';
import { useAuth } from '@/context/AuthContext';
import { useAuthor } from '@/context/AuthorContext';
import { useSearchBar } from '@/context/SearchBarContext';
import AuthorCard, { AuthorCardSkeleton } from '@/features/catalogue/AuthorCard';
import BookPagination from '@/features/catalogue/BookPagination';
import { Button } from '@/shared/ui/button';

const PAGE_SIZE = 10;
/** Past this many, nobody is paging: more of the name is the way through. */
const TOO_MANY_AUTHORS = 200;

const GRID = 'grid grid-cols-1 gap-1.5 card:grid-cols-2 card:gap-2';

/**
 * AuthorSearch lists the authors matching a name from the search panel.
 *
 * It is a stop on the way to a book list rather than a destination, and the
 * reader is picking which of several same-named authors they meant. A column of
 * near-identical names gives them nothing to pick with, so each author is a
 * card with their covers, and with how many books are behind it — which is
 * usually the whole answer, one Dostoyevsky holding 184 books beside four
 * holding one apiece.
 *
 * The count is taken under the reader's own books language, the same filter the
 * list behind the row uses, so the number is what they will actually get.
 *
 * The head line, the grid and the pager keep their places through a load: the
 * skeleton draws the same cards in the same grid.
 */
const AuthorSearch: React.FC = () => {
    const { t } = useTranslation();
    const [searchParams] = useSearchParams();
    const author = searchParams.get('q') ?? '';
    const page = pageFromQuery(searchParams);
    const [authors, setAuthors] = useState<AuthorCardData[]>([]);
    const [loading, setLoading] = useState(true);
    const location = useLocation();
    const [totalPages, setTotalPages] = useState(0);
    const [total, setTotal] = useState<number | undefined>();
    const { setSearchItem, clearSelectedSearch } = useSearchBar();
    const { user } = useAuth();
    const { setAuthorName } = useAuthor();

    useEffect(() => {
        const fetchAuthors = async () => {
            setLoading(true);
            try {
                window.scrollTo(0, 0);
                const responseData = await booksApi.listAuthors({
                    limit: PAGE_SIZE,
                    offset: (page - 1) * PAGE_SIZE,
                    author,
                    lang: user?.books_lang || '',
                });

                // A zero-hit search answers authors: null (a nil Go slice
                // marshals to null). Anything that is not an array means "no
                // results": skipping the update would leave the previous
                // search's authors and page count on screen.
                if (Array.isArray(responseData.authors)) {
                    setAuthors(responseData.authors);
                    setTotalPages(responseData.length);
                    setTotal(responseData.total);
                } else {
                    setAuthors([]);
                    setTotalPages(0);
                    setTotal(undefined);
                }
            } catch (error) {
                console.error('Error fetching authors:', error);
                setAuthors([]); // Set to an empty array in case of error
                setTotal(undefined);
            } finally {
                setLoading(false);
            }
        };

        if (author) {
            fetchAuthors().then((r) => r);
        }
    }, [author, location.search, page, user?.books_lang]);

    /** Clicking through leaves the search box empty so the filter, not a stale query, applies. */
    const handleAuthorClick = (fullName: string) => {
        setSearchItem('');
        // Carried over so the scope beside the search box can name the author
        // without asking the server for what was just on screen.
        setAuthorName(fullName);
    };

    return (
        <div className="flex min-h-[calc(100vh-200px)] flex-col">
            {/*
              One column element, holding the rows and the pager together. The
              pager fills its parent and fits its window to what that row
              measures; it used to sit outside this column, in the page's
              full-width root, so on a wide viewport it measured the viewport
              and put its arrows a couple of hundred pixels clear of the rows.
            */}
            <div className={cn(CONTENT_COLUMN, 'flex flex-1 flex-col')} {...listColumn}>
                <div
                    data-testid="author-head"
                    className="flex h-8 items-center justify-between gap-3 overflow-hidden text-[13px] whitespace-nowrap card:text-sm"
                >
                    {!loading && Boolean(total) && (
                        <>
                            <span>{t('authorSearch.total', { count: total })}</span>
                            {(total ?? 0) > TOO_MANY_AUTHORS && (
                                <span className="min-w-0 truncate opacity-80">
                                    <span className="hidden card:inline">
                                        {t('authorSearch.narrowDown')}
                                    </span>
                                    <span className="card:hidden">
                                        {t('authorSearch.narrowDownShort')}
                                    </span>
                                </span>
                            )}
                        </>
                    )}
                </div>
                {loading ? (
                    <div data-testid="author-grid" className={GRID}>
                        {Array.from({ length: PAGE_SIZE }).map((_, index) => (
                            <AuthorCardSkeleton key={index} />
                        ))}
                    </div>
                ) : authors.length === 0 ? (
                    <div className="rounded border border-border bg-card px-5 py-8 text-center">
                        <h3 className="mt-0 mb-1.5 text-[17px] leading-[1.3] font-semibold">
                            {t('noAuthorsFound', { query: author })}
                        </h3>
                        <p className="mt-0 mb-4 text-sm text-muted-foreground">
                            {t('authorSearch.emptyHint')}
                        </p>
                        <Button variant="outline" asChild>
                            <Link
                                to={searchPath(author)}
                                // The panel would stay on authors and answer
                                // the reader's next search with them.
                                onClick={(event) => {
                                    if (isPlainClick(event)) {
                                        clearSelectedSearch();
                                    }
                                }}
                            >
                                {t('authorSearch.searchBooks')}
                            </Link>
                        </Button>
                    </div>
                ) : (
                    <div data-testid="author-grid" className={GRID}>
                        {authors.map((hit) => (
                            <AuthorCard
                                key={hit.id}
                                author={hit}
                                query={author}
                                onGo={handleAuthorClick}
                            />
                        ))}
                    </div>
                )}
                <div
                    data-testid="author-pager-box"
                    className="mt-auto flex h-[72px] items-center justify-center"
                >
                    {loading && totalPages <= 1 ? (
                        <span
                            aria-hidden="true"
                            className="block h-8 w-[230px] max-w-full animate-pulse rounded-md bg-muted"
                        />
                    ) : (
                        <BookPagination
                            totalPages={totalPages}
                            currentPage={page}
                            baseUrl={location.pathname + location.search}
                        />
                    )}
                </div>
            </div>
        </div>
    );
};

export default AuthorSearch;
