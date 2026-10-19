import React, { useRef } from 'react';
import { useTranslation } from 'react-i18next';

import type { Book } from '@/api/books';
import { Button } from '@/shared/ui/button';
import ResponsiveSheet from '@/shared/components/ResponsiveSheet';

type SendConfirmSheetProps = {
    open: boolean;
    /** The book asked about; still the last one while the surface closes. */
    book: Book | null;
    /** The question, the word of the main action, and what goes where. */
    title: string;
    confirmLabel: string;
    /** Where the book goes, as lines under the book. */
    children: React.ReactNode;
    onConfirm: () => void;
    onCancel: () => void;
    'data-testid'?: string;
};

/** The book's authors as one line, from whichever list the catalogue sent. */
export const authorsOf = (book: Book) =>
    (
        book.authors_display?.map((author) => author.name) ??
        book.authors?.map((author) => author.full_name) ??
        []
    ).join(', ');

/**
 * SendConfirmSheet asks before a book is sent somewhere it cannot be taken
 * back from: which book, and where to. The Kindle and Telegram questions are
 * this one with their own words.
 *
 * The shell is the Kindle setup's — a dialog on a desktop, a sheet from the
 * bottom edge on a phone — with the details scrolling between the title and
 * the buttons: a collection with twenty authors still leaves Send and Cancel
 * on a 640px-high screen. «Send» takes the
 * focus when it opens, so Enter sends and Escape cancels; closing gives the
 * focus back to the card's button, which is where the reader was.
 */
const SendConfirmSheet: React.FC<SendConfirmSheetProps> = ({
    open,
    book,
    title,
    confirmLabel,
    children,
    onConfirm,
    onCancel,
    'data-testid': testId,
}) => {
    const { t } = useTranslation();
    const sendRef = useRef<HTMLButtonElement>(null);

    // Radix gives the focus back to the dialog's own trigger, and this one is
    // opened from a card instead: without this it fell to the page's body.
    const returnTo = useRef<HTMLElement | null>(null);
    const focusSend = (event: Event) => {
        event.preventDefault();
        returnTo.current =
            document.activeElement instanceof HTMLElement ? document.activeElement : null;
        sendRef.current?.focus();
    };
    const focusCard = (event: Event) => {
        event.preventDefault();
        returnTo.current?.focus();
        returnTo.current = null;
    };
    const onOpenChange = (next: boolean) => {
        if (!next) onCancel();
    };

    const authors = book ? authorsOf(book) : '';
    const details = book && (
        <>
            <span className="block font-medium break-words text-foreground">{book.title}</span>
            {authors && <span className="block break-words">{authors}</span>}
        </>
    );
    const send = (
        <Button ref={sendRef} onClick={onConfirm}>
            {confirmLabel}
        </Button>
    );
    const cancel = (
        <Button variant="ghost" onClick={onCancel}>
            {t('cancel')}
        </Button>
    );

    return (
        <ResponsiveSheet
            data-testid={testId}
            open={open}
            onOpenChange={onOpenChange}
            title={title}
            description={details}
            closeLabel={t('close')}
            onOpenAutoFocus={focusSend}
            onCloseAutoFocus={focusCard}
            primary={send}
            secondary={cancel}
        >
            {children}
        </ResponsiveSheet>
    );
};

/** SendTarget is one «where to» line of the question: a word, then the place. */
export const SendTarget: React.FC<{ label: string; value: string; 'data-testid'?: string }> = ({
    label,
    value,
    'data-testid': testId,
}) => (
    // Plain text: an address to read over, not to copy.
    <p className="text-sm">
        <span className="text-muted-foreground">{label} </span>
        <span data-testid={testId} className="break-all">
            {value}
        </span>
    </p>
);

export default SendConfirmSheet;
