import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import QRCode from 'qrcode';
import { Check, Copy, ExternalLink } from 'lucide-react';

import * as systemApi from '@/api/system';
import type { DonateMethod } from '@/api/system';
import { useTheme } from '@/context/ThemeContext';
import { Button } from '@/shared/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/shared/ui/tabs';
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

const CopyButton: React.FC<{ value: string; label: string }> = ({ value, label }) => {
    const { t } = useTranslation();
    const [copied, setCopied] = useState(false);

    // The tick is the whole of the feedback, so it has to clear itself — and
    // clean up, or closing the dialog mid-timeout leaves a timer running.
    useEffect(() => {
        if (!copied) return;
        const timer = setTimeout(() => setCopied(false), 1500);
        return () => clearTimeout(timer);
    }, [copied]);

    return (
        <Button
            type="button"
            variant="outline"
            size="sm"
            aria-label={`${t('copy')}: ${label}`}
            onClick={() => {
                navigator.clipboard.writeText(value);
                setCopied(true);
            }}
        >
            {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            {copied ? t('copied') : t('copy')}
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

const Method: React.FC<{ method: DonateMethod }> = ({ method }) => {
    const { t } = useTranslation();
    const shown = method.kind === 'card' ? formatCard(method.value) : method.value;

    return (
        <div className="flex flex-col gap-2">
            {method.kind === 'link' ? (
                <Button asChild variant="outline" size="sm" className="self-start">
                    <a href={method.value} target="_blank" rel="noopener noreferrer">
                        <ExternalLink className="size-4" />
                        {t('donateOpen')}
                    </a>
                </Button>
            ) : (
                <div className="flex flex-wrap items-center gap-2">
                    <code
                        className={cn(
                            'min-w-0 flex-1 rounded bg-muted px-2 py-1.5 text-[13px] break-all',
                            method.kind === 'card' && 'break-normal tracking-wider',
                        )}
                    >
                        {shown}
                    </code>
                    <CopyButton value={method.value} label={method.label} />
                </div>
            )}

            {method.link && method.kind !== 'link' && (
                <Button asChild variant="link" size="sm" className="h-auto self-start p-0 text-xs">
                    <a href={method.link} target="_blank" rel="noopener noreferrer">
                        {t('donateOpen')}
                    </a>
                </Button>
            )}

            {method.qr && <QrCode value={method.value} />}
        </div>
    );
};

const DonateModal: React.FC<DonateModalProps> = ({ open, onClose }) => {
    const { t } = useTranslation();
    const [methods, setMethods] = useState<DonateMethod[]>([]);
    const [chosen, setChosen] = useState<string>();

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

    return (
        <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
            <DialogContent
                closeLabel={t('close')}
                className="scrollbar-thin max-h-[85vh] overflow-y-auto sm:max-w-md"
            >
                <DialogTitle>{t('donateTitle')}</DialogTitle>
                <DialogDescription className="sr-only">{t('donateTitle')}</DialogDescription>

                {/* One way of giving needs no choosing between ways — but it
                    still needs naming, which is otherwise the tab's job. */}
                {methods.length === 1 ? (
                    <section className="flex flex-col gap-2">
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
                                <TabsContent key={method.id} value={method.id}>
                                    <Method method={method} />
                                </TabsContent>
                            ))}
                        </Tabs>
                    )
                )}
            </DialogContent>
        </Dialog>
    );
};

export default DonateModal;
