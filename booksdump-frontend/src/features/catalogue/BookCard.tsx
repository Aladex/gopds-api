import React from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { Check, ChevronDown, Pencil, RefreshCw, Star } from 'lucide-react';

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
 * annotation, and whatever of the authors, series and genres did not fit on a
 * line. A book with twelve authors or nine genres would otherwise stretch every
 * row in the list to fit the longest one.
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

/**
 * MetaRow is one line of the card's metadata: what it is, then the values.
 *
 * Shut, it shows a single line and lets the browser mark the cut. That is the
 * whole reason it is line-clamped rather than sliced in JavaScript:
 * `-webkit-line-clamp` adds the ellipsis only where it actually truncated, so a
 * book with one genre says nothing about hidden ones and a book with nine says
 * so — and nothing has to measure the row, or re-measure it as the window
 * changes width.
 *
 * The values break like ordinary text, so a first value longer than the line
 * still shows as much of itself as fits. They were unbreakable boxes to begin
 * with, on the reasoning that a cut should fall between two names rather than
 * inside one — which is true right up to the book whose only series is longer
 * than the row, where it left the label standing over an ellipsis and no
 * content at all.
 *
 * That is also why the values are links rather than buttons. A button is an
 * atomic inline box whatever its `display` says — measured: laid out as
 * `inline` it still vanishes whole — and these are navigations anyway, so they
 * gain the browser's own openings into a new tab on the way.
 *
 * The line is given more height than the text needs because each value carries
 * an underline, and a clamped line clips whatever leans out of it.
 */
const MetaRow: React.FC<React.PropsWithChildren<{ label: string; open: boolean }>> = ({
    label,
    open,
    children,
}) => (
    <div className={cn('text-sm leading-6 break-words', !open && 'line-clamp-1')}>
        <span className="mr-1.5 text-xs tracking-wide text-muted-foreground uppercase">
            {label}
        </span>
        {children}
    </div>
);

/** MetaItem is one value in such a row: where it goes, and what to forget first. */
const MetaItem: React.FC<
    React.PropsWithChildren<{ first: boolean; to: string; onGo: () => void }>
> = ({ first, to, onGo, children }) => (
    <>
        {!first && <span className="mx-1.5 text-muted-foreground">·</span>}
        <Link to={to} onClick={onGo} className="border-b border-border hover:border-current">
            {children}
        </Link>
    </>
);

export interface BookCardProps {
    book: Book;
    /**
     * How many lines of the annotation show while the card is shut.
     *
     * A phone fits about a third of the words per line that a desktop does, so
     * the same count leaves it with a sentence fragment — and with almost no
     * card left to press that is not an author or a genre.
     */
    annotationPeekLines: number;
    /**
     * Whether this is the narrow layout.
     *
     * Taken from the list rather than measured here, because the list already
     * knows — it is the same query that decides how much annotation to show —
     * and twenty cards asking the same question of the browser is twenty
     * listeners where one will do.
     */
    isMobile: boolean;
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
    annotationPeekLines,
    isMobile,
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
    const { setSearchItem } = useSearchBar();
    const { setAuthorName } = useAuthor();
    const [open, setOpen] = React.useState(false);

    /** Names the block the control opens, for anything reading the page aloud. */
    const detailsID = `book-${book.id}-details`;

    /** leaveScope empties the search box so the filter, not a stale query, applies. */
    const leaveScope = () => setSearchItem('');

    // A code the interface cannot name is not shown at all: "zxx" tells a
    // reader less than the empty space where it would have gone.
    const bookLanguage = getLanguageDisplaySafe(book.lang);
    const authors = book.authors ?? [];

    const cover = `${API_URL}/books-posters/${coverPath(book.path)}/${coverPath(book.filename)}.jpg`;

    const handleFormat = (id: string) => {
        if (id === 'epub') return onEpubRequest(book.id);
        if (id === 'mobi') return onMobiRequest(book.id);
        return onDownload(id, book.id);
    };

    /*
     * The four ways to take the book away. Two equal cells with the label
     * centred in each, so they read as an even block rather than as four links
     * of differing length.
     *
     * They sit under the cover on a wide screen, as quiet links. On a phone they
     * go below the rule instead, among the other things one can do to a book,
     * and become targets a finger can land on: beside an 88px cover each was 42
     * by 22, half the size that needs to be. They used to close the grid above
     * the rule, where the control that sits on it cut into their outlines.
     */
    const downloads = (
        <div className={cn('grid grid-cols-2', isMobile ? 'w-full gap-2' : 'gap-1')}>
            {DOWNLOAD_FORMATS.map((format) => {
                const converting = isBookConverting(book.id, format.id);
                return (
                    <button
                        key={format.id}
                        type="button"
                        disabled={converting}
                        onClick={() => handleFormat(format.id)}
                        className={cn(
                            'flex items-center justify-center rounded text-center text-primary',
                            'hover:bg-accent hover:underline',
                            isMobile
                                ? 'min-h-9 border border-border text-[13px]'
                                : 'py-0.5 text-[12px]',
                            'disabled:cursor-default disabled:text-muted-foreground disabled:no-underline disabled:hover:bg-transparent',
                        )}
                    >
                        {converting && <BouncingDots className="mr-1" />}
                        {format.label}
                    </button>
                );
            })}
        </div>
    );

    return (
        <article
            data-testid="book-card"
            data-state={open ? 'open' : 'collapsed'}
            className="rounded border border-border bg-card p-4 transition-colors hover:border-muted-foreground"
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
                    {!isMobile && downloads}
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

                <div
                    id={detailsID}
                    className="col-span-2 row-start-2 flex min-w-0 flex-col gap-1.5 sm:col-span-1 sm:col-start-2"
                >
                    {authors.length > 0 && (
                        <MetaRow label={t('authors')} open={open}>
                            {authors.map((author, index) => (
                                <MetaItem
                                    key={author.id}
                                    first={index === 0}
                                    to={`/books/find/author/${author.id}/1`}
                                    onGo={() => {
                                        leaveScope();
                                        // The name is on screen already, so the
                                        // search panel need not fetch it to say
                                        // whose books it is scoped to.
                                        setAuthorName(author.full_name);
                                    }}
                                >
                                    {author.full_name}
                                </MetaItem>
                            ))}
                        </MetaRow>
                    )}

                    {book.series && book.series.length > 0 && (
                        <MetaRow label={t('series')} open={open}>
                            {book.series.map((series, index) => (
                                <MetaItem
                                    key={series.id ?? series.ser}
                                    first={index === 0}
                                    to={`/books/find/category/${series.id}/1`}
                                    onGo={leaveScope}
                                >
                                    {series.ser}
                                </MetaItem>
                            ))}
                        </MetaRow>
                    )}

                    {book.genres && book.genres.length > 0 && (
                        <MetaRow label={t('genres')} open={open}>
                            {book.genres.map((genre, index) => (
                                <MetaItem
                                    key={genre.id}
                                    first={index === 0}
                                    to={`/books/find/genre/${genre.id}/1`}
                                    onGo={leaveScope}
                                >
                                    {t(genre.genre, genre.genre)}
                                </MetaItem>
                            ))}
                        </MetaRow>
                    )}

                    {book.annotation ? (
                        <Expandable
                            open={open}
                            peekLines={annotationPeekLines}
                            className="text-sm text-muted-foreground"
                        >
                            {book.annotation}
                        </Expandable>
                    ) : (
                        <p className="text-sm italic text-muted-foreground">{t('noAnnotation')}</p>
                    )}
                </div>
            </div>

            {/*
              The rule that separates the book from what can be done with it, with
              the way in and out sitting on it. On the line rather than in the
              flow because that is what it divides: everything above is the book
              as the list shows it, everything below is the reader acting on it,
              and opening the card is the move between the two.

              The card used to open on a click anywhere on it, which was never
              announced and could not be reached from a keyboard at all: it was an
              <article> with an onClick. It could not simply be given a role
              either — it holds links and buttons, and an interactive element
              wrapping interactive elements is flattened by the software that
              reads a page aloud. Selecting a line of the annotation with the
              mouse also counted as a click on the card and shut it, taking the
              text with it.
            */}
            {/* Roomier on a phone: the control sits astride the rule, and the
                downloads are the first thing under it, so the two touch targets
                would otherwise share an edge. */}
            <div className="relative mt-5 border-t border-border pt-8 sm:mt-3 sm:pt-2.5">
                <button
                    type="button"
                    onClick={() => setOpen((value) => !value)}
                    aria-expanded={open}
                    aria-controls={detailsID}
                    className={cn(
                        // Centred on the rule, painted in the card's own colour
                        // so the line appears to break for it rather than to run
                        // behind it. Taller where it is pressed with a finger
                        // rather than pointed at, and the offset follows so it
                        // still straddles the line.
                        'absolute left-1/2 -translate-x-1/2',
                        '-top-4 py-1.5 sm:-top-3 sm:py-0',
                        'flex items-center gap-1 rounded bg-card px-3 text-[13px] text-primary sm:px-2',
                        'hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none',
                    )}
                >
                    <ChevronDown
                        aria-hidden="true"
                        className={cn(
                            'size-4 transition-transform duration-200 motion-reduce:transition-none',
                            open && 'rotate-180',
                        )}
                    />
                    {open ? t('bookLess') : t('bookMore')}
                </button>

                {isMobile && downloads}

                <div className={cn('flex items-center justify-end gap-0.5', isMobile && 'pt-2')}>
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
                                aria-label={
                                    book.approved ? t('bookApproved') : t('bookNotApproved')
                                }
                                aria-pressed={book.approved}
                                onClick={() => onToggleApproved(book)}
                                className={
                                    book.approved ? 'text-green-600 dark:text-green-400' : undefined
                                }
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
            </div>
        </article>
    );
};

export default BookCard;
