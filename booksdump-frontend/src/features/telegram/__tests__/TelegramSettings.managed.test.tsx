import React, { useState } from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { toast } from 'sonner';

import TelegramSettings, {
    PENDING_POLL_MS,
    telegramBadge,
    useTelegramConnection,
} from '@/features/telegram/TelegramSettings';
import * as telegramApi from '@/api/telegram';
import type { BotStatus } from '@/api/telegram';
import { ApiError } from '@/api/errors';

// A personal bot made through the library's manager bot: the profile offers
// it first, sends the reader to Telegram to confirm the creation, waits for
// the bot to arrive, and confirms it with the same one-time link an entered
// bot gets. The token form stays one click away.

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
    startManaged: vi.fn(),
    claimManaged: vi.fn(),
    checkManaged: vi.fn(),
    cancelManaged: vi.fn(),
    restoreManaged: vi.fn(),
}));

const inMinutes = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString();

const CREATE_URL = 'https://t.me/newbot/booksdump_manager_Bot/bdabcdefghbot?name=Books';
const BOT = 'bdabcdefghbot';

const offer: BotStatus = { state: 'none', managed_available: true };
const waiting = (): BotStatus => ({
    state: 'none',
    managed_available: true,
    managed: { state: 'waiting', create_url: CREATE_URL, expires_at: inMinutes(15) },
});
const notReady = (): BotStatus => ({
    state: 'none',
    managed_available: true,
    managed: { state: 'not_ready', bot_username: BOT, expires_at: inMinutes(15) },
});
const ready = (): BotStatus => ({
    state: 'pending_link',
    pending_bot_username: BOT,
    managed_available: true,
    managed: { state: 'ready', bot_username: BOT, expires_at: inMinutes(15) },
});
const linked = (): BotStatus => ({
    ...ready(),
    link_url: `https://t.me/${BOT}?start=link_abc`,
    link_expires_at: inMinutes(15),
});

/** The profile's role: it holds the status and passes changes back down. */
const Harness: React.FC<{ initial: BotStatus }> = ({ initial }) => {
    const [status, setStatus] = useState<BotStatus>(initial);
    return <TelegramSettings status={status} onStatus={setStatus} />;
};

/** The profile's wiring: the hook reads, the settings act. */
const Live: React.FC = () => {
    const { status, setStatus } = useTelegramConnection(true);
    return <TelegramSettings status={status} onStatus={setStatus} />;
};

beforeEach(() => {
    vi.clearAllMocks();
});

afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

describe('the offer of a personal bot', () => {
    it('comes first where the library can make one', () => {
        render(<Harness initial={offer} />);

        expect(screen.getByText('telegram.connect.title')).toBeInTheDocument();
        expect(screen.getByText('telegram.connect.body')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'telegram.connect.create' })).toBeInTheDocument();
        expect(screen.queryByLabelText('telegram.tokenLabel')).not.toBeInTheDocument();
    });

    it.each([
        ['off', { state: 'none', managed_available: false } as BotStatus],
        ['not said', { state: 'none' } as BotStatus],
    ])('is not made where the manager is %s: the token form is', (_, status) => {
        render(<Harness initial={status} />);

        expect(screen.getByText('telegram.existing.title')).toBeInTheDocument();
        expect(screen.getByLabelText('telegram.tokenLabel')).toBeInTheDocument();
        expect(
            screen.queryByRole('button', { name: 'telegram.connect.create' }),
        ).not.toBeInTheDocument();
        expect(
            screen.queryByRole('button', { name: 'telegram.connect.back' }),
        ).not.toBeInTheDocument();
    });

    it('leaves the token form, with its BotFather steps, one click away and back', async () => {
        render(<Harness initial={offer} />);

        await userEvent.click(screen.getByRole('button', { name: 'telegram.connect.existing' }));
        expect(screen.getByText('telegram.existing.title')).toBeInTheDocument();
        expect(screen.getByLabelText('telegram.tokenLabel')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'telegram.create.toggle' })).toBeInTheDocument();

        await userEvent.click(screen.getByRole('button', { name: 'telegram.connect.back' }));
        expect(screen.getByText('telegram.connect.title')).toBeInTheDocument();
    });
});

describe('creating the bot', () => {
    /** A window the browser opened at the click, as window.open gives it. */
    const fakeWindow = () => ({
        opener: {} as unknown,
        location: { href: '' },
        close: vi.fn(),
    });

    it('opens the window at the click and sends it to Telegram once the server answers', async () => {
        const opened = fakeWindow();
        const open = vi.spyOn(window, 'open').mockReturnValue(opened as unknown as Window);
        let answer: (status: BotStatus) => void = () => {};
        vi.mocked(telegramApi.startManaged).mockImplementation(
            () => new Promise<BotStatus>((resolve) => (answer = resolve)),
        );
        render(<Harness initial={offer} />);

        await userEvent.click(screen.getByRole('button', { name: 'telegram.connect.create' }));
        // Opened before the answer: a window opened after it is a popup.
        expect(open).toHaveBeenCalledWith('', '_blank');
        expect(opened.opener).toBeNull();
        expect(opened.location.href).toBe('');

        await act(async () => answer(waiting()));

        expect(opened.location.href).toBe(CREATE_URL);
        expect(opened.close).not.toHaveBeenCalled();
        expect(screen.getByText('telegram.wait.title')).toBeInTheDocument();
        expect(screen.getByRole('link', { name: 'telegram.wait.openTelegram' })).toHaveAttribute(
            'href',
            CREATE_URL,
        );
    });

    it('still leads to Telegram when the browser blocked the window', async () => {
        vi.spyOn(window, 'open').mockReturnValue(null);
        vi.mocked(telegramApi.startManaged).mockResolvedValue(waiting());
        render(<Harness initial={offer} />);

        await userEvent.click(screen.getByRole('button', { name: 'telegram.connect.create' }));

        const link = await screen.findByRole('link', { name: 'telegram.wait.openTelegram' });
        expect(link).toHaveAttribute('href', CREATE_URL);
        expect(link).toHaveAttribute('target', '_blank');
    });

    it('closes the window it opened when the server refuses', async () => {
        const opened = fakeWindow();
        vi.spyOn(window, 'open').mockReturnValue(opened as unknown as Window);
        vi.mocked(telegramApi.startManaged).mockRejectedValue(
            new ApiError('managed_unavailable', 409, { body: { error: 'managed_unavailable' } }),
        );
        render(<Harness initial={offer} />);

        await userEvent.click(screen.getByRole('button', { name: 'telegram.connect.create' }));

        await waitFor(() =>
            expect(toast.error).toHaveBeenCalledWith('telegram.error.managed_unavailable'),
        );
        expect(opened.close).toHaveBeenCalledTimes(1);
        expect(opened.location.href).toBe('');
        expect(screen.getByText('telegram.connect.title')).toBeInTheDocument();
    });
});

describe('waiting for the bot', () => {
    it('reads the connection again until the bot is ready, then holds its link', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        vi.mocked(telegramApi.getBotStatus)
            .mockResolvedValueOnce(waiting())
            .mockResolvedValue(ready());
        vi.mocked(telegramApi.newLink).mockResolvedValue(linked());
        render(<Live />);

        expect(await screen.findByText('telegram.wait.title')).toBeInTheDocument();
        expect(screen.getByText('telegram.wait.body')).toBeInTheDocument();

        await vi.advanceTimersByTimeAsync(PENDING_POLL_MS);

        const open = await screen.findByRole('link', { name: `telegram.ready.open:${BOT}` });
        expect(open).toHaveAttribute('href', `https://t.me/${BOT}?start=link_abc`);
        expect(screen.getByText(`telegram.ready.title:${BOT}`)).toBeInTheDocument();
        expect(screen.getByText('telegram.managed.readyBody')).toBeInTheDocument();

        // Asked for once, not on every read.
        await vi.advanceTimersByTimeAsync(PENDING_POLL_MS * 2);
        expect(telegramApi.newLink).toHaveBeenCalledTimes(1);
    });

    it('goes back to the offer once the waiting connection has run out', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        vi.mocked(telegramApi.getBotStatus)
            .mockResolvedValueOnce(waiting())
            .mockResolvedValue(offer);
        render(<Live />);
        await screen.findByText('telegram.wait.title');

        await vi.advanceTimersByTimeAsync(PENDING_POLL_MS);

        expect(await screen.findByText('telegram.connect.title')).toBeInTheDocument();
        expect(screen.queryByText('telegram.wait.title')).not.toBeInTheDocument();
    });

    it('names a bot whose username was changed in Telegram, and says when it is not ours', async () => {
        vi.mocked(telegramApi.claimManaged).mockRejectedValue(
            new ApiError('managed_not_found', 404, { body: { error: 'managed_not_found' } }),
        );
        render(<Harness initial={waiting()} />);

        await userEvent.click(screen.getByRole('button', { name: 'telegram.wait.created' }));
        const find = screen.getByRole('button', { name: 'telegram.claim.find' });
        expect(find).toBeDisabled();
        await userEvent.type(screen.getByLabelText('telegram.claim.label'), ' @someone_bot ');
        await userEvent.click(find);

        expect(telegramApi.claimManaged).toHaveBeenCalledWith('@someone_bot');
        await waitFor(() =>
            expect(toast.error).toHaveBeenCalledWith('telegram.error.managed_not_found'),
        );
        expect(screen.getByLabelText('telegram.claim.label')).toHaveValue(' @someone_bot ');
        expect(screen.getByText('telegram.wait.title')).toBeInTheDocument();
    });

    it('takes a named bot to the ready screen', async () => {
        vi.mocked(telegramApi.claimManaged).mockResolvedValue(ready());
        vi.mocked(telegramApi.newLink).mockResolvedValue(linked());
        render(<Harness initial={waiting()} />);

        await userEvent.click(screen.getByRole('button', { name: 'telegram.wait.created' }));
        await userEvent.type(screen.getByLabelText('telegram.claim.label'), BOT);
        await userEvent.click(screen.getByRole('button', { name: 'telegram.claim.find' }));

        expect(
            await screen.findByRole('link', { name: `telegram.ready.open:${BOT}` }),
        ).toBeInTheDocument();
        expect(screen.queryByLabelText('telegram.claim.label')).not.toBeInTheDocument();
    });

    it('cancels the waiting connection', async () => {
        vi.mocked(telegramApi.cancelManaged).mockResolvedValue(offer);
        render(<Harness initial={waiting()} />);

        await userEvent.click(screen.getByRole('button', { name: 'telegram.wait.cancel' }));

        expect(telegramApi.cancelManaged).toHaveBeenCalledTimes(1);
        expect(await screen.findByText('telegram.connect.title')).toBeInTheDocument();
    });

    it('says when Telegram does not answer for the bot, and checks again on request', async () => {
        vi.mocked(telegramApi.checkManaged).mockResolvedValue(ready());
        vi.mocked(telegramApi.newLink).mockResolvedValue(linked());
        render(<Harness initial={notReady()} />);

        expect(screen.getByText('telegram.wait.title')).toBeInTheDocument();
        expect(screen.getByText('telegram.wait.notReady')).toBeInTheDocument();
        await userEvent.click(screen.getByRole('button', { name: 'telegram.wait.retry' }));

        expect(telegramApi.checkManaged).toHaveBeenCalledTimes(1);
        expect(
            await screen.findByRole('link', { name: `telegram.ready.open:${BOT}` }),
        ).toBeInTheDocument();
    });

    it('keeps reading while Telegram does not answer', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        vi.mocked(telegramApi.getBotStatus).mockResolvedValue(notReady());
        render(<Live />);
        await screen.findByText('telegram.wait.notReady');
        const reads = vi.mocked(telegramApi.getBotStatus).mock.calls.length;

        await vi.advanceTimersByTimeAsync(PENDING_POLL_MS);

        expect(vi.mocked(telegramApi.getBotStatus).mock.calls.length).toBeGreaterThan(reads);
    });
});

describe('a ready bot', () => {
    it('gets its link without a click, once', async () => {
        vi.mocked(telegramApi.newLink).mockResolvedValue(linked());
        const { rerender } = render(<Harness initial={ready()} />);

        expect(
            await screen.findByRole('link', { name: `telegram.ready.open:${BOT}` }),
        ).toHaveAttribute('href', `https://t.me/${BOT}?start=link_abc`);
        rerender(<Harness initial={ready()} />);
        expect(telegramApi.newLink).toHaveBeenCalledTimes(1);
    });

    it('falls back to the button when the link cannot be had', async () => {
        vi.mocked(telegramApi.newLink).mockRejectedValue(
            new ApiError('internal', 500, { body: { error: 'internal' } }),
        );
        render(<Harness initial={ready()} />);

        await waitFor(() => expect(toast.error).toHaveBeenCalledWith('telegram.error.internal'));
        expect(screen.getByRole('button', { name: 'telegram.ready.newLink' })).toBeInTheDocument();
        expect(telegramApi.newLink).toHaveBeenCalledTimes(1);
    });

    it('takes «another bot» to the field that names it, not to a token', async () => {
        vi.mocked(telegramApi.newLink).mockResolvedValue(linked());
        render(<Harness initial={ready()} />);
        await screen.findByRole('link', { name: `telegram.ready.open:${BOT}` });

        await userEvent.click(screen.getByRole('button', { name: 'telegram.ready.otherBot' }));

        expect(screen.getByLabelText('telegram.claim.label')).toBeInTheDocument();
        expect(screen.queryByLabelText('telegram.tokenLabel')).not.toBeInTheDocument();
    });
});

describe('a bot whose owner changed', () => {
    const ownerChanged: BotStatus = {
        state: 'owner_changed',
        bot_username: BOT,
        bot_kind: 'managed',
        telegram_name: 'Reader',
    };

    it('asks to connect it again and offers the link for it', async () => {
        vi.mocked(telegramApi.newLink).mockResolvedValue({
            ...ownerChanged,
            link_url: `https://t.me/${BOT}?start=link_again`,
            link_expires_at: inMinutes(15),
        });
        render(<Harness initial={ownerChanged} />);

        expect(screen.getByText('telegram.ownerChanged.body')).toBeInTheDocument();
        expect(
            screen.queryByRole('link', { name: 'telegram.connected.open' }),
        ).not.toBeInTheDocument();
        await userEvent.click(screen.getByRole('button', { name: 'telegram.ownerChanged.relink' }));

        expect(telegramApi.newLink).toHaveBeenCalledTimes(1);
        expect(
            await screen.findByRole('link', { name: `telegram.ready.open:${BOT}` }),
        ).toHaveAttribute('href', `https://t.me/${BOT}?start=link_again`);
    });

    it('is read again until connected, and the reader is told', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        vi.mocked(telegramApi.getBotStatus)
            .mockResolvedValueOnce(ownerChanged)
            .mockResolvedValue({ ...ownerChanged, state: 'connected' });
        render(<Live />);
        await screen.findByText('telegram.ownerChanged.body');

        await vi.advanceTimersByTimeAsync(PENDING_POLL_MS);

        await waitFor(() => expect(toast.success).toHaveBeenCalledWith(`telegram.linked:${BOT}`));
    });
});

describe('a rejected token', () => {
    it('is restored for a personal bot through the library, without a token', async () => {
        vi.mocked(telegramApi.restoreManaged).mockResolvedValue({
            state: 'connected',
            bot_username: BOT,
            bot_kind: 'managed',
        });
        render(
            <Harness
                initial={{ state: 'token_rejected', bot_username: BOT, bot_kind: 'managed' }}
            />,
        );
        expect(screen.getByText('telegram.tokenError.title')).toBeInTheDocument();
        expect(screen.getByText('telegram.tokenError.body')).toBeInTheDocument();

        await userEvent.click(screen.getByRole('button', { name: 'telegram.tokenError.restore' }));

        expect(telegramApi.restoreManaged).toHaveBeenCalledTimes(1);
        expect(screen.queryByLabelText('telegram.tokenLabel')).not.toBeInTheDocument();
        expect(
            await screen.findByRole('link', { name: 'telegram.connected.open' }),
        ).toBeInTheDocument();
    });

    it('is restored for an entered bot with a new token, as before', async () => {
        render(
            <Harness
                initial={{ state: 'token_rejected', bot_username: BOT, bot_kind: 'manual' }}
            />,
        );

        await userEvent.click(screen.getByRole('button', { name: 'telegram.tokenError.restore' }));

        expect(screen.getByLabelText('telegram.tokenLabel')).toBeInTheDocument();
        expect(telegramApi.restoreManaged).not.toHaveBeenCalled();
    });

    it('says what went wrong when the library cannot restore it', async () => {
        vi.mocked(telegramApi.restoreManaged).mockRejectedValue(
            new ApiError('managed_unavailable', 409, { body: { error: 'managed_unavailable' } }),
        );
        render(
            <Harness
                initial={{ state: 'token_rejected', bot_username: BOT, bot_kind: 'managed' }}
            />,
        );

        await userEvent.click(screen.getByRole('button', { name: 'telegram.tokenError.restore' }));

        await waitFor(() =>
            expect(toast.error).toHaveBeenCalledWith('telegram.error.managed_unavailable'),
        );
        expect(screen.getByText('telegram.tokenError.title')).toBeInTheDocument();
    });
});

describe('the badge', () => {
    it.each([
        [waiting(), 'telegram.badge.pending'],
        [notReady(), 'telegram.badge.pending'],
        [ready(), 'telegram.badge.pending'],
        [{ state: 'owner_changed' } as BotStatus, 'telegram.badge.attention'],
        [offer, 'telegram.badge.none'],
    ])('%o', (status, key) => {
        expect(telegramBadge(status)).toEqual({ ok: false, key });
    });
});

describe('the video of creating the bot', () => {
    /**
     * Image as the browser has it, for the probe of the poster: jsdom loads
     * no images, so whether the file is there is said here.
     */
    const imagesThat = (outcome: 'load' | 'error') => {
        const asked: string[] = [];
        class Probe {
            onload: (() => void) | null = null;
            onerror: (() => void) | null = null;
            set src(url: string) {
                asked.push(url);
                queueMicrotask(() => (outcome === 'load' ? this.onload : this.onerror)?.());
            }
        }
        vi.stubGlobal('Image', Probe);
        return asked;
    };

    it('is not there, nor its toggle, while the file is missing', async () => {
        const asked = imagesThat('error');
        render(<Harness initial={offer} />);

        await waitFor(() => expect(asked).toEqual(['/videos/telegram-setup-ru.jpg']));
        await act(async () => {});
        expect(screen.queryByTestId('telegram-video')).not.toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'telegram.video.watch' })).toBeNull();
        expect(document.querySelector('video')).toBeNull();
    });

    it('is offered beside the steps once the file is there', async () => {
        imagesThat('load');
        render(<Harness initial={offer} />);

        expect(await screen.findByRole('button', { name: 'telegram.video.watch' })).toHaveAttribute(
            'aria-expanded',
            'false',
        );
        // Not built until asked for.
        expect(document.querySelector('video')).toBeNull();
    });
});
