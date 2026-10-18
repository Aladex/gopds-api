import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/shared/ui/tooltip';
import KindleIcon from '@/shared/icons/KindleIcon';
import TelegramIcon from '@/shared/icons/TelegramIcon';
import { cn } from '@/shared/lib/utils';

/*
 * The two "send it somewhere" buttons of the book card, icon-only like the star
 * beside them. Their hints are radix tooltips rather than a title attribute: a
 * title never shows on keyboard focus or on a touch screen, and for the
 * Telegram button the hint is the whole of what it has to say.
 */

type KindleButtonProps = {
    /** Whether the reader has given a Kindle address to send to. */
    configured: boolean;
    sending: boolean;
    onClick: () => void;
};

/**
 * KindleButton sends the book, or — muted, for a reader who has not set up a
 * Kindle — opens the setup. Muted rather than disabled: it still does
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
                    <Button
                        variant="ghost"
                        size="icon"
                        aria-label={sending ? t('sendToKindle.sending') : label}
                        aria-busy={sending || undefined}
                        data-configured={configured}
                        onClick={onClick}
                        className={cn(!configured && 'text-muted-foreground')}
                    >
                        {sending ? (
                            <Loader2 aria-hidden="true" className="size-4 animate-spin" />
                        ) : (
                            <KindleIcon className="size-4" />
                        )}
                    </Button>
                </TooltipTrigger>
                <TooltipContent>{sending ? t('sendToKindle.sending') : label}</TooltipContent>
            </Tooltip>
        </TooltipProvider>
    );
};

/**
 * TelegramButton is a promise: sending to Telegram comes in a later version,
 * and the button is there so the reader knows where it will be.
 *
 * aria-disabled rather than disabled. A disabled button takes no focus and
 * swallows the pointer, so the hint that explains it could never be shown;
 * this one stays reachable and does nothing but show it — on hover, on focus,
 * and on a tap, which a tooltip does not otherwise answer.
 */
export const TelegramButton: React.FC = () => {
    const { t } = useTranslation();
    const [open, setOpen] = useState(false);

    return (
        <TooltipProvider>
            <Tooltip open={open} onOpenChange={setOpen}>
                <TooltipTrigger asChild>
                    <Button
                        variant="ghost"
                        size="icon"
                        aria-disabled="true"
                        aria-label={t('sendToTelegram.soon')}
                        onClick={(event) => {
                            // Default prevented, the trigger skips its own
                            // close-on-click: a tap opens the hint instead.
                            event.preventDefault();
                            setOpen(true);
                        }}
                        className="cursor-not-allowed opacity-40 hover:bg-transparent hover:text-current dark:hover:bg-transparent"
                    >
                        <TelegramIcon className="size-4" />
                    </Button>
                </TooltipTrigger>
                <TooltipContent>{t('sendToTelegram.soon')}</TooltipContent>
            </Tooltip>
        </TooltipProvider>
    );
};
