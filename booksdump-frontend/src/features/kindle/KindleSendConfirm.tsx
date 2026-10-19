import React, { useRef } from 'react';
import { useTranslation } from 'react-i18next';

import type { Book } from '@/api/books';
import { Button } from '@/shared/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import {
    Drawer,
    DrawerContent,
    DrawerDescription,
    DrawerFooter,
    DrawerTitle,
} from '@/shared/ui/drawer';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';

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
 * The shell is the one the Kindle setup and the donate dialog have — a dialog
 * on a desktop, a sheet from the bottom edge on a phone. «Send» takes the
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
    const isMobile = useMediaQuery('(max-width: 600px)');
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
            <span className="block font-medium text-foreground">{book.title}</span>
            {authors && <span className="block">{authors}</span>}
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

    if (isMobile) {
        return (
            <Drawer open={open} onOpenChange={onOpenChange}>
                <DrawerContent onOpenAutoFocus={focusSend} onCloseAutoFocus={focusCard}>
                    <div className="flex flex-col gap-3 px-4 pt-2">
                        <DrawerTitle className="text-base font-medium">
                            {t('sendToKindle.confirmTitle')}
                        </DrawerTitle>
                        <DrawerDescription className="text-sm text-muted-foreground">
                            {details}
                        </DrawerDescription>
                        {target}
                    </div>
                    {/* Stacked and full width, the main one first, as the
                        setup sheet has them. */}
                    <DrawerFooter className="pt-3">
                        {send}
                        {cancel}
                    </DrawerFooter>
                </DrawerContent>
            </Drawer>
        );
    }

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent
                closeLabel={t('close')}
                onOpenAutoFocus={focusSend}
                onCloseAutoFocus={focusCard}
            >
                <DialogHeader>
                    <DialogTitle>{t('sendToKindle.confirmTitle')}</DialogTitle>
                    <DialogDescription>{details}</DialogDescription>
                </DialogHeader>
                {target}
                <DialogFooter>
                    {cancel}
                    {send}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};

export default KindleSendConfirm;
