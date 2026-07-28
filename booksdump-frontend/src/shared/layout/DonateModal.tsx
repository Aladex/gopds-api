import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import QRCode from 'qrcode';
import { Check, Copy, ExternalLink } from 'lucide-react';

import * as systemApi from '@/api/system';
import type { DonateMethod } from '@/api/system';
import { useTheme } from '@/context/ThemeContext';
import { Button } from '@/shared/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from '@/shared/ui/drawer';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/ui/tabs';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { cn } from '@/shared/lib/utils';

/**
 * DonateModal shows the ways of supporting the service that its operator has
 * configured.
 *
 * It knows nothing about Bitcoin or PayPal. Each method arrives from
 * /api/donate saying how it wants to be shown — an address to copy, a card
 * number, a link to follow — so adding one is an edit to the configuration
 * rather than to this file. The tabs are built from that same list: it used to
 * be six of them written into the markup, with every address repeated three
 * times — once for its code, once as text, once inside the copy button.
 *
 * A tab also means only the method being looked at is mounted, so the QR codes
 * of the others are never drawn.
 */

type DonateModalProps = {
    open: boolean;
    onClose: () => void;
};

/** formatCard groups the digits the way they are printed on the card. */
const formatCard = (value: string) => value.replace(/(.{4})/g, '$1 ').trim();

/**
 * copyText puts a string on the clipboard, by whichever means the page has.
 *
 * navigator.clipboard exists only in a secure context, and this application is
 * self-hosted: a library served over plain http on somebody's network is an
 * ordinary way to run it, and there the API is not merely refused — it is
 * undefined, so reaching for it throws. The old selection dance still works
 * there, and is the whole reason this is not a one-liner.
 */
async function copyText(value: string): Promise<boolean> {
    if (navigator.clipboard?.writeText) {
        try {
            await navigator.clipboard.writeText(value);
            return true;
        } catch {
            // Refused, or no permission. Fall through and try the other way.
        }
    }

    try {
        const active = document.activeElement as HTMLElement | null;

        /*
          Inside the dialog, not on the body. A dialog keeps focus within
          itself, so a textarea appended outside one never takes the selection
          — and execCommand then reports success having copied nothing, which
          is worse than failing: the tick appears and the clipboard is
          unchanged. Measured with the dialog open: a textarea on the body
          selects 0 characters, one inside it selects all of them.
        */
        const host = active?.closest?.('[role="dialog"]') ?? document.body;

        const area = document.createElement('textarea');
        area.value = value;
        area.setAttribute('readonly', '');
        // Off-screen but focusable: a hidden element cannot be selected.
        area.style.cssText = 'position:fixed;top:0;left:0;opacity:0';
        host.appendChild(area);
        area.select();
        const copied = document.execCommand('copy');
        area.remove();

        // Selecting the textarea took the focus off whatever was clicked.
        active?.focus?.();

        return copied;
    } catch {
        return false;
    }
}

/**
 * useCopied copies, and says so for a moment — but only if it worked.
 *
 * The tick is the whole of the feedback, so it has to clear itself — and clean
 * up, or closing the dialog mid-timeout leaves a timer running.
 */
function useCopied(): [boolean, (value: string) => void] {
    const [copied, setCopied] = useState(false);

    useEffect(() => {
        if (!copied) return;
        const timer = setTimeout(() => setCopied(false), 1500);
        return () => clearTimeout(timer);
    }, [copied]);

    const copy = (value: string) => {
        void copyText(value).then((done) => setCopied(done));
    };

    return [copied, copy];
}

/**
 * CopyButton is an icon rather than a labelled button: it sits inside the block
 * holding the value, where what it copies is unambiguous and the word for it
 * would cost more width than the address can spare.
 */
const CopyButton: React.FC<{ value: string; label: string }> = ({ value, label }) => {
    const { t } = useTranslation();
    const [copied, copy] = useCopied();

    const name = `${copied ? t('copied') : t('copy')}: ${label}`;

    return (
        <Button
            type="button"
            variant="ghost"
            size="icon"
            className="shrink-0 hover:bg-background/60"
            aria-label={name}
            title={name}
            onClick={() => copy(value)}
        >
            {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
        </Button>
    );
};

const QrCode: React.FC<{ value: string }> = ({ value }) => {
    const { mode } = useTheme();
    const [dataUrl, setDataUrl] = useState('');

    useEffect(() => {
        let cancelled = false;
        QRCode.toDataURL(value, {
            width: 200,
            margin: 2,
            // Drawn in the theme's colours, which is the reason this stays on
            // the client: a code rendered by the server would have to be told
            // them, and cached per colour.
            color:
                mode === 'dark'
                    ? { dark: '#ffffff', light: '#1e1e1e' }
                    : { dark: '#000000', light: '#ffffff' },
        })
            .then((url) => {
                if (!cancelled) setDataUrl(url);
            })
            .catch((error) => console.error('Error generating QR code:', error));
        return () => {
            cancelled = true;
        };
    }, [value, mode]);

    if (!dataUrl) {
        return null;
    }
    return (
        <img src={dataUrl} alt="" aria-hidden="true" className="size-[200px] self-center rounded" />
    );
};

/**
 * OpenLink goes to a page that takes the money.
 *
 * One appearance wherever it appears. It used to be an outlined button when a
 * method was nothing but a link, and bare text beside a card number — the same
 * word for the same act, dressed two ways, and the second dressed as something
 * that could not be clicked at all: the link variant only underlines on hover,
 * so at rest it was a line of centred text.
 */
const OpenLink: React.FC<{ href: string }> = ({ href }) => {
    const { t } = useTranslation();

    return (
        <Button asChild variant="outline" size="sm">
            <a href={href} target="_blank" rel="noopener noreferrer">
                <ExternalLink className="size-4" />
                {t('donateOpen')}
            </a>
        </Button>
    );
};

/**
 * PaymentCard shows a card number as the thing it is.
 *
 * A row of digits in a grey box is a string; a card is an object someone
 * recognises before reading it, and recognises as the one they were told to
 * expect. The proportions are a real card's, 85.6 by 54mm.
 *
 * The whole card copies, because that is the only thing anyone wants to do
 * with it, and a target the size of a card is easier to hit than an icon.
 */
const PaymentCard: React.FC<{ value: string; label: string }> = ({ value, label }) => {
    const { t } = useTranslation();
    const [copied, copy] = useCopied();

    const name = `${copied ? t('copied') : t('copy')}: ${label}`;

    return (
        <button
            type="button"
            onClick={() => copy(value)}
            aria-label={name}
            title={name}
            className={cn(
                'relative flex aspect-[1.586] w-full max-w-80 flex-col justify-between rounded-xl p-4 text-left',
                'border border-border bg-gradient-to-br from-muted to-accent',
                'outline-none transition-shadow hover:shadow-md focus-visible:ring-[3px] focus-visible:ring-ring/50',
            )}
        >
            <span className="text-xs tracking-wide text-muted-foreground uppercase">{label}</span>

            {/* The chip: enough of one to read as a card at a glance. */}
            <span aria-hidden="true" className="h-6 w-9 rounded-[3px] bg-foreground/25" />

            <span className="font-mono text-[15px] tracking-[0.18em] tabular-nums">
                {formatCard(value)}
            </span>

            <span aria-hidden="true" className="absolute top-3 right-3 text-muted-foreground">
                {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            </span>
        </button>
    );
};

const Method: React.FC<{ method: DonateMethod }> = ({ method }) => {
    return (
        <div className="flex w-full flex-col items-center gap-2">
            {method.kind === 'link' ? (
                <OpenLink href={method.value} />
            ) : method.kind === 'card' ? (
                <PaymentCard value={method.value} label={method.label} />
            ) : (
                /*
                  The copy control lives inside the block it copies, as an icon.
                  A labelled button beside the address took 135px of a 308px
                  sheet, leaving the address to wrap three times to say what it
                  could have said in two.
                */
                <div className="flex w-full items-start gap-1 rounded bg-muted py-1.5 pr-1.5 pl-2">
                    <code className="min-w-0 flex-1 self-center text-[13px] break-all">
                        {method.value}
                    </code>
                    <CopyButton value={method.value} label={method.label} />
                </div>
            )}

            {method.link && method.kind !== 'link' && <OpenLink href={method.link} />}

            {method.qr && <QrCode value={method.value} />}
        </div>
    );
};

const DonateModal: React.FC<DonateModalProps> = ({ open, onClose }) => {
    const { t } = useTranslation();
    const [methods, setMethods] = useState<DonateMethod[]>([]);
    const [chosen, setChosen] = useState<string>();
    // The same break the profile uses to choose between a sheet and a dialog.
    const isMobile = useMediaQuery('(max-width: 600px)');

    useEffect(() => {
        if (!open) {
            return;
        }
        let cancelled = false;
        systemApi
            .getDonateMethods()
            .then((list) => {
                if (!cancelled) setMethods(list ?? []);
            })
            .catch((error) => console.error('Error fetching donate methods:', error));
        return () => {
            cancelled = true;
        };
    }, [open]);

    // The methods arrive after the first render, so the open tab cannot simply
    // be initial state. Deriving it — the chosen one while it is still on
    // offer, the first otherwise — means no effect has to correct it when the
    // list changes under a configuration the reader has never seen.
    const active = methods.some((method) => method.id === chosen) ? chosen : methods[0]?.id;

    /**
     * The panel keeps one height whichever method is showing.
     *
     * Methods differ wildly — a wallet address under a 200px QR code against a
     * single button — and the dialog is centred, so switching tab used to move
     * it by half the difference. Going from Bitcoin to PayPal took 220px off
     * the dialog and carried the tab strip 110px down the screen, out from
     * under the cursor that had just clicked it.
     *
     * The reserve is the tallest shape the configured methods can take, not a
     * constant: an operator offering nothing but cards and links should not
     * get a dialog sized around a QR code nobody has.
     */
    const room = methods.some((method) => method.qr)
        ? 'min-h-[17rem]' // a value, an optional link, and a 200px code
        : methods.some((method) => method.kind === 'card')
          ? 'min-h-[15rem]' // a card at its own proportions, and a link
          : 'min-h-24';

    const body =
        // One way of giving needs no choosing between ways — but it still needs
        // naming, which is otherwise the tab's job.
        methods.length === 1 ? (
            <section className={cn('flex flex-col items-center justify-center gap-2', room)}>
                <h3 className="text-sm font-medium">{methods[0].label}</h3>
                <Method method={methods[0]} />
            </section>
        ) : (
            methods.length > 1 && (
                <Tabs value={active} onValueChange={setChosen}>
                    <TabsList>
                        {methods.map((method) => (
                            <TabsTrigger key={method.id} value={method.id}>
                                {method.label}
                            </TabsTrigger>
                        ))}
                    </TabsList>
                    {methods.map((method) => (
                        <TabsContent
                            key={method.id}
                            value={method.id}
                            className={cn('flex flex-col items-center justify-center', room)}
                        >
                            <Method method={method} />
                        </TabsContent>
                    ))}
                </Tabs>
            )
        );

    // The same sheet the profile and the language use: a phone has the room to
    // show every method's tab at once, which a dialog inset from both edges
    // does not.
    if (isMobile) {
        return (
            <Drawer open={open} onOpenChange={(next) => !next && onClose()}>
                <DrawerContent className="max-h-[85vh]">
                    <DrawerTitle className="px-4 pb-1 text-base font-medium">
                        {t('donateTitle')}
                    </DrawerTitle>
                    <DrawerDescription className="sr-only">{t('donateTitle')}</DrawerDescription>
                    <div className="scrollbar-thin overflow-y-auto px-4 pb-6">{body}</div>
                </DrawerContent>
            </Drawer>
        );
    }

    return (
        <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
            {/*
              Wide enough that six tabs — what this project itself offers — sit
              in one row rather than scrolling. Beyond that the list scrolls,
              which is the honest fallback for a count nobody here decides.
            */}
            <DialogContent
                closeLabel={t('close')}
                className="scrollbar-thin max-h-[85vh] overflow-y-auto sm:max-w-xl"
            >
                <DialogTitle>{t('donateTitle')}</DialogTitle>
                <DialogDescription className="sr-only">{t('donateTitle')}</DialogDescription>
                {body}
            </DialogContent>
        </Dialog>
    );
};

export default DonateModal;
