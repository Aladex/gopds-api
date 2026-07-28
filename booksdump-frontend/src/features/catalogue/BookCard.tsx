import React from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { Check, Pencil, RefreshCw, Star } from 'lucide-react';

import type { Book } from '@/api/books';
import { Button } from '@/shared/ui/button';
import { BouncingDots } from '@/shared/ui/bouncing-dots';
import { Expandable } from '@/shared/ui/expandable';
import BookCover from '@/features/catalogue/BookCover';
import { cn } from '@/shared/lib/utils';
import { getLanguageDisplaySafe } from '@/shared/lib/languageUtils';

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

const VISIBLE_AUTHORS = 2;

export interface BookCardProps {
    book: Book;
    /**
     * Whether the book's own language is worth saying.
     *
     * While the catalogue is filtered to one language every card would carry
     * the same word, which is not information. It becomes information the
     * moment the reader asks for the whole library.
     */
    showLanguage: boolean;
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
    showLanguage,
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
    const { clearAuthorBook, setAuthorName } = useAuthor();
    const [open, setOpen] = React.useState(false);

    /** goTo leaves the search box empty so the filter, not a stale query, applies. */
    const goTo = (path: string) => {
        setSearchItem('');
        clearAuthorBook();
        navigate(path);
    };

    // A code the interface cannot name is not shown at all: "zxx" tells a
    // reader less than the empty space where it would have gone.
    const bookLanguage = getLanguageDisplaySafe(book.lang);
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
            {/*
              One grid, two arrangements. A phone has no room for a cover beside
              the whole text: at 340px that leaves 155px, in which the title
              breaks in two, every label parts from its value and the annotation
              comes to a couple of words — before a book with several authors
              makes it worse.
              
              So only the title and the dates sit beside the cover, which is what
              a reader scans first, and the metadata and annotation drop below to
              the card's full width. On a wider screen the same three blocks fall
              into the two columns they had.
            */}
            <div className="grid grid-cols-[104px_minmax(0,1fr)] gap-x-4 gap-y-3 sm:gap-y-1.5">
                <div className="contents sm:row-span-2 sm:flex sm:flex-col sm:gap-2">
                    <BookCover
                        src={cover}
                        alt={book.title}
                        className="col-start-1 row-start-1 h-[150px] w-[104px] flex-none rounded-sm"
                    />
                    {/* Two equal cells with the label centred in each, so the four
                        read as an even block rather than four links of differing
                        length. */}
                    {/* At the foot of the card on a phone, two to a row and
                        tall enough to hit: beside an 88px cover each of these
                        was 42 by 22, half the size a finger reliably lands on.
                        Beside the text on a wider screen they go back to being
                        quiet links under the cover. */}
                    <div className="col-span-2 row-start-3 grid grid-cols-2 gap-2 pt-1 sm:col-span-1 sm:gap-1 sm:pt-0">
                        {DOWNLOAD_FORMATS.map((format) => {
                            const converting = isBookConverting(book.id, format.id);
                            return (
                                <button
                                    key={format.id}
                                    type="button"
                                    disabled={converting}
                                    onClick={() => handleFormat(format.id)}
                                    className={cn(
                                        'flex min-h-9 items-center justify-center rounded border border-border text-center text-[13px] text-primary',
                                        'hover:bg-accent hover:underline',
                                        'sm:min-h-0 sm:border-0 sm:py-0.5 sm:text-[12px]',
                                        'disabled:cursor-default disabled:text-muted-foreground disabled:no-underline disabled:hover:bg-transparent',
                                    )}
                                >
                                    {converting && <BouncingDots className="mr-1" />}
                                    {format.label}
                                </button>
                            );
                        })}
                    </div>
                </div>

                <div className="col-start-2 row-start-1 flex min-w-0 flex-col gap-1.5">
                    <h3 className="text-lg font-medium text-balance">{book.title}</h3>

                    <div className="flex flex-wrap gap-x-3.5 gap-y-1 text-[13px] text-muted-foreground">
                        <span>
                            {t('bookAdded')} {formatDate(book.registerdate)}
                        </span>
                        <span>
                            {t('bookPublished')} {formatDate(book.docdate)}
                        </span>
                        {/* Last, and only when the list is mixed: it answers
                            "can I read this one?", which the dates do not. */}
                        {showLanguage && bookLanguage && <span>{bookLanguage}</span>}
                    </div>
                </div>

                <div className="col-span-2 row-start-2 flex min-w-0 flex-col gap-1.5 sm:col-span-1 sm:col-start-2">

                    {authors.length > 0 && (
                        <div className="flex flex-wrap items-baseline gap-1.5 text-sm">
                            <span className="text-xs uppercase tracking-wide text-muted-foreground">
                                {t('authors')}
                            </span>
                            {shownAuthors.map((author, index) => (
                                <span key={author.id} className="inline-flex items-baseline gap-1.5">
                                    {index > 0 && <span className="text-muted-foreground">·</span>}
                                    <button
                                        type="button"
                                        onClick={() => {
                                            // The name is on screen already, so
                                            // the search panel need not fetch it
                                            // to say whose books it is scoped to.
                                            setAuthorName(author.full_name);
                                            goTo(`/books/find/author/${author.id}/1`);
                                        }}
                                        className="border-b border-border hover:border-current"
                                    >
                                        {author.full_name}
                                    </button>
                                </span>
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
                                <span
                                    key={series.id ?? series.ser}
                                    className="inline-flex items-baseline gap-1.5"
                                >
                                    {index > 0 && <span className="text-muted-foreground">·</span>}
                                    <button
                                        type="button"
                                        onClick={() => goTo(`/books/find/category/${series.id}/1`)}
                                        className="border-b border-border hover:border-current"
                                    >
                                        {series.ser}
                                    </button>
                                </span>
                            ))}
                        </div>
                    )}

                    {book.genres && book.genres.length > 0 && (
                        <div className="flex flex-wrap items-baseline gap-1.5 text-sm">
                            <span className="text-xs uppercase tracking-wide text-muted-foreground">
                                {t('genres')}
                            </span>
                            {book.genres.map((genre, index) => (
                                <span key={genre.id} className="inline-flex items-baseline gap-1.5">
                                    {index > 0 && <span className="text-muted-foreground">·</span>}
                                    <button
                                        type="button"
                                        onClick={() => goTo(`/books/find/genre/${genre.id}/1`)}
                                        className="border-b border-border hover:border-current"
                                    >
                                        {t(genre.genre, genre.genre)}
                                    </button>
                                </span>
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
