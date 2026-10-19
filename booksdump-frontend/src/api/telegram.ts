import { http } from '@/api/http';
import { isApiError } from '@/api/errors';

/** Where the reader's Telegram connection stands. */
export type TelegramState = 'none' | 'pending_link' | 'connected' | 'token_rejected' | 'blocked';

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
}

/** The codes the server answers a refused request with. */
export type TelegramErrorCode =
    | 'invalid_token'
    | 'bot_taken'
    | 'telegram_unavailable'
    | 'webhook_failed'
    | 'no_pending_bot'
    | 'internal';

const KNOWN_ERRORS: readonly TelegramErrorCode[] = [
    'invalid_token',
    'bot_taken',
    'telegram_unavailable',
    'webhook_failed',
    'no_pending_bot',
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
