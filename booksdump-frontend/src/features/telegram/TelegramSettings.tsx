import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import * as telegramApi from '@/api/telegram';
import type { BotStatus } from '@/api/telegram';
import ResponsiveSheet from '@/shared/components/ResponsiveSheet';
import { Button } from '@/shared/ui/button';
import { Expandable } from '@/shared/ui/expandable';
import { Input } from '@/shared/ui/input';

export const TELEGRAM_TOKEN_FIELD_ID = 'profile-bot-token';

/** The outline-and-red look the profile gives its irreversible buttons. */
const DANGER_OUTLINE =
    'border-destructive/50 text-destructive hover:bg-destructive/10 hover:text-destructive';

/** How often a waiting connection is read again while the profile is open. */
export const PENDING_POLL_MS = 3000;

/** waitsForTelegram: a bot was entered and its «Start» has not come yet. */
const waitsForTelegram = (status: BotStatus | null) =>
    status?.state === 'pending_link' || Boolean(status?.pending_bot_username);

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
        try {
            apply(await telegramApi.getBotStatus());
        } catch {
            // Left as it was: the next reading tries again.
        }
    }, [apply]);
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

    return { status, setStatus: apply };
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
            return { ok: false, key: 'telegram.badge.attention' };
        default:
            return { ok: false, key: 'telegram.badge.none' };
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

    const apply = (next: BotStatus) => {
        onStatus(next);
        if (next.link_url) {
            setLink({ url: next.link_url, bot: next.pending_bot_username ?? '' });
            setLinkExpired(false);
        }
    };

    const fail = (error: unknown) => {
        toast.error(t(`telegram.error.${telegramApi.telegramErrorCode(error)}`));
    };

    const checkToken = async () => {
        if (!token.trim()) return;
        setBusy(true);
        try {
            apply(await telegramApi.setBotToken(token.trim()));
            setToken('');
            setEntering(false);
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
    const bound = state === 'connected' || state === 'blocked' || state === 'token_rejected';
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
            </div>
        </div>
    );

    // The bot entered and waiting for «Start»: the link, or a way to a new one.
    const ready = (username: string, change: boolean) => (
        <div data-testid="telegram-ready" className="flex flex-col gap-2">
            <p className="text-sm font-medium [overflow-wrap:anywhere]">
                {t('telegram.ready.title', { username })}
            </p>
            <p className="text-xs text-muted-foreground">
                {change ? t('telegram.change.pending') : t('telegram.ready.body')}
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
                <Button variant="ghost" size="sm" disabled={busy} onClick={() => setEntering(true)}>
                    {t('telegram.ready.otherBot')}
                </Button>
                {!change && (
                    <Button variant="ghost" size="sm" disabled={busy} onClick={disconnect}>
                        {t('telegram.ready.cancel')}
                    </Button>
                )}
            </div>
        </div>
    );

    const botLink = status.bot_username ? `https://t.me/${status.bot_username}` : undefined;

    return (
        <div className="flex flex-col gap-4">
            {state === 'none' && tokenForm(false)}

            {state === 'pending_link' &&
                (entering ? tokenForm(false) : ready(pending ?? '', false))}

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
                                <Button size="sm" onClick={() => setEntering(true)}>
                                    {t('telegram.tokenError.restore')}
                                </Button>
                            ) : (
                                botLink && (
                                    <Button size="sm" asChild>
                                        <a href={botLink} target="_blank" rel="noopener noreferrer">
                                            {t('telegram.connected.open')}
                                        </a>
                                    </Button>
                                )
                            )}
                            {state !== 'token_rejected' && (
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
