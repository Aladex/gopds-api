import React from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/shared/ui/tooltip';
import { LIST_SHEET_ROW_CLASS } from '@/shared/components/ListSheet';
import KindleIcon from '@/shared/icons/KindleIcon';
import TelegramIcon from '@/shared/icons/TelegramIcon';
import { cn } from '@/shared/lib/utils';

/*
 * The two "send it somewhere" buttons of the book card. Each says what it is
 * beside its icon — an icon alone was not recognised until pressed. Their
 * hints are radix tooltips rather than a title attribute: a title never shows
 * on keyboard focus or on a touch screen.
 *
 * One component per target, drawn in the shape of wherever it sits:
 *
 * - `rail`: a wide card's action rail, an outline button the height of the
 *   format buttons above it. It says the target's state in words: alone it
 *   has the rail's whole row and reads «В Kindle · настроить» or «Отправляем
 *   на Kindle…»; `compact`, beside the other target, it is the name with the
 *   state on a second line under it («Kindle» / «настроить»).
 * - `row`: a phone's row of three verbs, when only one target is on and the
 *   button names it directly: one tap, no sheet.
 * - `sheet`: a row of the phone's «Отправить» sheet, when both are on. It
 *   spells out what a press does, since a sheet has room and no hover.
 *
 * Whatever the shape, the name and the states are the same: the accessible
 * name says what a press does, muted means "sets it up first", and while the
 * book is on its way the icon turns into a spinner.
 */

/**
 * The classes every button of a phone's action row shares, and the label
 * that goes with them.
 *
 * Three buttons share a 254px row on a 320px phone, where an icon and a word
 * do not fit beside each other in each. Rather than a width threshold of
 * their own, the row lets the icon drop when it does not fit: the label comes
 * first in the source and the icon after it, laid out from the right
 * (`flex-row-reverse`) so the icon still reads first, and the button wraps.
 * When there is no room the icon is what wraps — onto a second line the
 * button's own height clips — so the word always stays.
 */
export const ROW_ACTION_CLASS =
    'h-11 min-w-0 flex-row-reverse flex-wrap gap-x-1 gap-y-0 overflow-hidden px-1 text-sm has-[>svg]:px-1';

export const RowActionContent: React.FC<{ icon: React.ReactNode; label: React.ReactNode }> = ({
    icon,
    label,
}) => (
    <>
        <span className="leading-[2.75rem]">{label}</span>
        {icon}
    </>
);

export type SendButtonVariant = 'rail' | 'row' | 'sheet';

type SendButtonBaseProps = {
    variant: SendButtonVariant;
    /** Whether the target is ready: the Kindle set up, the bot connected. */
    ready: boolean;
    sending: boolean;
    onClick: () => void;
    icon: React.ReactNode;
    /** The target's name: «Kindle», «Telegram». */
    word: string;
    /** What a press does, ready or not, and what it is doing while sending. */
    label: string;
    sendingLabel: string;
    /** The rail's words: «В Kindle», «настроить», «отправляем…». */
    railLabel: string;
    setupShort: string;
    sendingShort: string;
    /** In the rail beside the other target, with half the row. */
    compact?: boolean;
    'data-state-attr': Record<string, boolean>;
};

const Spinner = () => <Loader2 aria-hidden="true" className="size-4 animate-spin" />;

/**
 * SendButton is the shape shared by both targets. It stays enabled while the
 * book is on its way, so the focus a keyboard put on it stays there; a second
 * press is ignored by the caller.
 */
const SendButton: React.FC<SendButtonBaseProps> = ({
    variant,
    ready,
    sending,
    onClick,
    icon,
    word,
    label,
    sendingLabel,
    railLabel,
    setupShort,
    sendingShort,
    compact = false,
    'data-state-attr': stateAttr,
}) => {
    const name = sending ? sendingLabel : label;
    const glyph = sending ? <Spinner /> : icon;
    const stateClass = 'text-[11px] font-normal text-muted-foreground';

    // Alone in the rail: one line, the state after the name or instead of it.
    const railLine = (
        <>
            {glyph}
            <span className="truncate">{sending ? sendingLabel : railLabel}</span>
            {!sending && !ready && (
                <span data-send-state="" className={cn('flex-none', stateClass)}>
                    · {setupShort}
                </span>
            )}
        </>
    );

    // Beside the other target: the name, and the state on a line under it.
    const compactState = sending ? sendingShort : ready ? null : setupShort;
    const railStack = (
        <>
            {glyph}
            <span className="flex min-w-0 flex-col items-start leading-tight">
                <span className="max-w-full truncate">{word}</span>
                {compactState && (
                    <span data-send-state="" className={cn('max-w-full truncate', stateClass)}>
                        {compactState}
                    </span>
                )}
            </span>
        </>
    );

    if (variant === 'sheet') {
        return (
            <button
                type="button"
                aria-busy={sending || undefined}
                onClick={onClick}
                className={cn(LIST_SHEET_ROW_CLASS, !ready && 'text-muted-foreground')}
                {...stateAttr}
            >
                {glyph}
                {name}
            </button>
        );
    }

    return (
        <TooltipProvider>
            <Tooltip>
                <TooltipTrigger asChild>
                    {/* The name keeps the visible word in it, so a reader
                        who says "Kindle" to a voice control still reaches it. */}
                    <Button
                        variant="outline"
                        size={variant === 'rail' ? 'sm' : 'lg'}
                        aria-label={name}
                        aria-busy={sending || undefined}
                        onClick={onClick}
                        className={cn(
                            variant === 'rail'
                                ? 'h-auto min-h-8 w-full min-w-0 gap-1.5 px-2 py-1 text-xs has-[>svg]:px-2'
                                : ROW_ACTION_CLASS,
                            !ready && 'text-muted-foreground',
                        )}
                        {...stateAttr}
                    >
                        {variant === 'row' ? (
                            <RowActionContent icon={glyph} label={word} />
                        ) : compact ? (
                            railStack
                        ) : (
                            railLine
                        )}
                    </Button>
                </TooltipTrigger>
                <TooltipContent>{name}</TooltipContent>
            </Tooltip>
        </TooltipProvider>
    );
};

type KindleButtonProps = {
    variant: SendButtonVariant;
    compact?: boolean;
    /** Whether the reader has given a Kindle address to send to. */
    configured: boolean;
    sending: boolean;
    onClick: () => void;
};

/**
 * KindleButton asks to send the book, or — muted, for a reader who has not set
 * up a Kindle — opens the setup. Muted rather than disabled: it still does
 * something, and what it does is the way to make it work.
 */
export const KindleButton: React.FC<KindleButtonProps> = ({
    variant,
    compact,
    configured,
    sending,
    onClick,
}) => {
    const { t } = useTranslation();
    return (
        <SendButton
            variant={variant}
            ready={configured}
            sending={sending}
            onClick={onClick}
            icon={<KindleIcon className="size-4" />}
            word={t('sendToKindle.button')}
            label={configured ? t('sendToKindle.send') : t('sendToKindle.setup')}
            sendingLabel={t('sendToKindle.sending')}
            railLabel={t('sendToKindle.railLabel')}
            setupShort={t('sendToKindle.setupShort')}
            sendingShort={t('sendToKindle.sendingShort')}
            compact={compact}
            data-state-attr={{ 'data-configured': configured }}
        />
    );
};

type TelegramButtonProps = {
    variant: SendButtonVariant;
    compact?: boolean;
    /** Whether the reader's bot is connected and may write to them. */
    connected: boolean;
    sending: boolean;
    onClick: () => void;
};

/**
 * TelegramButton asks to send the book into the reader's Telegram chat, or —
 * muted, for a reader without a working connection — opens the profile's
 * Telegram tab, as Kindle's opens its setup.
 */
export const TelegramButton: React.FC<TelegramButtonProps> = ({
    variant,
    compact,
    connected,
    sending,
    onClick,
}) => {
    const { t } = useTranslation();
    return (
        <SendButton
            variant={variant}
            ready={connected}
            sending={sending}
            onClick={onClick}
            icon={<TelegramIcon className="size-4" />}
            word={t('sendToTelegram.button')}
            label={connected ? t('sendToTelegram.send') : t('sendToTelegram.setup')}
            sendingLabel={t('sendToTelegram.sending')}
            railLabel={t('sendToTelegram.railLabel')}
            setupShort={t('sendToTelegram.setupShort')}
            sendingShort={t('sendToTelegram.sendingShort')}
            compact={compact}
            data-state-attr={{ 'data-connected': connected }}
        />
    );
};
