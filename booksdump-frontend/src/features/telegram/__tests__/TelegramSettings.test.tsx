import React, { useState } from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { toast } from 'sonner';

import TelegramSettings, { telegramBadge } from '@/features/telegram/TelegramSettings';
import * as telegramApi from '@/api/telegram';
import type { BotStatus } from '@/api/telegram';
import { ApiError } from '@/api/errors';

// The profile's Telegram group: a bot is entered by its token, then confirmed
// from Telegram through a one-time link; a connected bot can be changed —
// the old one working until the new one is confirmed — or disconnected after
// a question.

const translate = (key: string, options?: Record<string, unknown>) =>
    options?.username !== undefined ? `${key}:${String(options.username)}` : key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({
    useTranslation: () => translation,
    Trans: ({ i18nKey }: { i18nKey: string }) => i18nKey,
}));
vi.mock('sonner', () => ({
    toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), info: vi.fn() }),
}));
vi.mock('@/api/telegram', async (importOriginal) => ({
    ...(await importOriginal<typeof import('@/api/telegram')>()),
    getBotStatus: vi.fn(),
    setBotToken: vi.fn(),
    newLink: vi.fn(),
    removeBotToken: vi.fn(),
}));

const inMinutes = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString();

/** The profile's role: it holds the status and passes changes back down. */
const Harness: React.FC<{ initial: BotStatus }> = ({ initial }) => {
    const [status, setStatus] = useState<BotStatus>(initial);
    return <TelegramSettings status={status} onStatus={setStatus} />;
};

beforeEach(() => {
    vi.clearAllMocks();
});

describe('connecting a bot', () => {
    it('checks the token and offers the one-time link', async () => {
        vi.mocked(telegramApi.setBotToken).mockResolvedValue({
            state: 'pending_link',
            pending_bot_username: 'my_library_bot',
            link_url: 'https://t.me/my_library_bot?start=link_abc',
            link_expires_at: inMinutes(15),
        });
        render(<Harness initial={{ state: 'none' }} />);

        expect(screen.getByText('telegram.existing.title')).toBeInTheDocument();
        const check = screen.getByRole('button', { name: 'telegram.check' });
        expect(check).toBeDisabled();
        await userEvent.type(screen.getByLabelText('telegram.tokenLabel'), ' 123:abc ');
        await userEvent.click(check);

        expect(telegramApi.setBotToken).toHaveBeenCalledWith('123:abc');
        const open = await screen.findByRole('link', {
            name: 'telegram.ready.open:my_library_bot',
        });
        expect(open).toHaveAttribute('href', 'https://t.me/my_library_bot?start=link_abc');
        expect(open).toHaveAttribute('target', '_blank');
        expect(screen.getByText('telegram.ready.title:my_library_bot')).toBeInTheDocument();
    });

    it('keeps the BotFather steps for creating a bot', async () => {
        render(<Harness initial={{ state: 'none' }} />);
        const toggle = screen.getByRole('button', { name: 'telegram.create.toggle' });
        expect(toggle).toHaveAttribute('aria-expanded', 'false');
        await userEvent.click(toggle);
        expect(toggle).toHaveAttribute('aria-expanded', 'true');
        expect(screen.getByText('telegram.create.step2')).toBeInTheDocument();
    });

    it.each([
        ['invalid_token', 400],
        ['bot_taken', 409],
        ['telegram_unavailable', 502],
    ])('says what went wrong: %s', async (code, status) => {
        vi.mocked(telegramApi.setBotToken).mockRejectedValue(
            new ApiError(code, status, { body: { error: code } }),
        );
        render(<Harness initial={{ state: 'none' }} />);
        await userEvent.type(screen.getByLabelText('telegram.tokenLabel'), '123:abc');
        await userEvent.click(screen.getByRole('button', { name: 'telegram.check' }));

        await waitFor(() => expect(toast.error).toHaveBeenCalledWith(`telegram.error.${code}`));
        expect(screen.getByText('telegram.existing.title')).toBeInTheDocument();
    });

    it('asks for a new link once the server no longer holds one in this page', async () => {
        vi.mocked(telegramApi.newLink).mockResolvedValue({
            state: 'pending_link',
            pending_bot_username: 'my_library_bot',
            link_url: 'https://t.me/my_library_bot?start=link_fresh',
            link_expires_at: inMinutes(15),
        });
        render(
            <Harness initial={{ state: 'pending_link', pending_bot_username: 'my_library_bot' }} />,
        );

        await userEvent.click(screen.getByRole('button', { name: 'telegram.ready.newLink' }));
        expect(
            await screen.findByRole('link', { name: 'telegram.ready.open:my_library_bot' }),
        ).toHaveAttribute('href', 'https://t.me/my_library_bot?start=link_fresh');
    });
});

describe('a connected bot', () => {
    const connected: BotStatus = {
        state: 'connected',
        bot_username: 'my_library_bot',
        telegram_name: 'Reader (@reader)',
    };

    it('shows the bot and the Telegram account it serves', () => {
        render(<Harness initial={connected} />);
        expect(screen.getByText('@my_library_bot')).toBeInTheDocument();
        expect(screen.getByText('Reader (@reader)')).toBeInTheDocument();
        expect(screen.getByRole('link', { name: 'telegram.connected.open' })).toHaveAttribute(
            'href',
            'https://t.me/my_library_bot',
        );
    });

    it('disconnects only after the question is answered', async () => {
        vi.mocked(telegramApi.removeBotToken).mockResolvedValue({ state: 'none' });
        render(<Harness initial={connected} />);

        await userEvent.click(
            screen.getByRole('button', { name: 'telegram.connected.disconnect' }),
        );
        expect(telegramApi.removeBotToken).not.toHaveBeenCalled();
        const sheet = await screen.findByRole('dialog');
        expect(
            within(sheet).getByText('telegram.disconnect.title:my_library_bot'),
        ).toBeInTheDocument();
        expect(within(sheet).getByText('telegram.disconnect.body')).toBeInTheDocument();

        await userEvent.click(within(sheet).getByRole('button', { name: 'cancel' }));
        expect(telegramApi.removeBotToken).not.toHaveBeenCalled();

        await userEvent.click(
            screen.getByRole('button', { name: 'telegram.connected.disconnect' }),
        );
        await userEvent.click(
            within(await screen.findByRole('dialog')).getByRole('button', {
                name: 'telegram.disconnect.confirm',
            }),
        );
        await waitFor(() => expect(telegramApi.removeBotToken).toHaveBeenCalledTimes(1));
        expect(toast.success).toHaveBeenCalledWith('telegram.disconnected');
        expect(await screen.findByText('telegram.existing.title')).toBeInTheDocument();
    });

    it('changes the bot while the current one keeps working', async () => {
        vi.mocked(telegramApi.setBotToken).mockResolvedValue({
            ...connected,
            pending_bot_username: 'new_library_bot',
            link_url: 'https://t.me/new_library_bot?start=link_xyz',
            link_expires_at: inMinutes(15),
        });
        render(<Harness initial={connected} />);

        await userEvent.click(screen.getByRole('button', { name: 'telegram.connected.change' }));
        expect(screen.getByText('telegram.change.body')).toBeInTheDocument();
        await userEvent.type(screen.getByLabelText('telegram.tokenLabel'), '456:def');
        await userEvent.click(screen.getByRole('button', { name: 'telegram.check' }));

        expect(
            await screen.findByRole('link', { name: 'telegram.ready.open:new_library_bot' }),
        ).toBeInTheDocument();
        expect(screen.getByText('telegram.change.pending')).toBeInTheDocument();
        expect(screen.getByText('@my_library_bot')).toBeInTheDocument();
        expect(
            screen.queryByRole('button', { name: 'telegram.ready.cancel' }),
            'cancelling a change must not disconnect the working bot',
        ).not.toBeInTheDocument();
    });

    it('offers to restore a bot whose token Telegram rejected', async () => {
        render(<Harness initial={{ ...connected, state: 'token_rejected' }} />);
        expect(screen.getByText('telegram.tokenError.title')).toBeInTheDocument();
        await userEvent.click(screen.getByRole('button', { name: 'telegram.tokenError.restore' }));
        expect(screen.getByLabelText('telegram.tokenLabel')).toBeInTheDocument();
    });

    it('says when the bot cannot write to the reader', () => {
        render(<Harness initial={{ ...connected, state: 'blocked' }} />);
        expect(screen.getByText('telegram.blocked.body')).toBeInTheDocument();
    });
});

describe('the badge', () => {
    it.each([
        [{ state: 'connected' } as BotStatus, true, 'telegram.badge.connected'],
        [{ state: 'pending_link' } as BotStatus, false, 'telegram.badge.pending'],
        [{ state: 'blocked' } as BotStatus, false, 'telegram.badge.attention'],
        [{ state: 'token_rejected' } as BotStatus, false, 'telegram.badge.attention'],
        [{ state: 'none' } as BotStatus, false, 'telegram.badge.none'],
        [null, false, 'telegram.badge.none'],
    ])('%o', (status, ok, key) => {
        expect(telegramBadge(status)).toEqual({ ok, key });
    });
});
