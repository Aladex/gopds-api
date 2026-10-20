import { http } from '@/api/http';
import { isApiError } from '@/api/errors';

/** Where the reader's Telegram connection stands. */
export type TelegramState =
    'none' | 'pending_link' | 'connected' | 'token_rejected' | 'blocked' | 'owner_changed';

/**
 * A personal bot being created through the library's manager bot:
 * waiting — the reader was sent to Telegram and no bot has arrived yet;
 * not_ready — a bot arrived and Telegram does not answer for it yet;
 * ready — the bot answers and waits for its «Start» like an entered one.
 */
export interface ManagedConnection {
    state: 'waiting' | 'not_ready' | 'ready';
    /** Telegram's creation window, while waiting. */
    create_url?: string;
    /** The bot that arrived, once one did. */
    bot_username?: string;
    /** When the waiting connection runs out. */
    expires_at: string;
}

/** The reader's Telegram connection, as the server sees it. */
export interface BotStatus {
    state: TelegramState;
    /** The connected bot and the Telegram account it is bound to. */
    bot_username?: string;
    telegram_name?: string;
    /** The format books are sent in from the site, while connected. */
    default_format?: string;
    /** A bot entered and not yet confirmed from Telegram. */
    pending_bot_username?: string;
    /** The one-time link, only in the answer of the call that made it. */
    link_url?: string;
    link_expires_at?: string;
    /** How the bound bot came to be: entered by its token, or created here. */
    bot_kind?: 'manual' | 'managed';
    /** Whether a personal bot can be created from the site. */
    managed_available?: boolean;
    /** A personal bot on its way from Telegram. */
    managed?: ManagedConnection;
    /**
     * Whether the bound bot wears the library's avatar and descriptions:
     * always so for a created bot, for an entered one only once its owner
     * asked for it.
     */
    profile_applied?: boolean;
}

/** The codes the server answers a refused request with. */
export type TelegramErrorCode =
    | 'invalid_token'
    | 'bot_taken'
    | 'telegram_unavailable'
    | 'webhook_failed'
    | 'no_pending_bot'
    | 'managed_unavailable'
    | 'managed_not_found'
    | 'bot_changed'
    | 'internal';

const KNOWN_ERRORS: readonly TelegramErrorCode[] = [
    'invalid_token',
    'bot_taken',
    'telegram_unavailable',
    'webhook_failed',
    'no_pending_bot',
    'managed_unavailable',
    'managed_not_found',
    'bot_changed',
];

/** telegramErrorCode reads the server's code from a failed request. */
export function telegramErrorCode(error: unknown): TelegramErrorCode {
    if (isApiError(error) && error.body && typeof error.body === 'object') {
        const code = (error.body as { error?: unknown }).error;
        if (typeof code === 'string' && (KNOWN_ERRORS as readonly string[]).includes(code)) {
            return code as TelegramErrorCode;
        }
    }
    return 'internal';
}

export const getBotStatus = () => http.get<BotStatus>('/telegram/bot/status');

/** setBotToken checks a bot's token and answers with a link to confirm it. */
export const setBotToken = (token: string) => http.post<BotStatus>('/telegram/bot', { token });

/** newLink gives a fresh link for the bot entered last. */
export const newLink = () => http.post<BotStatus>('/telegram/link', {});

/** removeBotToken disconnects the bot from the library. */
export const removeBotToken = () => http.delete<BotStatus>('/telegram/bot');

/** startManaged begins a personal bot and answers with Telegram's creation window. */
export const startManaged = () => http.post<BotStatus>('/telegram/managed', {});

/** claimManaged names the created bot when its username was changed in Telegram. */
export const claimManaged = (username: string) =>
    http.post<BotStatus>('/telegram/managed/claim', { username });

/** checkManaged asks whether Telegram answers for the bot that arrived. */
export const checkManaged = () => http.post<BotStatus>('/telegram/managed/check', {});

/** cancelManaged drops the waiting connection; the bot in Telegram stays. */
export const cancelManaged = () => http.delete<BotStatus>('/telegram/managed');

/** restoreManaged gets a personal bot a new token after Telegram rejected the old one. */
export const restoreManaged = () => http.post<BotStatus>('/telegram/managed/restore', {});

/**
 * applyProfile gives the connected bot the library's avatar and descriptions,
 * replacing the owner's own. It names the bot the page showed: the server
 * refuses with bot_changed when the account's bot is another one by now.
 */
export const applyProfile = (botUsername: string) =>
    http.post<BotStatus>('/telegram/profile', { bot_username: botUsername });
