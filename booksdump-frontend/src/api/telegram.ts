import { http } from './http';

/** Per-user Telegram bot configuration. */

export interface BotStatus {
    has_bot_token: boolean;
}

export const getBotStatus = () => http.get<BotStatus>('/telegram/bot/status');

export const setBotToken = (token: string) => http.post<unknown>('/telegram/bot', { token });

export const removeBotToken = () => http.delete<unknown>('/telegram/bot');
