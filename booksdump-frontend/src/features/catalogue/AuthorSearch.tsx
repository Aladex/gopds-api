import React, { useState, useEffect } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router';
import { useTranslation } from 'react-i18next';

import * as booksApi from '@/api/books';
import type { Author } from '@/api/books';
import { useAuth } from '@/context/AuthContext';
import { useAuthor } from '@/context/AuthorContext';
import { useSearchBar } from '@/context/SearchBarContext';
import BookPagination from '@/features/catalogue/BookPagination';
import SkeletonCard from '@/features/catalogue/SkeletonCard';

const PAGE_SIZE = 10;

/**
 * AuthorSearch lists the authors matching a name from the search panel.
 *
 * It is a stop on the way to a book list rather than a destination, and the
 * reader is picking which of several same-named authors they meant. A column of
 * near-identical names gives them nothing to pick with, so each row also says
 * how many books are behind it — which is usually the whole answer, one
 * Dostoyevsky holding 184 books beside four holding one apiece.
 *
 * The count is taken under the reader's own books language, the same filter the
 * list behind the row uses, so the number is what they will actually get.
 */
const AuthorSearch: React.FC = () => {
    const { t } = useTranslation();
    const { page } = useParams<{ page: string }>();
    const { author } = useParams<{ author: string }>();
    const [authors, setAuthors] = useState<Author[]>([]);
    const [loading, setLoading] = useState(true);
    const location = useLocation();
    const [totalPages, setTotalPages] = useState(0);
    const baseUrl = window.location.pathname.replace(/\/\d+$/, '');
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const { user } = useAuth();
    const { clearAuthorBook, setAuthorName } = useAuthor();

    useEffect(() => {
        const fetchAuthors = async () => {
            setLoading(true);
            try {
                window.scrollTo(0, 0);
                const currentPage = parseInt(page || '1', 10);

                const responseData = await booksApi.listAuthors({
                    limit: PAGE_SIZE,
                    offset: (currentPage - 1) * PAGE_SIZE,
                    author: decodeURIComponent(author || ''),
                    lang: user?.books_lang || '',
                });

                if (responseData.authors && Array.isArray(responseData.authors)) {
                    setAuthors(responseData.authors);
                    setTotalPages(responseData.length);
                }
            } catch (error) {
                console.error('Error fetching authors:', error);
                setAuthors([]); // Set to an empty array in case of error
            } finally {
                setLoading(false);
            }
        };

        if (author) {
            fetchAuthors().then((r) => r);
        }
    }, [author, location.search, page, user?.books_lang]);

    /** Clicking through leaves the search box empty so the filter, not a stale query, applies. */
    const handleAuthorClick = (authorId: number, fullName: string) => {
        setSearchItem('');
        clearAuthorBook();
        // Carried over so the scope beside the search box can name the author
        // without asking the server for what was just on screen.
        setAuthorName(fullName);
        navigate(`/books/find/author/${authorId}/1`);
    };

    return (
        <div className="flex min-h-[calc(100vh-200px)] flex-col">
            <div className="flex-1">
                <div className="mx-auto w-full max-w-[1200px] py-2">
                    {loading ? (
                        <div className="flex flex-col gap-1.5">
                            {Array.from({ length: PAGE_SIZE }).map((_, index) => (
                                <SkeletonCard key={index} />
                            ))}
                        </div>
                    ) : authors.length === 0 ? (
                        <div className="rounded border border-border bg-card p-8">
                            <p className="text-center text-lg">{t('noAuthorsFound')}</p>
                        </div>
                    ) : (
                        <div className="rounded border border-border bg-card p-4">
                            <h2 className="mb-2 text-center text-2xl font-medium">
                                {t('authorsSearch')}
                            </h2>
                            {/* Tailwind runs without preflight, so a bare <ul> keeps
                                both its markers and the browser's 40px inline start
                                padding, which would indent every author. */}
                            <ul className="list-none p-0">
                                {authors.map((author) => (
                                    <li key={author.id}>
                                        <button
                                            type="button"
                                            onClick={() =>
                                                handleAuthorClick(author.id, author.full_name)
                                            }
                                            className="flex w-full items-baseline justify-between gap-3 rounded px-3 py-2 text-left hover:bg-accent hover:text-accent-foreground"
                                        >
                                            <span>{author.full_name}</span>
                                            {author.books_count ? (
                                                /* Kept off the name's baseline
                                                   run so a long name wraps
                                                   without pushing the number
                                                   onto its own line. */
                                                <span className="shrink-0 text-sm whitespace-nowrap text-muted-foreground">
                                                    {t('bookCount', {
                                                        count: author.books_count,
                                                    })}
                                                </span>
                                            ) : null}
                                        </button>
                                    </li>
                                ))}
                            </ul>
                        </div>
                    )}
                </div>
            </div>
            <div className="mt-auto flex justify-center pt-4">
                <BookPagination
                    totalPages={totalPages}
                    currentPage={parseInt(page || '1', 10)}
                    baseUrl={baseUrl}
                />
            </div>
        </div>
    );
};

export default AuthorSearch;
