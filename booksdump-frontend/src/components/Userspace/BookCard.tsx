import React from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Check, Pencil, RefreshCw, Star } from 'lucide-react';

import type { Book } from '@/api/books';
import { Button } from '@/components/ui/button';
import { Expandable } from '@/components/ui/expandable';
import { cn } from '@/lib/utils';

import { API_URL } from '@/api/config';
import { useAuthor } from '@/context/AuthorContext';
import { useSearchBar } from '@/context/SearchBarContext';

/**
 * BookCard renders one row of the catalogue.
 *
 * Clicking the card reveals everything it was holding back — the rest of the
 * annotation and the authors beyond the first two. A book with twelve authors
 * would otherwise stretch every row in the list to fit the longest one.
 */

/**
 * The archived download is labelled ZIP rather than FB2+ZIP so all four labels
 * are of a length, which lets them sit in an even block the exact width of the
 * cover. Next to FB2, EPUB and MOBI it reads as what it is: the same book, in an
 * archive.
 */
const DOWNLOAD_FORMATS = [
    { id: 'zip', label: 'ZIP' },
    { id: 'fb2', label: 'FB2' },
    { id: 'epub', label: 'EPUB' },
    { id: 'mobi', label: 'MOBI' },
] as const;

/** Matches the cover, so the block of formats sits flush beneath it. */
const COVER_WIDTH = 104;

const VISIBLE_AUTHORS = 2;

export interface BookCardProps {
    book: Book;
    isSuperuser: boolean;
    formatDate: (value: string) => string;
    isBookConverting: (bookID: number, format: string) => boolean;
    onDownload: (format: string, bookID: number) => void;
    onEpubRequest: (bookID: number) => void;
    onMobiRequest: (bookID: number) => void;
    onToggleFavourite: (book: Book) => void;
    onToggleApproved: (book: Book) => void;
    onRescan: (bookID: number) => void;
    onEdit: (book: Book) => void;
}

/** coverPath mirrors the sanitising the poster route expects. */
const coverPath = (value: string) => value.replaceAll('.', '-').replace(/^\/+/, '');

const BookCard: React.FC<BookCardProps> = ({
    book,
    isSuperuser,
    formatDate,
    isBookConverting,
    onDownload,
    onEpubRequest,
    onMobiRequest,
    onToggleFavourite,
    onToggleApproved,
    onRescan,
    onEdit,
}) => {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const { setSearchItem } = useSearchBar();
    const { clearAuthorBook } = useAuthor();
    const [open, setOpen] = React.useState(false);

    /** goTo leaves the search box empty so the filter, not a stale query, applies. */
    const goTo = (path: string) => {
        setSearchItem('');
        clearAuthorBook();
        navigate(path);
    };

    const authors = book.authors ?? [];
    const shownAuthors = open ? authors : authors.slice(0, VISIBLE_AUTHORS);
    const hiddenAuthors = authors.length - shownAuthors.length;

    const cover = `${API_URL}/books-posters/${coverPath(book.path)}/${coverPath(book.filename)}.jpg`;

    const handleFormat = (id: string) => {
        if (id === 'epub') return onEpubRequest(book.id);
        if (id === 'mobi') return onMobiRequest(book.id);
        return onDownload(id, book.id);
    };

    return (
        <article
            data-testid="book-card"
            data-state={open ? 'open' : 'collapsed'}
            onClick={(event) => {
                // Buttons and links inside the card keep their own meaning.
                if ((event.target as HTMLElement).closest('button, a')) return;
                setOpen((value) => !value);
            }}
            className="cursor-pointer rounded border border-border bg-card p-4 transition-colors hover:border-muted-foreground"
        >
            <div
                className="grid gap-4"
                style={{ gridTemplateColumns: `${COVER_WIDTH}px minmax(0, 1fr)` }}
            >
                <div className="flex flex-col gap-2">
                    <img
                        src={cover}
                        alt={book.title}
                        loading="lazy"
                        style={{ width: COVER_WIDTH }}
                        className="h-[150px] flex-none rounded-sm bg-muted object-cover"
                    />
                    {/* Two equal cells with the label centred in each, so the four
                        read as an even block rather than four links of differing
                        length. */}
                    <div className="grid grid-cols-2 gap-1">
                        {DOWNLOAD_FORMATS.map((format) => {
                            const converting = isBookConverting(book.id, format.id);
                            return (
                                <button
                                    key={format.id}
                                    type="button"
                                    disabled={converting}
                                    onClick={() => handleFormat(format.id)}
                                    className={cn(
                                        'rounded py-0.5 text-center text-[12px] text-primary hover:bg-accent hover:underline',
                                        'disabled:cursor-default disabled:text-muted-foreground disabled:no-underline disabled:hover:bg-transparent',
                                    )}
                                >
                                    {converting && <span className="mr-0.5 inline-block animate-spin">◌</span>}
                                    {format.label}
                                </button>
                            );
                        })}
                    </div>
                </div>

                <div className="flex min-w-0 flex-col gap-1.5">
                    <h3 className="text-lg font-medium text-balance">{book.title}</h3>

                    <div className="flex flex-wrap gap-x-3.5 gap-y-1 text-[13px] text-muted-foreground">
                        <span>
                            {t('bookAdded')} {formatDate(book.registerdate)}
                        </span>
                        <span>
                            {t('bookPublished')} {formatDate(book.docdate)}
                        </span>
                    </div>

                    {authors.length > 0 && (
                        <div className="flex flex-wrap items-baseline gap-1.5 text-sm">
                            <span className="text-xs uppercase tracking-wide text-muted-foreground">
                                {t('authors')}
                            </span>
                            {shownAuthors.map((author, index) => (
                                <React.Fragment key={author.id}>
                                    {index > 0 && <span className="text-muted-foreground">·</span>}
                                    <button
                                        type="button"
                                        onClick={() => goTo(`/books/find/author/${author.id}/1`)}
                                        className="border-b border-border hover:border-current"
                                    >
                                        {author.full_name}
                                    </button>
                                </React.Fragment>
                            ))}
                            {hiddenAuthors > 0 && (
                                <span className="text-muted-foreground">· {t('andMore', { count: hiddenAuthors })}</span>
                            )}
                        </div>
                    )}

                    {book.series && book.series.length > 0 && (
                        <div className="flex flex-wrap items-baseline gap-1.5 text-sm">
                            <span className="text-xs uppercase tracking-wide text-muted-foreground">
                                {t('categories')}
                            </span>
                            {book.series.map((series, index) => (
                                <React.Fragment key={series.id ?? series.ser}>
                                    {index > 0 && <span className="text-muted-foreground">·</span>}
                                    <button
                                        type="button"
                                        onClick={() => goTo(`/books/find/category/${series.id}/1`)}
                                        className="border-b border-border hover:border-current"
                                    >
                                        {series.ser}
                                    </button>
                                </React.Fragment>
                            ))}
                        </div>
                    )}

                    {book.genres && book.genres.length > 0 && (
                        <div className="flex flex-wrap items-baseline gap-1.5 text-sm">
                            <span className="text-xs uppercase tracking-wide text-muted-foreground">
                                {t('genres')}
                            </span>
                            {book.genres.map((genre, index) => (
                                <React.Fragment key={genre.id}>
                                    {index > 0 && <span className="text-muted-foreground">·</span>}
                                    <button
                                        type="button"
                                        onClick={() => goTo(`/books/find/genre/${genre.id}/1`)}
                                        className="border-b border-border hover:border-current"
                                    >
                                        {t(genre.genre, genre.genre)}
                                    </button>
                                </React.Fragment>
                            ))}
                        </div>
                    )}

                    {book.annotation ? (
                        <Expandable open={open} peekLines={2} className="text-sm text-muted-foreground">
                            {book.annotation}
                        </Expandable>
                    ) : (
                        <p className="text-sm italic text-muted-foreground">{t('noAnnotation')}</p>
                    )}
                </div>

            </div>

            <div className="mt-2 flex items-center justify-end gap-0.5 border-t border-border pt-2.5">
                {isSuperuser && (
                    <>
                        <Button
                            variant="ghost"
                            size="icon"
                            title={t('rescanBook')}
                            aria-label={t('rescanBook')}
                            onClick={() => onRescan(book.id)}
                        >
                            <RefreshCw className="size-4" />
                        </Button>
                        <Button
                            variant="ghost"
                            size="icon"
                            title={t('editBook')}
                            aria-label={t('editBook')}
                            onClick={() => onEdit(book)}
                        >
                            <Pencil className="size-4" />
                        </Button>
                        <Button
                            variant="ghost"
                            size="icon"
                            title={book.approved ? t('bookApproved') : t('bookNotApproved')}
                            aria-label={book.approved ? t('bookApproved') : t('bookNotApproved')}
                            aria-pressed={book.approved}
                            onClick={() => onToggleApproved(book)}
                            className={book.approved ? 'text-green-600 dark:text-green-400' : undefined}
                        >
                            <Check className="size-4" />
                        </Button>
                    </>
                )}
                <Button
                    variant="ghost"
                    size="icon"
                    title={book.fav ? t('bookFavRemove') : t('bookFavAdd')}
                    aria-label={book.fav ? t('bookFavRemove') : t('bookFavAdd')}
                    aria-pressed={book.fav}
                    onClick={() => onToggleFavourite(book)}
                    className={book.fav ? 'text-amber-500' : undefined}
                >
                    <Star className={cn('size-4', book.fav && 'fill-current')} />
                </Button>
            </div>
        </article>
    );
};

export default BookCard;
