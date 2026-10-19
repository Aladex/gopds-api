import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import * as telegramApi from '@/api/telegram';
import type { BotStatus } from '@/api/telegram';
import ResponsiveSheet from '@/shared/components/ResponsiveSheet';
import SetupVideo, {
    type VideoLanguage,
    useFileExists,
    videoLanguage,
} from '@/shared/components/SetupVideo';
import { Button } from '@/shared/ui/button';
import { Expandable } from '@/shared/ui/expandable';
import { Input } from '@/shared/ui/input';

export const TELEGRAM_TOKEN_FIELD_ID = 'profile-bot-token';
export const TELEGRAM_CLAIM_FIELD_ID = 'profile-bot-claim';

/** The outline-and-red look the profile gives its irreversible buttons. */
const DANGER_OUTLINE =
    'border-destructive/50 text-destructive hover:bg-destructive/10 hover:text-destructive';

/** How often a waiting connection is read again while the profile is open. */
export const PENDING_POLL_MS = 3000;

/**
 * waitsForTelegram: something is to happen in Telegram, away from this page —
 * a bot entered waits for its «Start», a personal bot is being created there,
 * or a bot whose owner changed waits to be connected again.
 */
const waitsForTelegram = (status: BotStatus | null) =>
    status?.state === 'pending_link' ||
    status?.state === 'owner_changed' ||
    Boolean(status?.pending_bot_username) ||
    Boolean(status?.managed);

/**
 * useTelegramConnection reads the reader's Telegram connection whenever the
 * profile opens. The profile shows its state in the group's badge and the
 * settings below act on it, so both take it from here.
 *
 * The confirmation happens in Telegram, away from this page: while a bot
 * waits for it, the connection is read again every few seconds and whenever
 * the reader comes back to the page, and the moment it is confirmed the
 * reader is told so.
 */
export const useTelegramConnection = (open: boolean) => {
    const { t } = useTranslation();
    const [status, setStatus] = useState<BotStatus | null>(null);
    const last = useRef<BotStatus | null>(null);
    // Reads overlap — a poll, a focus and a return can each start one — and
    // answers arrive in any order. Each read and each action takes the next
    // generation; an answer is applied only if nothing newer was asked since,
    // so a slow "still waiting" cannot undo a confirmation already shown.
    const generation = useRef(0);
    const apply = useCallback(
        (next: BotStatus) => {
            const before = last.current;
            last.current = next;
            setStatus(next);
            const confirmed =
                waitsForTelegram(before) &&
                !waitsForTelegram(next) &&
                (next.state === 'connected' || next.state === 'blocked');
            if (confirmed) {
                toast.success(t('telegram.linked', { username: next.bot_username ?? '' }));
            }
        },
        [t],
    );
    const refresh = useCallback(async () => {
        const mine = ++generation.current;
        try {
            const next = await telegramApi.getBotStatus();
            if (mine === generation.current) apply(next);
        } catch {
            // Left as it was: the next reading tries again.
        }
    }, [apply]);
    // An action's own answer (connect, new link, disconnect) supersedes every
    // read still on its way.
    const applyAction = useCallback(
        (next: BotStatus) => {
            generation.current += 1;
            apply(next);
        },
        [apply],
    );
    useEffect(() => {
        if (open) void refresh();
    }, [open, refresh]);

    const waiting = open && waitsForTelegram(status);
    useEffect(() => {
        if (!waiting) return;
        const poll = window.setInterval(() => void refresh(), PENDING_POLL_MS);
        const onFocus = () => void refresh();
        const onVisible = () => {
            if (document.visibilityState === 'visible') void refresh();
        };
        window.addEventListener('focus', onFocus);
        document.addEventListener('visibilitychange', onVisible);
        return () => {
            window.clearInterval(poll);
            window.removeEventListener('focus', onFocus);
            document.removeEventListener('visibilitychange', onVisible);
        };
    }, [waiting, refresh]);

    return { status, setStatus: applyAction };
};

/** The badge beside the group's title: how the connection stands, in a word. */
export const telegramBadge = (status: BotStatus | null) => {
    switch (status?.state) {
        case 'connected':
            return { ok: true, key: 'telegram.badge.connected' };
        case 'pending_link':
            return { ok: false, key: 'telegram.badge.pending' };
        case 'blocked':
        case 'token_rejected':
        case 'owner_changed':
            return { ok: false, key: 'telegram.badge.attention' };
        default:
            // A personal bot on its way from Telegram waits as an entered one does.
            return status?.managed
                ? { ok: false, key: 'telegram.badge.pending' }
                : { ok: false, key: 'telegram.badge.none' };
    }
};

const BotFatherLink = (props: React.PropsWithChildren) => (
    <a
        href="https://t.me/BotFather"
        target="_blank"
        rel="noopener noreferrer"
        className="text-primary underline-offset-2 hover:underline"
    >
        {props.children}
    </a>
);

/** The how-to video of creating a personal bot, one per interface language. */
const telegramVideo = (lang: VideoLanguage) => ({
    src: `/videos/telegram-setup-${lang}.mp4`,
    poster: `/videos/telegram-setup-${lang}.jpg`,
});

/**
 * TelegramVideo is the video of creating a personal bot, in the place and the
 * shell of Kindle's. It is recorded after the feature ships, so until its
 * poster is there nothing shows — not even the toggle — and a video that
 * turns out to be missing takes the toggle away with it.
 */
const TelegramVideo: React.FC = () => {
    const { t, i18n } = useTranslation();
    const { poster } = telegramVideo(videoLanguage(i18n.language ?? ''));
    const exists = useFileExists(poster);
    const [show, setShow] = useState(false);
    const [missing, setMissing] = useState(false);
    const onMissing = useCallback(() => setMissing(true), []);
    if (!exists || missing) return null;
    return (
        <div data-testid="telegram-video">
            <button
                type="button"
                onClick={() => setShow((value) => !value)}
                aria-expanded={show}
                className="text-xs text-primary underline"
            >
                {show ? t('telegram.video.hide') : t('telegram.video.watch')}
            </button>
            {/* As in Kindle: the player is not built until asked for. */}
            <Expandable open={show} peekLines={0} fade={false}>
                <div className="pt-2">
                    {show && (
                        <SetupVideo
                            files={telegramVideo}
                            label={t('telegram.video.label')}
                            onMissing={onMissing}
                        />
                    )}
                </div>
            </Expandable>
        </div>
    );
};

type TelegramSettingsProps = {
    status: BotStatus | null;
    onStatus: (status: BotStatus) => void;
};

/**
 * TelegramSettings is the profile's Telegram group.
 *
 * A bot is connected in two steps: its token is checked here, and the
 * connection is confirmed from Telegram itself by opening a one-time link —
 * whoever presses «Start» there becomes the bot's owner, and only through a
 * link this profile handed out. Until then the bot waits, and a change of bot
 * leaves the current one working.
 *
 * Where the library has a manager bot, the reader is first offered a
 * personal bot made for them: Telegram creates it after a confirmation there,
 * the library finds it, and it is confirmed with the same one-time link. The
 * token form stays one click away for a bot the reader already has.
 *
 * Every action applies on its own button, like the Kindle group beside it;
 * the profile's Save never sees any of this.
 */
const TelegramSettings: React.FC<TelegramSettingsProps> = ({ status, onStatus }) => {
    const { t } = useTranslation();
    // The token form, over a connection that exists: changing the bot, or
    // giving a working token back to one Telegram refused.
    const [entering, setEntering] = useState(false);
    const [token, setToken] = useState('');
    const [busy, setBusy] = useState(false);
    const [showSteps, setShowSteps] = useState(false);
    const [confirmOpen, setConfirmOpen] = useState(false);
    // The token form over the offer of a personal bot: «I already have a bot».
    const [existing, setExisting] = useState(false);
    // The field naming a created bot, for when its username was changed in
    // Telegram and the library cannot recognise it: «I already created it».
    const [claiming, setClaiming] = useState(false);
    const [claimName, setClaimName] = useState('');
    // The link of the last call that made one. The server never gives it
    // again, so it is kept here for as long as the profile is open.
    // It belongs to the bot it was made for: once that bot is confirmed — or
    // another one entered — it is not shown any more.
    const [link, setLink] = useState<{ url: string; bot: string } | null>(null);
    // Whether that link has run out; set by a timer at its expiry, so the
    // button turns into «get a new link» without a reload.
    const [linkExpired, setLinkExpired] = useState(false);
    const expiresAt = status?.link_expires_at;
    useEffect(() => {
        if (!link || !expiresAt) return;
        const left = Date.parse(expiresAt) - Date.now();
        const timer = window.setTimeout(() => setLinkExpired(true), Math.max(left, 0));
        return () => window.clearTimeout(timer);
    }, [link, expiresAt]);
    const disconnectRef = useRef<HTMLButtonElement>(null);

    const apply = useCallback(
        (next: BotStatus) => {
            onStatus(next);
            if (next.link_url) {
                // A link is for the bot waiting for its «Start» — or, after its
                // owner changed, for the bound bot itself.
                const bot = next.pending_bot_username ?? next.bot_username ?? '';
                setLink({ url: next.link_url, bot });
                setLinkExpired(false);
            }
        },
        [onStatus],
    );

    const fail = useCallback(
        (error: unknown) => {
            toast.error(t(`telegram.error.${telegramApi.telegramErrorCode(error)}`));
        },
        [t],
    );

    // run applies an action's answer, or says what went wrong.
    const run = async (action: () => Promise<BotStatus>) => {
        setBusy(true);
        try {
            apply(await action());
            return true;
        } catch (error) {
            fail(error);
            return false;
        } finally {
            setBusy(false);
        }
    };

    // A personal bot that Telegram has made ready waits for its «Start» like an
    // entered one; its link is asked for once, without a click, so the next
    // button opens it.
    const managedBot =
        status?.state === 'pending_link' && status.managed?.state === 'ready'
            ? (status.pending_bot_username ?? status.managed.bot_username ?? '')
            : null;
    const holdsLink = managedBot !== null && link?.bot === managedBot;
    const linkAsked = useRef<string | null>(null);
    useEffect(() => {
        if (!managedBot || holdsLink || linkAsked.current === managedBot) return;
        linkAsked.current = managedBot;
        telegramApi.newLink().then(apply, fail);
    }, [managedBot, holdsLink, apply, fail]);

    /**
     * createBot sends the reader to Telegram's creation window. The window is
     * opened at the click, while the browser still counts it as the reader's —
     * once the request has answered, a popup blocker would stop it — and is
     * sent to Telegram when the address is known. A blocked window leaves the
     * «Open Telegram» button on the next screen.
     */
    const createBot = async () => {
        const opened = window.open('', '_blank');
        if (opened) opened.opener = null;
        setClaiming(false);
        setBusy(true);
        try {
            const next = await telegramApi.startManaged();
            apply(next);
            const url = next.managed?.create_url;
            if (opened && url) opened.location.href = url;
            else opened?.close();
        } catch (error) {
            opened?.close();
            fail(error);
        } finally {
            setBusy(false);
        }
    };

    const claimBot = async () => {
        const name = claimName.trim();
        if (!name) return;
        if (await run(() => telegramApi.claimManaged(name))) {
            setClaimName('');
            setClaiming(false);
        }
    };

    const cancelCreation = async () => {
        if (await run(telegramApi.cancelManaged)) {
            setClaiming(false);
            setClaimName('');
        }
    };

    const checkToken = async () => {
        if (!token.trim()) return;
        setBusy(true);
        try {
            apply(await telegramApi.setBotToken(token.trim()));
            setToken('');
            setEntering(false);
            setExisting(false);
        } catch (error) {
            fail(error);
        } finally {
            setBusy(false);
        }
    };

    const freshLink = async () => {
        setBusy(true);
        try {
            apply(await telegramApi.newLink());
        } catch (error) {
            fail(error);
        } finally {
            setBusy(false);
        }
    };

    const disconnect = async () => {
        setBusy(true);
        try {
            apply(await telegramApi.removeBotToken());
            setLink(null);
            setEntering(false);
            setExisting(false);
            setConfirmOpen(false);
            toast.success(t('telegram.disconnected'));
        } catch (error) {
            fail(error);
        } finally {
            setBusy(false);
        }
    };

    if (!status) return null;

    const state = status.state;
    const bound =
        state === 'connected' ||
        state === 'blocked' ||
        state === 'token_rejected' ||
        state === 'owner_changed';
    const managed = status.managed;
    // The waiting screen while the bot is being made in Telegram; a bot the
    // library already found and confirmed ready is on the ready screen.
    const creating = state === 'none' && managed !== undefined && managed.state !== 'ready';
    const offerManaged = state === 'none' && !creating && Boolean(status.managed_available);
    const managedBound = status.bot_kind === 'managed';
    const pending = status.pending_bot_username;
    const pendingBot = status.pending_bot_username ?? '';
    const ownLink = link !== null && link.bot === pendingBot ? link : null;
    const linkLive = ownLink !== null && !linkExpired;

    const tokenForm = (change: boolean) => (
        <div className="flex flex-col gap-2">
            <p className="text-sm font-medium">
                {t(change ? 'telegram.change.title' : 'telegram.existing.title')}
            </p>
            <p className="text-xs text-muted-foreground">
                {change ? (
                    t('telegram.change.body')
                ) : (
                    <Trans
                        i18nKey="telegram.existing.hint"
                        components={{ botfather: <BotFatherLink /> }}
                    />
                )}
            </p>
            {!change && (
                <>
                    <button
                        type="button"
                        onClick={() => setShowSteps((value) => !value)}
                        aria-expanded={showSteps}
                        className="self-start text-xs text-primary underline"
                    >
                        {t('telegram.create.toggle')}
                    </button>
                    <Expandable open={showSteps} peekLines={0} fade={false}>
                        <ol className="mb-1 list-decimal pl-5 text-xs text-muted-foreground [&>li]:mb-1">
                            <li>
                                <Trans
                                    i18nKey="telegram.create.step1"
                                    components={{ botfather: <BotFatherLink /> }}
                                />
                            </li>
                            <li>{t('telegram.create.step2')}</li>
                            <li>{t('telegram.create.step3')}</li>
                            <li>{t('telegram.create.step4')}</li>
                        </ol>
                    </Expandable>
                </>
            )}
            <label htmlFor={TELEGRAM_TOKEN_FIELD_ID} className="sr-only">
                {t('telegram.tokenLabel')}
            </label>
            <Input
                id={TELEGRAM_TOKEN_FIELD_ID}
                type="password"
                autoComplete="off"
                placeholder={t('telegram.tokenPlaceholder')}
                value={token}
                disabled={busy}
                onChange={(event) => setToken(event.target.value)}
            />
            <div className="flex flex-wrap gap-2">
                <Button size="sm" disabled={busy || !token.trim()} onClick={checkToken}>
                    {busy ? t('telegram.checking') : t('telegram.check')}
                </Button>
                {(bound || entering) && (
                    <Button
                        variant="ghost"
                        size="sm"
                        disabled={busy}
                        onClick={() => {
                            setEntering(false);
                            setToken('');
                        }}
                    >
                        {t('cancel')}
                    </Button>
                )}
                {offerManaged && (
                    <Button
                        variant="ghost"
                        size="sm"
                        disabled={busy}
                        onClick={() => {
                            setExisting(false);
                            setToken('');
                        }}
                    >
                        {t('telegram.connect.back')}
                    </Button>
                )}
            </div>
        </div>
    );

    // The bot entered and waiting for «Start»: the link, or a way to a new one.
    // A personal bot's «other bot» is the field that names it, not a token.
    const ready = (username: string, change: boolean, created = false) => (
        <div data-testid="telegram-ready" className="flex flex-col gap-2">
            <p className="text-sm font-medium [overflow-wrap:anywhere]">
                {t('telegram.ready.title', { username })}
            </p>
            <p className="text-xs text-muted-foreground">
                {change
                    ? t('telegram.change.pending')
                    : t(created ? 'telegram.managed.readyBody' : 'telegram.ready.body')}
            </p>
            {!linkLive && ownLink !== null && (
                <p className="text-xs text-muted-foreground">{t('telegram.ready.expired')}</p>
            )}
            <div className="flex flex-wrap gap-2">
                {linkLive ? (
                    // The label names the bot, and a username runs to 32
                    // characters: it wraps inside the shared button rather
                    // than running past a phone's edge.
                    <Button
                        size="sm"
                        asChild
                        className="h-auto max-w-full py-1.5 text-left whitespace-normal [overflow-wrap:anywhere]"
                    >
                        <a href={ownLink?.url} target="_blank" rel="noopener noreferrer">
                            {t('telegram.ready.open', { username })}
                        </a>
                    </Button>
                ) : (
                    <Button size="sm" disabled={busy} onClick={freshLink}>
                        {t('telegram.ready.newLink')}
                    </Button>
                )}
                <Button
                    variant="ghost"
                    size="sm"
                    disabled={busy}
                    onClick={() => (created ? setClaiming(true) : setEntering(true))}
                >
                    {t('telegram.ready.otherBot')}
                </Button>
                {!change && !created && (
                    <Button variant="ghost" size="sm" disabled={busy} onClick={disconnect}>
                        {t('telegram.ready.cancel')}
                    </Button>
                )}
            </div>
        </div>
    );

    // W-CONNECT: a personal bot first, a bot of one's own one click away.
    const connect = (
        <div data-testid="telegram-connect" className="flex flex-col gap-2">
            <p className="text-sm font-medium">{t('telegram.connect.title')}</p>
            <p className="text-xs text-muted-foreground">{t('telegram.connect.body')}</p>
            <TelegramVideo />
            <div className="flex flex-wrap gap-2 pt-1">
                <Button size="sm" disabled={busy} onClick={createBot}>
                    {t('telegram.connect.create')}
                </Button>
                <Button variant="ghost" size="sm" disabled={busy} onClick={() => setExisting(true)}>
                    {t('telegram.connect.existing')}
                </Button>
            </div>
        </div>
    );

    // The field naming a created bot whose username the library cannot match.
    const claimForm = (
        <div className="flex flex-col gap-2">
            <label htmlFor={TELEGRAM_CLAIM_FIELD_ID} className="text-xs text-muted-foreground">
                {t('telegram.claim.label')}
            </label>
            <Input
                id={TELEGRAM_CLAIM_FIELD_ID}
                autoComplete="off"
                autoCapitalize="off"
                spellCheck={false}
                placeholder={t('telegram.claim.placeholder')}
                value={claimName}
                disabled={busy}
                onChange={(event) => setClaimName(event.target.value)}
                onKeyDown={(event) => {
                    if (event.key === 'Enter') void claimBot();
                }}
            />
            <div className="flex flex-wrap gap-2">
                <Button size="sm" disabled={busy || !claimName.trim()} onClick={claimBot}>
                    {t('telegram.claim.find')}
                </Button>
                <Button
                    variant="ghost"
                    size="sm"
                    disabled={busy}
                    onClick={() => {
                        setClaiming(false);
                        setClaimName('');
                    }}
                >
                    {t('cancel')}
                </Button>
            </div>
        </div>
    );

    // W-WAIT: the bot is being made in Telegram; the page reads it again by itself.
    const wait = managed && (
        <div data-testid="telegram-wait" className="flex flex-col gap-2">
            <p className="text-sm font-medium">{t('telegram.wait.title')}</p>
            <p className="text-xs text-muted-foreground">{t('telegram.wait.body')}</p>
            {managed.state === 'not_ready' && (
                <p className="text-xs text-muted-foreground">{t('telegram.wait.notReady')}</p>
            )}
            <div className="flex flex-wrap gap-2 pt-1">
                {managed.state === 'not_ready' ? (
                    <Button size="sm" disabled={busy} onClick={() => run(telegramApi.checkManaged)}>
                        {t('telegram.wait.retry')}
                    </Button>
                ) : (
                    managed.create_url && (
                        <Button size="sm" asChild>
                            <a href={managed.create_url} target="_blank" rel="noopener noreferrer">
                                {t('telegram.wait.openTelegram')}
                            </a>
                        </Button>
                    )
                )}
                {!claiming && (
                    <Button
                        variant="outline"
                        size="sm"
                        disabled={busy}
                        onClick={() => setClaiming(true)}
                    >
                        {t('telegram.wait.created')}
                    </Button>
                )}
                <Button variant="ghost" size="sm" disabled={busy} onClick={cancelCreation}>
                    {t('telegram.wait.cancel')}
                </Button>
            </div>
            {claiming && claimForm}
        </div>
    );

    // A bot whose owner changed in Telegram: connected again through a new
    // link, confirmed from the new owner's account.
    const relinkOwn = link !== null && link.bot === status.bot_username ? link : null;
    const relink =
        relinkOwn !== null && !linkExpired ? (
            <Button
                size="sm"
                asChild
                className="h-auto max-w-full py-1.5 text-left whitespace-normal [overflow-wrap:anywhere]"
            >
                <a href={relinkOwn.url} target="_blank" rel="noopener noreferrer">
                    {t('telegram.ready.open', { username: status.bot_username ?? '' })}
                </a>
            </Button>
        ) : (
            <Button size="sm" disabled={busy} onClick={freshLink}>
                {t('telegram.ownerChanged.relink')}
            </Button>
        );

    const restore = () => (managedBound ? void run(telegramApi.restoreManaged) : setEntering(true));

    const botLink = status.bot_username ? `https://t.me/${status.bot_username}` : undefined;

    return (
        <div className="flex flex-col gap-4">
            {state === 'none' &&
                (creating ? wait : offerManaged && !existing ? connect : tokenForm(false))}

            {state === 'pending_link' &&
                (managedBot !== null
                    ? claiming
                        ? claimForm
                        : ready(managedBot, false, true)
                    : entering
                      ? tokenForm(false)
                      : ready(pending ?? '', false))}

            {bound && (
                <div className="flex flex-col gap-2">
                    {state === 'token_rejected' && (
                        <>
                            <p className="text-sm font-medium">{t('telegram.tokenError.title')}</p>
                            <p className="text-xs text-muted-foreground">
                                {t('telegram.tokenError.body')}
                            </p>
                        </>
                    )}
                    {state === 'blocked' && (
                        <p className="text-sm text-destructive">{t('telegram.blocked.body')}</p>
                    )}
                    {state === 'owner_changed' && (
                        <p className="text-sm text-destructive">
                            {t('telegram.ownerChanged.body')}
                        </p>
                    )}
                    <dl className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-sm">
                        <dt className="text-muted-foreground">{t('telegram.connected.bot')}</dt>
                        <dd className="break-all">@{status.bot_username}</dd>
                        {status.telegram_name && (
                            <>
                                <dt className="text-muted-foreground">
                                    {t('telegram.connected.account')}
                                </dt>
                                <dd className="break-words">{status.telegram_name}</dd>
                            </>
                        )}
                    </dl>
                    {!entering && (
                        <div className="flex flex-wrap gap-2 pt-1">
                            {state === 'token_rejected' ? (
                                <Button size="sm" disabled={busy} onClick={restore}>
                                    {t('telegram.tokenError.restore')}
                                </Button>
                            ) : state === 'owner_changed' ? (
                                relink
                            ) : (
                                botLink && (
                                    <Button size="sm" asChild>
                                        <a href={botLink} target="_blank" rel="noopener noreferrer">
                                            {t('telegram.connected.open')}
                                        </a>
                                    </Button>
                                )
                            )}
                            {state !== 'token_rejected' && state !== 'owner_changed' && (
                                <Button
                                    variant="outline"
                                    size="sm"
                                    onClick={() => setEntering(true)}
                                >
                                    {t('telegram.connected.change')}
                                </Button>
                            )}
                            <Button
                                ref={disconnectRef}
                                variant="outline"
                                size="sm"
                                disabled={busy}
                                onClick={() => setConfirmOpen(true)}
                                className={DANGER_OUTLINE}
                            >
                                {t('telegram.connected.disconnect')}
                            </Button>
                        </div>
                    )}
                    {entering && tokenForm(true)}
                    {!entering && pending && ready(pending, true)}
                </div>
            )}

            <ResponsiveSheet
                data-testid="telegram-disconnect"
                open={confirmOpen}
                onOpenChange={(next) => {
                    if (!next) setConfirmOpen(false);
                }}
                title={t('telegram.disconnect.title', { username: status.bot_username ?? '' })}
                description={t('telegram.disconnect.body')}
                closeLabel={t('close')}
                onCloseAutoFocus={(event) => {
                    event.preventDefault();
                    disconnectRef.current?.focus();
                }}
                primary={
                    <Button variant="destructive" disabled={busy} onClick={disconnect}>
                        {t('telegram.disconnect.confirm')}
                    </Button>
                }
                secondary={
                    <Button variant="ghost" onClick={() => setConfirmOpen(false)}>
                        {t('cancel')}
                    </Button>
                }
            />
        </div>
    );
};

export default TelegramSettings;
