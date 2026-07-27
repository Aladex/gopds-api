import React, { useState, useEffect } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';

import * as booksApi from '@/api/books';
import { useAuthor } from '@/context/AuthorContext';
import { useSearchBar } from '@/context/SearchBarContext';
import BookPagination from '@/features/catalogue/BookPagination';
import SkeletonCard from '@/features/catalogue/SkeletonCard';

interface Author {
    id: number;
    full_name: string;
}

const PAGE_SIZE = 10;

/**
 * AuthorSearch lists the authors matching a name from the search panel.
 *
 * It is a stop on the way to a book list rather than a destination, so each row
 * carries nothing but the name and every row is a target: the reader is picking
 * which of several same-named authors they meant.
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
    const { clearAuthorBook } = useAuthor();

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
            fetchAuthors().then(r => r);
        }
    }, [author, location.search, page]);

    /** Clicking through leaves the search box empty so the filter, not a stale query, applies. */
    const handleAuthorClick = (authorId: number) => {
        setSearchItem('');
        clearAuthorBook();
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
                            <h2 className="mb-2 text-center text-2xl font-medium">{t('authorsSearch')}</h2>
                            {/* list-none: Tailwind runs without preflight, so a bare
                                <li> would still show its marker. */}
                            <ul className="list-none">
                                {authors.map((author) => (
                                    <li key={author.id}>
                                        <button
                                            type="button"
                                            onClick={() => handleAuthorClick(author.id)}
                                            className="w-full rounded px-3 py-2 text-left hover:bg-accent hover:text-accent-foreground"
                                        >
                                            {author.full_name}
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
