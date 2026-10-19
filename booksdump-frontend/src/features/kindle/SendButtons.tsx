import React from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/shared/ui/tooltip';
import KindleIcon from '@/shared/icons/KindleIcon';
import TelegramIcon from '@/shared/icons/TelegramIcon';
import { cn } from '@/shared/lib/utils';

/*
 * The two "send it somewhere" buttons of the book card. Every button of the
 * card's action row says what it is beside its icon — an icon alone was not
 * recognised until pressed. Their hints are radix tooltips rather than a title
 * attribute: a title never shows on keyboard focus or on a touch screen.
 */

/**
 * The size every button of the card's action row shares: the shared Button's
 * small size, as tall as a finger needs on a phone — where the row becomes a
 * two-column grid — and back to the small height from the card's wide layout
 * up, where a pointer presses it.
 */
export const CARD_ACTION_CLASS = 'h-10 gap-1.5 sm:h-8';

type KindleButtonProps = {
    /** Whether the reader has given a Kindle address to send to. */
    configured: boolean;
    sending: boolean;
    onClick: () => void;
};

/**
 * KindleButton asks to send the book, or — muted, for a reader who has not set
 * up a Kindle — opens the setup. Muted rather than disabled: it still does
 * something, and what it does is the way to make it work.
 *
 * While the book is on its way it shows a spinner and says so. It stays
 * enabled, so the focus a keyboard put on it stays there; a second press is
 * ignored by the caller.
 */
export const KindleButton: React.FC<KindleButtonProps> = ({ configured, sending, onClick }) => {
    const { t } = useTranslation();
    const label = configured ? t('sendToKindle.send') : t('sendToKindle.setup');

    return (
        <TooltipProvider>
            <Tooltip>
                <TooltipTrigger asChild>
                    {/* The name keeps the visible word in it, so a reader
                        who says "Kindle" to a voice control still reaches it. */}
                    <Button
                        variant="ghost"
                        size="sm"
                        aria-label={sending ? t('sendToKindle.sending') : label}
                        aria-busy={sending || undefined}
                        data-configured={configured}
                        onClick={onClick}
                        className={cn(CARD_ACTION_CLASS, !configured && 'text-muted-foreground')}
                    >
                        {sending ? (
                            <Loader2 aria-hidden="true" className="size-4 animate-spin" />
                        ) : (
                            <KindleIcon className="size-4" />
                        )}
                        {t('sendToKindle.button')}
                    </Button>
                </TooltipTrigger>
                <TooltipContent>{sending ? t('sendToKindle.sending') : label}</TooltipContent>
            </Tooltip>
        </TooltipProvider>
    );
};

type TelegramButtonProps = {
    /** Whether the reader's bot is connected and may write to them. */
    connected: boolean;
    sending: boolean;
    onClick: () => void;
};

/**
 * TelegramButton asks to send the book into the reader's Telegram chat, or —
 * muted, for a reader without a working connection — opens the profile's
 * Telegram tab, as Kindle's opens its setup. Labelled as Kindle's is; its hint
 * names what it does, and behaves as Kindle's: hover and keyboard focus, never
 * the focus a closing question gives back.
 */
export const TelegramButton: React.FC<TelegramButtonProps> = ({ connected, sending, onClick }) => {
    const { t } = useTranslation();
    const label = connected ? t('sendToTelegram.send') : t('sendToTelegram.setup');

    return (
        <TooltipProvider>
            <Tooltip>
                <TooltipTrigger asChild>
                    <Button
                        variant="ghost"
                        size="sm"
                        aria-label={sending ? t('sendToTelegram.sending') : label}
                        aria-busy={sending || undefined}
                        data-connected={connected}
                        onClick={onClick}
                        className={cn(CARD_ACTION_CLASS, !connected && 'text-muted-foreground')}
                    >
                        {sending ? (
                            <Loader2 aria-hidden="true" className="size-4 animate-spin" />
                        ) : (
                            <TelegramIcon className="size-4" />
                        )}
                        {t('sendToTelegram.button')}
                    </Button>
                </TooltipTrigger>
                <TooltipContent>{sending ? t('sendToTelegram.sending') : label}</TooltipContent>
            </Tooltip>
        </TooltipProvider>
    );
};
