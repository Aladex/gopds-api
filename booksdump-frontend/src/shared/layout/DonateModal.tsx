import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import QRCode from 'qrcode';
import { ArrowLeft, Check, Copy, ExternalLink, QrCode as QrIcon } from 'lucide-react';

import type { DonateMethod } from '@/api/system';
import { useTheme } from '@/context/ThemeContext';
import { Button } from '@/shared/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from '@/shared/ui/drawer';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { useSnapCarousel } from '@/shared/hooks/useSnapCarousel';
import { cn } from '@/shared/lib/utils';
import { copyText } from '@/shared/lib/copyText';

/**
 * DonateModal shows the ways of supporting the service that its operator has
 * configured.
 *
 * It knows nothing about Bitcoin or PayPal. Each method arrives from
 * /api/donate saying how it wants to be shown — an address to copy, a card
 * number, a link to follow — so adding one is an edit to the configuration
 * rather than to this file. Everything below branches on `kind`, `link` and
 * `qr`, never on which method it is.
 *
 * Cards come first, as objects: one in its sleeve, several as a row of sleeves
 * swiped sideways. Everything else follows as rows, every value on show at
 * once with nothing to choose between.
 *
 * The methods are handed in rather than fetched here. The header has to ask for
 * them anyway — an installation offering none should not show the button at all
 * — so fetching them again on open asked the server the same question twice and
 * left the dialog showing a title over an empty space until the answer came
 * back. Given the list, it renders whole in its first frame.
 */

type DonateModalProps = {
    open: boolean;
    methods: DonateMethod[];
    onClose: () => void;
};

/** formatCard groups the digits the way they are printed on the card. */
const formatCard = (value: string) => value.replace(/(.{4})/g, '$1 ').trim();

/**
 * hostOf names where a link goes. The configuration is hand-written, so a
 * mistyped address is shown as it stands rather than taking the dialog down.
 */
const hostOf = (url: string) => {
    try {
        return new URL(url).hostname.replace(/^www\./, '');
    } catch {
        return url;
    }
};

/** Phones short and narrow enough that the chrome gives way to the card. */
const COMPACT_QUERY = '(max-width: 375px) and (max-height: 699px)';

/** A fixed 40px toolbar, two lines of address and the 200px code. */
const QR_VIEW_HEIGHT = 272;

/** Opens a method's code, remembering what asked so that going back can return to it. */
type QrOpener = (event: React.MouseEvent<HTMLElement>, id: string) => void;

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
 * CopyButton is an icon rather than a labelled button: it sits in the QR view's
 * toolbar, directly over the value it copies, where that is unambiguous.
 */
const CopyButton: React.FC<{ value: string; label: string }> = ({ value, label }) => {
    const { t } = useTranslation();
    const [copied, copy] = useCopied();

    const name = `${copied ? t('copied') : t('copy')}: ${label}`;

    return (
        <Button
            type="button"
            variant="ghost"
            size="icon-lg"
            className="text-muted-foreground"
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
            // them, and cached per colour. They are the card and its text from
            // index.css (the palette test keeps the two in step).
            color:
                mode === 'dark'
                    ? { dark: '#f1ebe0', light: '#1e1a16' }
                    : { dark: '#2b2118', light: '#fbf8f2' },
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
 * PaymentCard shows a card number as the thing it is.
 *
 * A row of digits in a grey box is a string; a card is an object someone
 * recognises before reading it, and recognises as the one they were told to
 * expect. The proportions are a real card's, 85.6 by 54mm.
 *
 * The whole card copies, because that is the only thing anyone wants to do
 * with it, and a target the size of a card is easier to hit than an icon.
 *
 * The number stays 15px at every width; what gives on a narrow face is the
 * tracking. The container query reads the face's content box, so 225px there
 * is a 260px card.
 */
const PaymentCard: React.FC<{
    value: string;
    label: string;
    compact: boolean;
    tabIndex?: number;
}> = ({ value, label, compact, tabIndex }) => {
    const { t } = useTranslation();
    const [copied, copy] = useCopied();

    const name = `${copied ? t('copied') : t('copy')}: ${label}`;

    return (
        <button
            type="button"
            onClick={() => copy(value)}
            aria-label={name}
            title={name}
            tabIndex={tabIndex}
            className={cn(
                '@container relative flex aspect-[1.586] w-[var(--card-width)] flex-col justify-between rounded-xl text-left',
                compact ? 'p-3' : 'p-4',
                'border border-border bg-gradient-to-br shadow-[0_2px_4px_rgba(0,0,0,.07)]',
                'from-[#ebe6dd] to-[#f3eee5] dark:from-[#342e27] dark:to-[#2b251f]',
                'outline-none transition-shadow hover:shadow-md focus-visible:ring-[3px] focus-visible:ring-focus',
            )}
        >
            <span className="max-w-[calc(100%-1.5rem)] text-xs tracking-wide text-[#5b5044] uppercase dark:text-[#c9beac]">
                {label}
            </span>

            {/* The chip: enough of one to read as a card at a glance. */}
            <span aria-hidden="true" className="h-6 w-9 rounded-[3px] bg-foreground/25" />

            <span
                className={cn(
                    'font-mono text-[15px] whitespace-nowrap tabular-nums',
                    compact
                        ? 'tracking-[0.08em]'
                        : 'tracking-[0.18em] @max-[225px]:tracking-[0.08em]',
                )}
            >
                {formatCard(value)}
            </span>

            <span aria-hidden="true" className="absolute top-3 right-3 text-muted-foreground">
                {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            </span>
        </button>
    );
};

/**
 * Sleeve is a card and what can be done with it, held in one frame: the number
 * to copy, and the page of the bank that takes the money, named by its host so
 * nobody has to follow it to find out whose it is.
 *
 * Where a code was configured its action sits beside that page's, outside the
 * card: the whole face is a button already, and one cannot hold another.
 *
 * A card with neither is the card alone. Another frame around it would have
 * nothing to group it with.
 */
const Sleeve: React.FC<{
    method: DonateMethod;
    compact: boolean;
    /** Tighter still, where one card shares a small screen with the rows. */
    dense: boolean;
    tabIndex?: number;
    qrId?: string;
    onQr: QrOpener;
}> = ({ method, compact, dense, tabIndex, qrId, onQr }) => {
    const { t } = useTranslation();
    const face = (
        <PaymentCard
            value={method.value}
            label={method.label}
            compact={compact}
            tabIndex={tabIndex}
        />
    );

    if (!method.link && !method.qr) return face;

    return (
        <div
            className={cn(
                'flex w-fit flex-none flex-col rounded-2xl border border-border bg-accent',
                compact ? 'p-1.5' : 'p-2',
            )}
        >
            {face}
            <div className={cn('flex gap-2', dense ? 'mt-1.5' : 'mt-2')}>
                {method.link && (
                    <Button
                        asChild
                        className={cn(
                            'h-auto min-w-0 flex-1 justify-between rounded-lg text-left whitespace-normal sm:h-auto sm:text-sm',
                            dense
                                ? 'min-h-10 px-2 py-[3px] text-xs font-semibold sm:px-2 sm:text-xs'
                                : 'min-h-12 px-3 py-[5px] text-sm leading-5 font-semibold sm:px-3',
                        )}
                    >
                        <a
                            href={method.link}
                            target="_blank"
                            rel="noopener noreferrer"
                            tabIndex={tabIndex}
                        >
                            <span>
                                {t('donatePayOnline')}
                                <span className="block text-[11px] leading-[14px] font-normal">
                                    {hostOf(method.link)}
                                </span>
                            </span>
                            <ExternalLink className="size-4" />
                        </a>
                    </Button>
                )}
                {method.qr && (
                    <Button
                        type="button"
                        variant="outline"
                        size="icon-lg"
                        tabIndex={tabIndex}
                        aria-label={t('donateQr', { label: method.label })}
                        aria-expanded={method.id === qrId}
                        onClick={(event) => onQr(event, method.id)}
                        className={cn(
                            'h-auto rounded-lg',
                            dense ? 'min-h-10' : 'min-h-12',
                            !method.link ? 'flex-1' : !dense && 'w-12',
                        )}
                    >
                        <QrIcon className="size-4" />
                    </Button>
                )}
            </div>
        </div>
    );
};

/**
 * CardCarousel is several cards as a row of whole sleeves, each with its own
 * action, the next one showing at the edge to say there is more.
 *
 * The track is useSnapCarousel's, centred: the browser scrolls it, the slide
 * nearest its centre is the selected one, and a tap on a card that is only
 * peeking, or the end of a swipe, never reaches the card or its controls. The
 * dots and the count sit outside every sleeve for the same reason.
 */
const CardCarousel: React.FC<{
    cards: DonateMethod[];
    compact: boolean;
    qrId?: string;
    onQr: QrOpener;
}> = ({ cards, compact, qrId, onQr }) => {
    const { t } = useTranslation();
    const { trackRef, active, dragging, go, trackProps } = useSnapCarousel({
        count: cards.length,
        align: 'center',
    });

    return (
        <section
            // A card's code is that card's: selecting another under it would
            // leave the sheet showing one card and offering another's code.
            inert={cards.some((method) => method.id === qrId)}
            aria-roledescription={t('donateCarouselRole')}
            aria-label={t('donateCarousel')}
            style={
                {
                    '--card-width': 'min(286px, calc(100vw - 80px))',
                    '--slide-width': `calc(var(--card-width) + ${compact ? 14 : 18}px)`,
                } as React.CSSProperties
            }
            className="w-[min(calc(100%+1.5rem),384px)] min-w-0 shrink-0"
        >
            <div
                ref={trackRef}
                role="group"
                tabIndex={0}
                aria-label={t('donateCarouselHint')}
                {...trackProps}
                className={cn(
                    'flex gap-2 overflow-x-auto overflow-y-hidden overscroll-x-contain py-1',
                    'touch-pan-x touch-pan-y [scrollbar-width:none] [&::-webkit-scrollbar]:hidden',
                    'scroll-px-[calc((100%-var(--slide-width))/2)] px-[calc((100%-var(--slide-width))/2)]',
                    'rounded-md focus-visible:outline-3 focus-visible:-outline-offset-3 focus-visible:outline-focus',
                    dragging
                        ? 'cursor-grabbing snap-none select-none'
                        : 'cursor-grab snap-x snap-mandatory',
                )}
            >
                {cards.map((method, index) => (
                    <div
                        key={method.id}
                        data-slide={index}
                        role="group"
                        aria-roledescription={t('donateCarouselSlideRole')}
                        aria-label={t('donateCarouselSlide', {
                            index: index + 1,
                            total: cards.length,
                        })}
                        aria-hidden={index !== active}
                        className={cn(
                            // As tall as a sleeve whether or not this one has
                            // one, so a linkless card sits in the same height.
                            'flex min-w-0 flex-[0_0_var(--slide-width)] snap-center snap-always items-center justify-center',
                            compact
                                ? 'min-h-[calc(var(--card-width)/1.586+70px)]'
                                : 'min-h-[calc(var(--card-width)/1.586+74px)]',
                        )}
                    >
                        <Sleeve
                            method={method}
                            compact={compact}
                            dense={false}
                            tabIndex={index === active ? 0 : -1}
                            qrId={qrId}
                            onQr={onQr}
                        />
                    </div>
                ))}
            </div>

            <div className="flex min-h-10 items-center justify-center">
                <div role="group" aria-label={t('donateCarouselPick')} className="flex">
                    {cards.map((method, index) => (
                        <Button
                            key={method.id}
                            type="button"
                            variant="ghost"
                            size="icon-lg"
                            aria-label={t('donateCarouselCard', {
                                index: index + 1,
                                label: method.label,
                            })}
                            aria-current={index === active}
                            onClick={() => go(index)}
                            className="text-muted-foreground"
                        >
                            <span
                                className={cn(
                                    'h-1.5 rounded-full bg-current',
                                    index === active ? 'w-4' : 'w-1.5 opacity-35',
                                )}
                            />
                        </Button>
                    ))}
                </div>
                <span
                    aria-live="polite"
                    aria-atomic="true"
                    className="ml-2 min-w-[30px] text-xs text-muted-foreground tabular-nums"
                >
                    {active + 1} / {cards.length}
                </span>
            </div>
        </section>
    );
};

const rowIcon = 'flex size-10 shrink-0 items-center justify-center text-muted-foreground';
const rowTarget =
    'h-auto min-w-0 flex-1 gap-0 rounded-sm px-0 text-left whitespace-normal sm:h-auto sm:px-0';

/** An address and its whole row to copy it with; it wraps, and is never cut. */
const AddressRow: React.FC<{ method: DonateMethod }> = ({ method }) => {
    const { t } = useTranslation();
    const [copied, copy] = useCopied();

    const name = `${copied ? t('copied') : t('copy')}: ${method.label}`;

    return (
        <Button
            type="button"
            variant="ghost"
            onClick={() => copy(method.value)}
            aria-label={name}
            title={name}
            className={cn(rowTarget, 'min-h-14 justify-start py-[3px] font-normal')}
        >
            <span className="flex min-w-0 flex-1 flex-col">
                <span className="text-sm leading-[18px] font-medium">{method.label}</span>
                <span className="text-xs leading-4 [overflow-wrap:anywhere] text-muted-foreground">
                    {method.value}
                </span>
            </span>
            <span aria-hidden="true" className={rowIcon}>
                {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            </span>
        </Button>
    );
};

/** A method that is nothing but a link: its name, and whose page it is. */
const LinkRow: React.FC<{ method: DonateMethod }> = ({ method }) => (
    <Button asChild variant="ghost" className={cn(rowTarget, 'min-h-[43px] justify-between py-0')}>
        <a href={method.value} target="_blank" rel="noopener noreferrer">
            <span className="flex min-w-0 flex-1 items-baseline gap-2 text-sm font-medium">
                {method.label}
                <span className="text-xs font-normal [overflow-wrap:anywhere] text-muted-foreground">
                    {hostOf(method.value)}
                </span>
            </span>
            <span aria-hidden="true" className={rowIcon}>
                <ExternalLink className="size-4" />
            </span>
        </a>
    </Button>
);

/**
 * QrView takes the list's place rather than opening a layer over it: the list
 * was the reason for coming here and is one press away, with the full value
 * and its copy beside the code.
 */
const QrView: React.FC<{
    method: DonateMethod;
    backRef: React.Ref<HTMLButtonElement>;
    onBack: () => void;
}> = ({ method, backRef, onBack }) => {
    const { t } = useTranslation();

    return (
        <div data-testid="donate-qr" className="col-start-1 row-start-1 flex flex-col">
            <div className="flex h-10 items-center">
                <Button
                    ref={backRef}
                    type="button"
                    variant="ghost"
                    onClick={onBack}
                    aria-label={`${t('donateBack')}: ${method.label}`}
                    className="min-w-0 flex-1 justify-start px-1 sm:px-1"
                >
                    <ArrowLeft className="size-4" />
                    {method.label}
                </Button>
                <CopyButton value={method.value} label={method.label} />
            </div>
            <span className="min-h-8 text-xs leading-4 [overflow-wrap:anywhere] text-muted-foreground select-text">
                {method.value}
            </span>
            <QrCode value={method.value} />
        </div>
    );
};

/**
 * useQrView is the one code on show, whichever method it belongs to: what opens
 * it, what it keeps of the place it was opened from, and the return there.
 */
function useQrView() {
    const [id, setId] = useState<string>();
    const container = useRef<HTMLDivElement>(null);
    const back = useRef<HTMLButtonElement>(null);
    const origin = useRef<{ trigger: HTMLElement; scrollTop: number } | null>(null);

    useLayoutEffect(() => {
        const list = container.current;
        const body = list?.closest('[data-donate-body]');
        if (!list || !body) return;

        if (!id) {
            // Going back puts the reader where they were, on what they pressed.
            const from = origin.current;
            if (!from) return;
            origin.current = null;
            list.style.minHeight = '';
            body.scrollTop = from.scrollTop;
            from.trigger.focus({ preventScroll: true });
            return;
        }

        // Near the end of the body the code would be cut off, and a short list
        // leaves nothing to scroll it into view with, so it is given the room.
        const top =
            list.getBoundingClientRect().top - body.getBoundingClientRect().top + body.scrollTop;
        if (top + QR_VIEW_HEIGHT > body.clientHeight) {
            list.style.minHeight = `${Math.max(list.offsetHeight, body.clientHeight)}px`;
            body.scrollTop = Math.floor(top);
        }
        back.current?.focus({ preventScroll: true });
    }, [id]);

    const open: QrOpener = (event, next) => {
        origin.current = {
            trigger: event.currentTarget,
            // A card's code can replace an address's: the list was left once.
            scrollTop:
                origin.current?.scrollTop ??
                container.current?.closest('[data-donate-body]')?.scrollTop ??
                0,
        };
        setId(next);
    };

    return { id, container, back, open, close: () => setId(undefined) };
}

/**
 * MethodRows lists everything that is not a card, each value whole, and gives
 * up its place to a code, a card's included.
 *
 * It reserves the height of a code whenever one can be shown, so opening one
 * moves nothing: the sheet is centred on a desktop and hangs from the bottom
 * edge on a phone, and a change of height would slide its title out from under
 * the eye. Codes are drawn on request, except where there is only the one and
 * room for it in the list.
 */
const MethodRows: React.FC<{
    rows: DonateMethod[];
    shown?: DonateMethod;
    reserve: boolean;
    listRef: React.Ref<HTMLDivElement>;
    backRef: React.Ref<HTMLButtonElement>;
    onQr: QrOpener;
    onBack: () => void;
}> = ({ rows, shown, reserve, listRef, backRef, onQr, onBack }) => {
    const { t } = useTranslation();

    const coded = rows.filter((method) => method.qr);
    const inline = coded.length === 1 && rows.length <= 2 ? coded[0] : undefined;

    return (
        <div
            ref={listRef}
            className="grid grid-cols-1"
            style={{
                gridTemplateRows: reserve ? `minmax(${QR_VIEW_HEIGHT}px, auto)` : undefined,
            }}
        >
            {/*
              Faded rather than made invisible: Button transitions everything,
              visibility included, so a row made visible again is still hidden
              in the frame that has to give focus back to it, and refuses it.
            */}
            <div
                data-testid="donate-list"
                inert={shown !== undefined}
                className={cn('col-start-1 row-start-1 min-w-0', shown && 'opacity-0')}
            >
                {rows.map((method) => (
                    <div
                        key={method.id}
                        className="flex items-center border-b border-border last:border-0"
                    >
                        {method.kind === 'link' ? (
                            <LinkRow method={method} />
                        ) : (
                            <AddressRow method={method} />
                        )}
                        {method.kind !== 'link' && method.link && (
                            <Button asChild variant="ghost" size="icon-lg">
                                <a
                                    href={method.link}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    aria-label={`${t('donatePayOnline')}: ${method.label}`}
                                    className="text-muted-foreground"
                                >
                                    <ExternalLink className="size-4" />
                                </a>
                            </Button>
                        )}
                        {method.qr && method !== inline && (
                            <Button
                                type="button"
                                variant="ghost"
                                size="icon-lg"
                                aria-label={t('donateQr', { label: method.label })}
                                aria-expanded={method.id === shown?.id}
                                onClick={(event) => onQr(event, method.id)}
                                className="text-muted-foreground"
                            >
                                <QrIcon className="size-4" />
                            </Button>
                        )}
                    </div>
                ))}
                {inline && (
                    <div className="mt-2 flex flex-col">
                        <QrCode value={inline.value} />
                    </div>
                )}
            </div>
            {shown && <QrView method={shown} backRef={backRef} onBack={onBack} />}
        </div>
    );
};

const DonateModal: React.FC<DonateModalProps> = ({ open, methods, onClose }) => {
    const { t } = useTranslation();
    // The same break the profile uses to choose between a sheet and a dialog.
    const isMobile = useMediaQuery('(max-width: 600px)');
    const short = useMediaQuery(COMPACT_QUERY);

    const cards = methods.filter((method) => method.kind === 'card');
    const rows = methods.filter((method) => method.kind !== 'card');
    // Only when the two compete for the height: alone, either has room.
    const compact = short && cards.length > 0 && rows.length > 0;
    const { id: qrId, container, back, open: openQr, close: closeQr } = useQrView();
    const reserve = methods.some((method) => method.qr);

    const body = (
        <div
            data-donate-body
            style={{ '--card-width': 'min(286px, calc(100vw - 52px))' } as React.CSSProperties}
            className={cn(
                'scrollbar-thin min-h-0 overflow-x-hidden overflow-y-auto overscroll-contain',
                !isMobile && '-mx-1 px-1 pb-1',
                isMobile && (compact ? 'px-4 pb-3' : 'px-4 pt-2 pb-5'),
            )}
        >
            {cards.length > 0 && (
                <div
                    // Left to itself the sheet takes the first few pixels of any
                    // swipe with a downward slant, 13 of them in Chrome, before
                    // the browser settles on panning the track. A vertical swipe
                    // that starts here still scrolls the body; the sheet is
                    // dragged by its title. On this side of the carousel because
                    // an inert element is no pointer target: while a card's code
                    // is up the press lands here, and vaul looks from here up.
                    data-vaul-no-drag={cards.length > 1 ? '' : undefined}
                    className={cn('flex justify-center last:mb-0', compact ? 'mb-1.5' : 'mb-3')}
                >
                    {cards.length > 1 ? (
                        <CardCarousel cards={cards} compact={compact} qrId={qrId} onQr={openQr} />
                    ) : (
                        <Sleeve
                            method={cards[0]}
                            compact={compact}
                            dense={compact}
                            qrId={qrId}
                            onQr={openQr}
                        />
                    )}
                </div>
            )}
            {(rows.length > 0 || reserve) && (
                <MethodRows
                    rows={rows}
                    shown={methods.find((method) => method.id === qrId)}
                    reserve={reserve}
                    listRef={container}
                    backRef={back}
                    onQr={openQr}
                    onBack={closeQr}
                />
            )}
        </div>
    );

    // The same sheet the profile and the language use, with its bottom cap left
    // as the primitive sets it. A plain max-h here would lose to that on
    // specificity, as ResponsiveSheet explains.
    if (isMobile) {
        return (
            <Drawer open={open} onOpenChange={(next) => !next && onClose()}>
                <DrawerContent>
                    <DrawerTitle className="px-4 pb-1 text-base font-medium">
                        {t('donateTitle')}
                    </DrawerTitle>
                    <DrawerDescription className="sr-only">{t('donateTitle')}</DrawerDescription>
                    {body}
                </DrawerContent>
            </Drawer>
        );
    }

    return (
        <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
            {/* A column as in ResponsiveSheet: the title stays, the body scrolls. */}
            <DialogContent
                closeLabel={t('close')}
                className="flex max-h-[85vh] flex-col sm:max-w-xl"
            >
                <DialogTitle className="pr-8">{t('donateTitle')}</DialogTitle>
                <DialogDescription className="sr-only">{t('donateTitle')}</DialogDescription>
                {body}
            </DialogContent>
        </Dialog>
    );
};

export default DonateModal;
