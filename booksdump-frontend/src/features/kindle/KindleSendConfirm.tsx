import React from 'react';
import { useTranslation } from 'react-i18next';

import type { Book } from '@/api/books';
import SendConfirmSheet, { SendTarget } from '@/features/send/SendConfirmSheet';

type KindleSendConfirmProps = {
    open: boolean;
    /** The book asked about; still the last one while the surface closes. */
    book: Book | null;
    /** The reader's Kindle address. */
    address: string;
    onConfirm: () => void;
    onCancel: () => void;
};

/**
 * KindleSendConfirm asks before a book is sent to the reader's Kindle: which
 * book, and to which address.
 */
const KindleSendConfirm: React.FC<KindleSendConfirmProps> = ({
    open,
    book,
    address,
    onConfirm,
    onCancel,
}) => {
    const { t } = useTranslation();
    return (
        <SendConfirmSheet
            data-testid="kindle-confirm"
            open={open}
            book={book}
            title={t('sendToKindle.confirmTitle')}
            confirmLabel={t('sendToKindle.confirm')}
            onConfirm={onConfirm}
            onCancel={onCancel}
        >
            <SendTarget
                label={t('sendToKindle.confirmTo')}
                value={address}
                data-testid="kindle-confirm-address"
            />
        </SendConfirmSheet>
    );
};

export default KindleSendConfirm;
