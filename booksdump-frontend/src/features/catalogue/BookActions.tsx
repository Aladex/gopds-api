import React from 'react';
import { useTranslation } from 'react-i18next';
import { BookOpen, Download, Loader2, Send } from 'lucide-react';

import type { Book } from '@/api/books';
import type { BookCardProps } from '@/features/catalogue/BookCard';
import { Button } from '@/shared/ui/button';
import { BouncingDots } from '@/shared/ui/bouncing-dots';
import ListSheet, { LIST_SHEET_ROW_CLASS } from '@/shared/components/ListSheet';
import {
    KindleButton,
    ROW_ACTION_CLASS,
    RowActionContent,
    TelegramButton,
    type SendButtonVariant,
} from '@/features/kindle/SendButtons';
import { cn } from '@/shared/lib/utils';

/**
 * BookActions is what a reader can do with a book: read it, download it,
 * send it somewhere.
 *
 * A wide card gives it a rail of its own beside the text: a solid «Читать
 * онлайн», the four formats in a 2×2 grid, and the send buttons under a
 * hairline, Kindle and Telegram side by side. A phone has a row of three
 * verbs under the text instead — «Читать», «Скачать» and «Отправить» — and
 * the formats and the send targets open in bottom sheets.
 *
 * The send rule is the same at both sizes, and lives here once: a target the
 * reader switched off in the profile is not offered. With neither on there is
 * no send block in the rail and no third button on the row; with one on the
 * button names it and sends with one tap; with both on the phone's
 * «Отправить» opens a sheet of the two.
 *
 * Every book is an FB2 — the other formats are converted from it — so the
 * reader is offered on every card.
 */

export type BookActionsProps = Pick<
    BookCardProps,
    | 'book'
    | 'isWide'
    | 'isBookConverting'
    | 'onDownload'
    | 'onPreview'
    | 'onEpubRequest'
    | 'onMobiRequest'
    | 'showKindleButton'
    | 'showTelegramButton'
    | 'kindleConfigured'
    | 'isSendingToKindle'
    | 'onSendToKindle'
    | 'telegramConnected'
    | 'isSendingToTelegram'
    | 'onSendToTelegram'
> & { className?: string };

/** The four ways to take the book away, in the order both sizes list them. */
const FORMATS = [
    { id: 'fb2', label: 'FB2' },
    { id: 'epub', label: 'EPUB' },
    { id: 'mobi', label: 'MOBI' },
    { id: 'zip', label: 'ZIP' },
] as const;

type Sheet = 'download' | 'send' | null;

const BookActions: React.FC<BookActionsProps> = ({
    book,
    isWide,
    isBookConverting,
    onDownload,
    onPreview,
    onEpubRequest,
    onMobiRequest,
    showKindleButton = false,
    showTelegramButton = false,
    kindleConfigured = false,
    isSendingToKindle = false,
    onSendToKindle,
    telegramConnected = false,
    isSendingToTelegram = false,
    onSendToTelegram,
    className,
}) => {
    const { t } = useTranslation();
    const [sheet, setSheet] = React.useState<Sheet>(null);
    const headingID = React.useId();

    const handleFormat = (id: string) => {
        if (id === 'epub') return onEpubRequest(book.id);
        if (id === 'mobi') return onMobiRequest(book.id);
        return onDownload(id, book.id);
    };

    const targets = Number(showKindleButton) + Number(showTelegramButton);

    /** The send buttons the reader left on, drawn for one place on the card. */
    const sends = (variant: SendButtonVariant, after?: () => void) => {
        // Two in the rail share its row, and each says its state under its name.
        const compact = variant === 'rail' && targets === 2;
        const press = (send?: (book: Book) => void) => () => {
            after?.();
            send?.(book);
        };
        return [
            showKindleButton && (
                <KindleButton
                    key="kindle"
                    variant={variant}
                    compact={compact}
                    configured={kindleConfigured}
                    sending={isSendingToKindle}
                    onClick={press(onSendToKindle)}
                />
            ),
            showTelegramButton && (
                <TelegramButton
                    key="telegram"
                    variant={variant}
                    compact={compact}
                    connected={telegramConnected}
                    sending={isSendingToTelegram}
                    onClick={press(onSendToTelegram)}
                />
            ),
        ].filter(Boolean);
    };

    if (isWide) {
        const rail = sends('rail');
        return (
            <div
                data-testid="book-actions"
                data-layout="rail"
                className={cn(
                    'flex flex-col gap-3 rounded-lg border border-border bg-secondary p-3.5',
                    className,
                )}
            >
                <Button
                    onClick={() => onPreview(book)}
                    className="h-[38px] w-full font-semibold shadow-xs"
                >
                    <BookOpen className="size-4" />
                    {t('bookReadOnline')}
                </Button>

                <div role="group" aria-labelledby={headingID}>
                    <p
                        id={headingID}
                        className="mb-1.5 text-xs font-semibold text-muted-foreground"
                    >
                        {t('bookDownload')}
                    </p>
                    <div className="grid grid-cols-2 gap-1.5">
                        {FORMATS.map((format) => {
                            const converting = isBookConverting(book.id, format.id);
                            return (
                                <Button
                                    key={format.id}
                                    variant="outline"
                                    size="sm"
                                    disabled={converting}
                                    onClick={() => handleFormat(format.id)}
                                    className="h-8 gap-1.5 bg-card text-xs font-semibold dark:bg-card"
                                >
                                    {converting ? (
                                        <BouncingDots />
                                    ) : (
                                        <Download aria-hidden="true" className="size-3.5" />
                                    )}
                                    {format.label}
                                </Button>
                            );
                        })}
                    </div>
                </div>

                {rail.length > 0 && (
                    <div
                        data-testid="book-send"
                        className={cn(
                            'grid gap-1.5 border-t border-border pt-2.5',
                            rail.length === 2 && 'grid-cols-2',
                        )}
                    >
                        {rail}
                    </div>
                )}
            </div>
        );
    }

    const row = sends('row');
    const sending = isSendingToKindle || isSendingToTelegram;

    return (
        <>
            <div
                data-testid="book-actions"
                data-layout="row"
                className={cn('grid auto-cols-fr grid-flow-col gap-2', className)}
            >
                <Button
                    size="lg"
                    onClick={() => onPreview(book)}
                    className={cn(ROW_ACTION_CLASS, 'font-semibold')}
                >
                    <RowActionContent
                        icon={<BookOpen className="size-4" />}
                        label={t('bookRead')}
                    />
                </Button>
                <Button
                    variant="outline"
                    size="lg"
                    aria-haspopup="dialog"
                    onClick={() => setSheet('download')}
                    className={cn(ROW_ACTION_CLASS, 'text-primary')}
                >
                    <RowActionContent
                        icon={<Download className="size-4" />}
                        label={t('bookDownload')}
                    />
                </Button>
                {row.length === 1 && row}
                {row.length === 2 && (
                    <Button
                        variant="outline"
                        size="lg"
                        aria-haspopup="dialog"
                        aria-busy={sending || undefined}
                        onClick={() => setSheet('send')}
                        className={ROW_ACTION_CLASS}
                    >
                        <RowActionContent
                            icon={
                                sending ? (
                                    <Loader2 aria-hidden="true" className="size-4 animate-spin" />
                                ) : (
                                    <Send className="size-4" />
                                )
                            }
                            label={t('bookSend')}
                        />
                    </Button>
                )}
            </div>

            <ListSheet
                open={sheet === 'download'}
                onOpenChange={(open) => setSheet(open ? 'download' : null)}
                title={t('bookDownload')}
                description={book.title}
                data-testid="book-download-sheet"
            >
                <ul className="divide-y divide-border">
                    {FORMATS.map((format) => {
                        const converting = isBookConverting(book.id, format.id);
                        return (
                            <li key={format.id}>
                                {/* Named by the format alone; the hint beside it
                                    is the row's description. A choice closes the
                                    sheet: a conversion has its own backdrop. */}
                                <button
                                    type="button"
                                    aria-label={format.label}
                                    disabled={converting}
                                    onClick={() => {
                                        setSheet(null);
                                        handleFormat(format.id);
                                    }}
                                    className={cn(LIST_SHEET_ROW_CLASS, 'min-h-14')}
                                >
                                    <span className="w-14 flex-none font-semibold text-primary">
                                        {format.label}
                                    </span>
                                    <span className="flex items-center gap-1 text-sm text-muted-foreground">
                                        {/* While it is being made the row says only
                                            that: beside the hint it ran to three
                                            lines on a 320px phone. */}
                                        {converting ? (
                                            <>
                                                <BouncingDots />
                                                <span>{t('bookConverting')}</span>
                                            </>
                                        ) : (
                                            t(`bookFormatHint.${format.id}`)
                                        )}
                                    </span>
                                    {!converting && (
                                        <Download
                                            aria-hidden="true"
                                            className="ml-auto size-4 flex-none text-muted-foreground"
                                        />
                                    )}
                                </button>
                            </li>
                        );
                    })}
                </ul>
            </ListSheet>

            {row.length === 2 && (
                <ListSheet
                    open={sheet === 'send'}
                    onOpenChange={(open) => setSheet(open ? 'send' : null)}
                    title={t('bookSend')}
                    description={book.title}
                    data-testid="book-send-sheet"
                >
                    <ul className="divide-y divide-border">
                        {sends('sheet', () => setSheet(null)).map((button, index) => (
                            <li key={index}>{button}</li>
                        ))}
                    </ul>
                </ListSheet>
            )}
        </>
    );
};

export default BookActions;
