import React, { useRef } from 'react';
import { useTranslation } from 'react-i18next';

import type { Book } from '@/api/books';
import { Button } from '@/shared/ui/button';
import ResponsiveSheet from '@/shared/components/ResponsiveSheet';

type KindleSendConfirmProps = {
    open: boolean;
    /** The book asked about; still the last one while the surface closes. */
    book: Book | null;
    /** The reader's Kindle address. */
    address: string;
    onConfirm: () => void;
    onCancel: () => void;
};

/** The book's authors as one line, from whichever list the catalogue sent. */
const authorsOf = (book: Book) =>
    (
        book.authors_display?.map((author) => author.name) ??
        book.authors?.map((author) => author.full_name) ??
        []
    ).join(', ');

/**
 * KindleSendConfirm asks before a book is sent to the reader's Kindle: which
 * book, and to which address.
 *
 * The shell is the Kindle setup's — a dialog on a desktop, a sheet from the
 * bottom edge on a phone — with the details scrolling between the title and
 * the buttons: a collection with twenty authors still leaves Send and Cancel
 * on a 640px-high screen. «Send» takes the
 * focus when it opens, so Enter sends and Escape cancels; closing gives the
 * focus back to the card's button, which is where the reader was.
 */
const KindleSendConfirm: React.FC<KindleSendConfirmProps> = ({
    open,
    book,
    address,
    onConfirm,
    onCancel,
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
    // Plain text: an address to read over, not to copy.
    const target = (
        <p className="text-sm">
            <span className="text-muted-foreground">{t('sendToKindle.confirmTo')} </span>
            <span data-testid="kindle-confirm-address" className="break-all">
                {address}
            </span>
        </p>
    );
    const send = (
        <Button ref={sendRef} onClick={onConfirm}>
            {t('sendToKindle.confirm')}
        </Button>
    );
    const cancel = (
        <Button variant="ghost" onClick={onCancel}>
            {t('cancel')}
        </Button>
    );

    return (
        <ResponsiveSheet
            data-testid="kindle-confirm"
            open={open}
            onOpenChange={onOpenChange}
            title={t('sendToKindle.confirmTitle')}
            description={details}
            closeLabel={t('close')}
            onOpenAutoFocus={focusSend}
            onCloseAutoFocus={focusCard}
            primary={send}
            secondary={cancel}
        >
            {target}
        </ResponsiveSheet>
    );
};

export default KindleSendConfirm;
