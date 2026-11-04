import React from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { ChevronDown, Star } from 'lucide-react';

import type { AuthorDisplay, Book } from '@/api/books';
import { Button } from '@/shared/ui/button';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/shared/ui/tooltip';
import BookCover from '@/features/catalogue/BookCover';
import BookAdminMenu from '@/features/catalogue/BookAdminMenu';
import AuthorByline from '@/features/catalogue/AuthorByline';
import GenreChips from '@/features/catalogue/GenreChips';
import BookActions from '@/features/catalogue/BookActions';
import { isClippedBlock, isClippedLine, useMeasured } from '@/features/catalogue/hooks/useMeasured';
import { cn } from '@/shared/lib/utils';
import { formatCalendarDay } from '@/shared/lib/formatDate';
import { getLanguageDisplaySafe } from '@/shared/lib/languageUtils';

import { API_URL } from '@/api/config';
import { useAuthor } from '@/context/AuthorContext';
import { useSearchBar } from '@/context/SearchBarContext';

/**
 * BookCard renders one row of the catalogue.
 *
 * One card, two arrangements of the same parts. A wide card is three zones:
 * the cover, the bibliographic core, and an action rail. Below that the title
 * block sits beside a smaller cover, the genres and annotation take the
 * card's full width, and a row of three verbs closes the card.
 *
 * The core reads in one order at both sizes: the title, «by» its authors, the
 * series, the dates, the genres, a colophon, the annotation. Shut, the card
 * shows the annotation's first three lines and one row of genres, and a phone
 * leaves the colophon out; «Подробнее» opens the rest, and it is offered only
 * when something actually was left out.
 */

/** Two names and the counter, or the full author line, from what the list sends. */
const authorLine = (book: Book): AuthorDisplay[] =>
    book.authors_display ??
    (book.authors ?? []).map((author) => ({ name: author.full_name, legacy_author_id: author.id }));

export interface BookCardProps {
    book: Book;
    /**
     * Whether the card has room for the wide layout.
     *
     * Taken from the list rather than measured here, because twenty cards
     * asking the same question of the browser is twenty listeners where one
     * will do. The list asks it with CARD_WIDE_QUERY, the same number the
     * `card:` classes below use — see shared/layout/breakpoints for why the
     * action area and the authors list are still decided in JS.
     */
    isWide: boolean;
    /**
     * Whether the book's own language is worth saying.
     *
     * While the catalogue is filtered to one language every card would carry
     * the same word, which is not information. It becomes information the
     * moment the reader asks for the whole library.
     */
    showLanguage: boolean;
    isSuperuser: boolean;
    /** Formats the date the book was added; the published date is a day, shown as one. */
    formatDate: (value: string) => string;
    isBookConverting: (bookID: number, format: string) => boolean;
    onDownload: (format: string, bookID: number) => void;
    /** Opens the in-place reader. */
    onPreview: (book: Book) => void;
    onEpubRequest: (bookID: number) => void;
    onMobiRequest: (bookID: number) => void;
    onToggleFavourite: (book: Book) => void;
    onToggleApproved: (book: Book) => void;
    onRescan: (bookID: number) => void;
    onEdit: (book: Book) => void;
    /**
     * The two send buttons, each shown unless the reader turned it off in the
     * profile. Optional, and off when left out, so a card rendered without a
     * reader behind it shows neither.
     */
    showKindleButton?: boolean;
    showTelegramButton?: boolean;
    /** Whether the reader has a Kindle address to send to. */
    kindleConfigured?: boolean;
    isSendingToKindle?: boolean;
    /** Asks to send the book, or opens the setup when there is no Kindle yet. */
    onSendToKindle?: (book: Book) => void;
    /** Whether the reader's Telegram bot is connected and may write to them. */
    telegramConnected?: boolean;
    isSendingToTelegram?: boolean;
    /** Asks to send the book, or opens the Telegram tab when it cannot be sent. */
    onSendToTelegram?: (book: Book) => void;
}

/** coverPath mirrors the sanitising the poster route expects. */
const coverPath = (value: string) => value.replaceAll('.', '-').replace(/^\/+/, '');

/**
 * FavouriteStar sits on the cover's top-right corner at both sizes.
 *
 * On the cover, «in favourites» is visible as an amber disc while scanning a
 * list: it is a property of the book. The dark scrim makes the star readable
 * on any cover and on the typographic tile, and the hit area reaches past the
 * 28px disc to a finger's 40px. One name in both states, as a toggle's name
 * should be: the pressed state says which, and the hint what a press does.
 */
const FavouriteStar: React.FC<{ book: Book; onToggle: (book: Book) => void }> = ({
    book,
    onToggle,
}) => {
    const { t } = useTranslation();
    return (
        <TooltipProvider>
            <Tooltip>
                <TooltipTrigger asChild>
                    <Button
                        variant="ghost"
                        size="icon-xs"
                        aria-pressed={book.fav}
                        aria-label={t('bookFav')}
                        onClick={() => onToggle(book)}
                        className={cn(
                            'absolute top-1 right-1 size-7 rounded-full text-white card:size-[30px]',
                            "before:absolute before:-inset-1.5 before:content-['']",
                            book.fav
                                ? 'bg-amber-500 hover:bg-amber-500/90 hover:text-white dark:hover:bg-amber-500/90'
                                : 'bg-black/45 hover:bg-black/60 hover:text-white dark:hover:bg-black/60',
                        )}
                    >
                        <Star className={cn('size-[15px]', book.fav && 'fill-current')} />
                    </Button>
                </TooltipTrigger>
                <TooltipContent>{book.fav ? t('bookFavRemove') : t('bookFavAdd')}</TooltipContent>
            </Tooltip>
        </TooltipProvider>
    );
};

const BookCard: React.FC<BookCardProps> = ({
    book,
    isWide,
    showLanguage,
    isSuperuser,
    formatDate,
    isBookConverting,
    onDownload,
    onPreview,
    onEpubRequest,
    onMobiRequest,
    onToggleFavourite,
    onToggleApproved,
    onRescan,
    onEdit,
    showKindleButton = false,
    showTelegramButton = false,
    kindleConfigured = false,
    isSendingToKindle = false,
    onSendToKindle,
    telegramConnected = false,
    isSendingToTelegram = false,
    onSendToTelegram,
}) => {
    const { t } = useTranslation();
    const { setSearchItem } = useSearchBar();
    const { setAuthorName } = useAuthor();
    const [open, setOpen] = React.useState(false);
    const [chipsHidden, setChipsHidden] = React.useState(0);

    /** Names the block the control opens, for anything reading the page aloud. */
    const detailsID = `book-${book.id}-details`;

    /** leaveScope empties the search box so the filter, not a stale query, applies. */
    const leaveScope = () => setSearchItem('');

    // A code the interface cannot name is not shown at all: "zxx" tells a
    // reader less than the empty space where it would have gone.
    const bookLanguage = getLanguageDisplaySafe(book.lang);
    const authors = authorLine(book);
    const published = formatCalendarDay(book.docdate);
    const genres = book.genres ?? [];

    // Publisher and ISBNs as the source file states them, and only when it
    // does. Plain text, not links: nothing in the catalogue is filtered by them.
    const colophon = [
        book.publisher,
        book.isbn && book.isbn.length > 0 ? `${t('bookIsbn')} ${book.isbn.join(', ')}` : null,
    ]
        .filter(Boolean)
        .join(' · ');
    // A wide card always has room for the line, cut if it must be; a phone
    // keeps it for the opened card.
    const showColophon = colophon !== '' && (isWide || open);

    const [annotationRef, annotationClipped] = useMeasured<HTMLParagraphElement, boolean>(
        isClippedBlock,
        false,
        !open,
    );
    const [colophonRef, colophonClipped] = useMeasured<HTMLParagraphElement, boolean>(
        isClippedLine,
        false,
        isWide && !open,
    );

    // «Подробнее» only when the shut card is holding something back. Once
    // open it stays, as the way to shut it again.
    const holdingBack =
        annotationClipped || chipsHidden > 0 || (isWide ? colophonClipped : colophon !== '');
    const offerMore = open || holdingBack;

    const hasDetails = genres.length > 0 || showColophon || Boolean(book.annotation) || offerMore;

    const cover = `${API_URL}/books-posters/${coverPath(book.path)}/${coverPath(book.filename)}.jpg`;

    return (
        <article
            data-testid="book-card"
            data-state={open ? 'open' : 'collapsed'}
            className="relative rounded-lg border border-border bg-card p-4 transition-colors hover:border-muted-foreground card:p-5"
        >
            {/*
              One grid, two arrangements. Wide, the cover and the rail each span
              both rows: the first row (the title block) keeps its content
              height and the second takes whatever the cover or the rail
              leave, so spare height lands below the text and not inside the
              title row. Narrow, only the title block sits beside the cover,
              and the rest drops below it to the card's full width: at 320px a
              cover beside the whole text leaves the annotation a word a line.
            */}
            <div className="grid grid-cols-[80px_minmax(0,1fr)] gap-x-3.5 gap-y-3 card:grid-cols-[108px_minmax(0,1fr)_240px] card:grid-rows-[auto_1fr] card:gap-x-6 card:gap-y-2">
                <div className="col-start-1 row-start-1 flex flex-col items-center gap-1.5 card:row-span-2 card:gap-2">
                    <BookCover
                        src={cover}
                        alt={book.title}
                        hasPicture={book.cover}
                        title={book.title}
                        author={authors[0]?.name}
                        className="h-[116px] w-20 flex-none rounded shadow-[0_2px_5px_rgba(0,0,0,0.12)] card:h-[156px] card:w-[108px]"
                    >
                        <FavouriteStar book={book} onToggle={onToggleFavourite} />
                    </BookCover>
                    {/* Only when the list is mixed: it answers "can I read this
                        one?", which nothing else on the card does. */}
                    {showLanguage && bookLanguage && (
                        <span className="rounded-sm bg-secondary px-1.5 text-[11px] leading-[18px] font-semibold text-muted-foreground uppercase">
                            <span aria-hidden="true">{book.lang}</span>
                            <span className="sr-only">{bookLanguage}</span>
                        </span>
                    )}
                </div>

                <div className="col-start-2 row-start-1 flex min-w-0 flex-col gap-1">
                    {/*
                      The administrator's menu ends the title row at both sizes:
                      never in the rail, never on the cover where the star is,
                      and out of the buttons a reader presses every day. After
                      the title in the source, so it is read and tabbed to after
                      it.
                    */}
                    <div className="flex items-start gap-1">
                        <h3 className="min-w-0 flex-1 text-[17px] leading-[1.3] font-semibold text-balance [overflow-wrap:anywhere] card:text-[19px] card:leading-[1.35]">
                            {book.title}
                        </h3>
                        {isSuperuser && (
                            <div className="-my-2 -mr-2 flex-none">
                                <BookAdminMenu
                                    book={book}
                                    onRescan={onRescan}
                                    onEdit={onEdit}
                                    onToggleApproved={onToggleApproved}
                                />
                            </div>
                        )}
                    </div>

                    <AuthorByline
                        authors={authors}
                        isWide={isWide}
                        bookTitle={book.title}
                        onGo={(scopeName) => {
                            leaveScope();
                            // The name is on screen already, so the search panel
                            // need not fetch it to say whose books it is scoped to.
                            setAuthorName(scopeName);
                        }}
                    />

                    {book.series && book.series.length > 0 && (
                        <div className="text-[13px] leading-snug text-muted-foreground">
                            {t('bookSeriesLabel')}:{' '}
                            {book.series.map((series, index) => (
                                <React.Fragment key={series.id ?? series.ser}>
                                    {index > 0 && ', '}
                                    <Link
                                        to={`/books/find/category/${series.id}/1`}
                                        onClick={leaveScope}
                                        className="border-b border-border text-foreground hover:border-current"
                                    >
                                        {series.ser}
                                    </Link>
                                </React.Fragment>
                            ))}
                        </div>
                    )}

                    <div className="mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5 text-[12.5px] leading-snug text-muted-foreground">
                        <span>
                            {t('bookAdded')} {formatDate(book.registerdate)}
                        </span>
                        {published && (
                            <span>
                                {t('bookPublished')} {published}
                            </span>
                        )}
                    </div>
                </div>

                {hasDetails && (
                    <div
                        id={detailsID}
                        className="col-span-2 row-start-2 flex min-w-0 flex-col gap-2 card:col-span-1 card:col-start-2"
                    >
                        {genres.length > 0 && (
                            <GenreChips
                                genres={genres}
                                open={open}
                                onOpen={() => setOpen(true)}
                                onGo={leaveScope}
                                onHiddenChange={setChipsHidden}
                            />
                        )}

                        {showColophon && (
                            <p
                                ref={colophonRef}
                                data-testid="book-colophon"
                                data-clip="colophon"
                                className={cn(
                                    'text-[12.5px] leading-normal text-muted-foreground',
                                    !open && 'truncate',
                                )}
                            >
                                {colophon}
                            </p>
                        )}

                        {/* Three lines shut at both sizes; open, it flows in the
                            card rather than in a box of its own that scrolls
                            inside the page. */}
                        {book.annotation && (
                            <p
                                ref={annotationRef}
                                data-clip="annotation"
                                className={cn(
                                    'text-sm leading-normal whitespace-pre-line text-muted-foreground card:text-[13.5px] card:leading-[1.55]',
                                    !open && 'line-clamp-3',
                                )}
                            >
                                {book.annotation}
                            </p>
                        )}

                        {/*
                          The one way in and out. The card used to open on a click
                          anywhere on it, which was never announced and could not
                          be reached from a keyboard; and selecting a line of the
                          annotation with the mouse shut it, taking the text with
                          it.
                        */}
                        {offerMore && (
                            <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => setOpen((value) => !value)}
                                aria-expanded={open}
                                aria-controls={detailsID}
                                className="-my-1 -ml-2 h-10 gap-1 self-start px-2 font-semibold text-primary has-[>svg]:px-2 card:h-8"
                            >
                                <ChevronDown
                                    aria-hidden="true"
                                    className={cn(
                                        'size-4 transition-transform duration-200 motion-reduce:transition-none',
                                        open && 'rotate-180',
                                    )}
                                />
                                {open ? t('bookLess') : t('bookMore')}
                            </Button>
                        )}
                    </div>
                )}

                <BookActions
                    book={book}
                    isWide={isWide}
                    isBookConverting={isBookConverting}
                    onDownload={onDownload}
                    onPreview={onPreview}
                    onEpubRequest={onEpubRequest}
                    onMobiRequest={onMobiRequest}
                    showKindleButton={showKindleButton}
                    showTelegramButton={showTelegramButton}
                    kindleConfigured={kindleConfigured}
                    isSendingToKindle={isSendingToKindle}
                    onSendToKindle={onSendToKindle}
                    telegramConnected={telegramConnected}
                    isSendingToTelegram={isSendingToTelegram}
                    onSendToTelegram={onSendToTelegram}
                    className={
                        isWide ? 'col-start-3 row-span-2 row-start-1 self-start' : 'col-span-2'
                    }
                />
            </div>
        </article>
    );
};

export default BookCard;
