import React, { useState } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { toast } from 'sonner';

import TelegramSettings from '@/features/telegram/TelegramSettings';
import * as telegramApi from '@/api/telegram';
import type { BotStatus } from '@/api/telegram';
import { ApiError } from '@/api/errors';

// The library's look for a bot entered by its token: the bot's own avatar and
// descriptions are replaced, so it happens only on a tick and a press, and
// only for a connected bot the reader brought. A created bot wears the look
// from its creation and is offered nothing.

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
    newLink: vi.fn(),
    applyProfile: vi.fn(),
}));

const BOT = 'my_library_bot';

const manual = (extra: Partial<BotStatus> = {}): BotStatus => ({
    state: 'connected',
    bot_username: BOT,
    telegram_name: 'Reader (@reader)',
    bot_kind: 'manual',
    ...extra,
});

/** The profile's role: it holds the status and passes changes back down. */
const Harness: React.FC<{ initial: BotStatus }> = ({ initial }) => {
    const [status, setStatus] = useState<BotStatus>(initial);
    return <TelegramSettings status={status} onStatus={setStatus} />;
};

const box = () => screen.getByRole('checkbox', { name: 'telegram.profile.label' });
const applyButton = () => screen.getByRole('button', { name: 'telegram.profile.apply' });

beforeEach(() => {
    vi.clearAllMocks();
});

describe("the library's look for an entered bot", () => {
    it('is offered for a connected bot entered by its token, unticked', () => {
        render(<Harness initial={manual()} />);

        expect(screen.getByTestId('telegram-profile')).toBeInTheDocument();
        expect(box()).not.toBeChecked();
        expect(screen.getByText('telegram.profile.hint')).toBeInTheDocument();
        expect(screen.queryByText('telegram.profile.applied')).not.toBeInTheDocument();
    });

    it('cannot be applied until the box is ticked', async () => {
        render(<Harness initial={manual()} />);

        expect(applyButton()).toBeDisabled();
        await userEvent.click(applyButton());
        expect(telegramApi.applyProfile).not.toHaveBeenCalled();

        // The word beside the box ticks it too.
        await userEvent.click(screen.getByText('telegram.profile.label'));
        expect(box()).toBeChecked();
        expect(applyButton()).toBeEnabled();

        await userEvent.click(box());
        expect(applyButton()).toBeDisabled();
    });

    it('applies once on a tick and a press, then shows it applied and unticks', async () => {
        vi.mocked(telegramApi.applyProfile).mockResolvedValue(manual({ profile_applied: true }));
        render(<Harness initial={manual()} />);

        await userEvent.click(box());
        await userEvent.click(applyButton());

        expect(await screen.findByText('telegram.profile.applied')).toBeInTheDocument();
        expect(telegramApi.applyProfile).toHaveBeenCalledTimes(1);
        expect(toast.success).toHaveBeenCalledWith('telegram.profile.done');
        // Applying again takes the same deliberate tick.
        expect(box()).not.toBeChecked();
        expect(applyButton()).toBeDisabled();
    });

    it('can be applied again the same way once it is on', async () => {
        vi.mocked(telegramApi.applyProfile).mockResolvedValue(manual({ profile_applied: true }));
        render(<Harness initial={manual({ profile_applied: true })} />);

        expect(screen.getByText('telegram.profile.applied')).toBeInTheDocument();
        expect(applyButton()).toBeDisabled();
        await userEvent.click(box());
        await userEvent.click(applyButton());

        await waitFor(() => expect(telegramApi.applyProfile).toHaveBeenCalledTimes(1));
    });

    it.each([
        ['telegram_unavailable', 502, 'telegram.error.telegram_unavailable'],
        ['no_pending_bot', 409, 'telegram.profile.noBot'],
        ['internal', 500, 'telegram.error.internal'],
    ])('says what went wrong: %s', async (code, status, message) => {
        vi.mocked(telegramApi.applyProfile).mockRejectedValue(
            new ApiError(code, status, { body: { error: code } }),
        );
        render(<Harness initial={manual()} />);

        await userEvent.click(box());
        await userEvent.click(applyButton());

        await waitFor(() => expect(toast.error).toHaveBeenCalledWith(message));
        expect(screen.queryByText('telegram.profile.applied')).not.toBeInTheDocument();
        // Left ticked: the owner already chose, and may press again.
        expect(box()).toBeChecked();
    });

    it('starts unticked for another bot connected meanwhile', async () => {
        const { rerender } = render(
            <TelegramSettings status={manual()} onStatus={() => undefined} />,
        );
        await userEvent.click(box());
        expect(box()).toBeChecked();

        rerender(
            <TelegramSettings
                status={manual({ bot_username: 'another_bot' })}
                onStatus={() => undefined}
            />,
        );
        expect(box()).not.toBeChecked();
        expect(applyButton()).toBeDisabled();
    });

    it.each([
        ['a created bot', manual({ bot_kind: 'managed', profile_applied: true })],
        ['a created bot, look not said', manual({ bot_kind: 'managed' })],
        ['a bot of unknown kind', manual({ bot_kind: undefined })],
        ['no bot', { state: 'none' } as BotStatus],
        ['a bot waiting for its «Start»', { state: 'pending_link', pending_bot_username: BOT }],
        ['a blocked bot', manual({ state: 'blocked' })],
        ['a rejected token', manual({ state: 'token_rejected' })],
        ['a bot whose owner changed', manual({ state: 'owner_changed' })],
        ['a bot being changed', manual({ pending_bot_username: 'next_bot' })],
    ] as [string, BotStatus][])('is not offered for %s', (_, status) => {
        render(<Harness initial={status} />);

        expect(screen.queryByTestId('telegram-profile')).not.toBeInTheDocument();
        expect(screen.queryByText('telegram.profile.applied')).not.toBeInTheDocument();
        expect(
            screen.queryByRole('button', { name: 'telegram.profile.apply' }),
        ).not.toBeInTheDocument();
    });

    it('is not offered while the token form for another bot is open', async () => {
        render(<Harness initial={manual()} />);

        await userEvent.click(screen.getByRole('button', { name: 'telegram.connected.change' }));
        expect(screen.queryByTestId('telegram-profile')).not.toBeInTheDocument();
    });
});
