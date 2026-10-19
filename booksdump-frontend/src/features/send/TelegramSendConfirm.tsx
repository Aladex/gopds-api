import React from 'react';
import { useTranslation } from 'react-i18next';

import type { Book } from '@/api/books';
import SendConfirmSheet, { SendTarget } from '@/features/send/SendConfirmSheet';

type TelegramSendConfirmProps = {
    open: boolean;
    /** The book asked about; still the last one while the surface closes. */
    book: Book | null;
    /** The connected bot, without the @. */
    botUsername: string;
    /** The format the book goes in. */
    format: string;
    onConfirm: () => void;
    onCancel: () => void;
};

/**
 * TelegramSendConfirm asks before a book is sent into the reader's Telegram:
 * which book, to which bot, in which format. The question is the Kindle one's.
 */
const TelegramSendConfirm: React.FC<TelegramSendConfirmProps> = ({
    open,
    book,
    botUsername,
    format,
    onConfirm,
    onCancel,
}) => {
    const { t } = useTranslation();
    return (
        <SendConfirmSheet
            data-testid="telegram-confirm"
            open={open}
            book={book}
            title={t('sendToTelegram.confirmTitle')}
            confirmLabel={t('sendToTelegram.confirm')}
            onConfirm={onConfirm}
            onCancel={onCancel}
        >
            {/* One group: where and in what, read together as Kindle's one line is. */}
            <div className="space-y-1">
                <SendTarget
                    label={t('sendToTelegram.confirmTo')}
                    value={`@${botUsername}`}
                    data-testid="telegram-confirm-bot"
                />
                <SendTarget
                    label={t('sendToTelegram.confirmFormat')}
                    value={format.toUpperCase()}
                    data-testid="telegram-confirm-format"
                />
            </div>
        </SendConfirmSheet>
    );
};

export default TelegramSendConfirm;
