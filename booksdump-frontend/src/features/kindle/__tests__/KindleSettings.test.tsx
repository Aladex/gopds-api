import React from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { toast } from 'sonner';

import KindleSettings, { CardButtonSettings } from '@/features/kindle/KindleSettings';
import { isKindleAddress, normalizeKindleAddress } from '@/features/kindle/kindleAddress';
import * as kindleApi from '@/api/kindle';
import * as authApi from '@/api/auth';
import type { User } from '@/api/auth';

// The profile's Kindle group: the address is checked before it is sent, saved
// and removed on its own buttons. And the card-button switches, on the
// profile's own tab, which apply as they flip and share the address's count of
// changes.

const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({
    useTranslation: () => translation,
    Trans: ({ i18nKey }: { i18nKey: string }) => i18nKey,
}));
vi.mock('sonner', () => ({
    toast: Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn(), info: vi.fn() }),
}));
vi.mock('@/api/kindle', () => ({ setKindleEmail: vi.fn(), clearKindleEmail: vi.fn() }));
vi.mock('@/api/auth', () => ({ updateCurrentUser: vi.fn(), getCurrentUser: vi.fn() }));

const updateUser = vi.fn();
const authState: { user: User; updateUser: typeof updateUser } = {
    user: { username: 'reader', first_name: '', last_name: '', is_superuser: false },
    updateUser,
};
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));

const SENDER = '5f0c7a52-3c1e-4bb4-9a77-0b3c1f6a2d10@booksdump.com';

beforeEach(() => {
    vi.clearAllMocks();
    authState.user = {
        username: 'reader',
        first_name: '',
        last_name: '',
        is_superuser: false,
        kindle_email: '',
        kindle_sender: SENDER,
    };
    // A re-read after a set-aside answer finds the user as the store holds it.
    vi.mocked(authApi.getCurrentUser).mockImplementation(async () => authState.user);
});

const field = () => screen.getByLabelText('kindle.addressLabel');
const saveButton = () => screen.getByRole('button', { name: 'kindle.save' });

describe('the address check', () => {
    it.each([
        ['reader@kindle.com', true],
        ['  Reader@Kindle.COM ', true],
        ['reader@free.kindle.com', true],
        ['reader@gmail.com', false],
        ['reader@kindle.com.evil.test', false],
        ['reader@kindle.co', false],
        ['@kindle.com', false],
        ['Reader <reader@kindle.com>', false],
        ['', false],
    ])('%j is a Kindle address: %s', (value, expected) => {
        expect(isKindleAddress(value)).toBe(expected);
    });

    it('stores what it accepts trimmed and lower-cased', () => {
        expect(normalizeKindleAddress('  Reader@Kindle.COM ')).toBe('reader@kindle.com');
    });
});

describe('the Kindle address field', () => {
    it('says nothing while empty and cannot be saved', () => {
        render(<KindleSettings />);

        expect(field()).not.toHaveAttribute('aria-invalid');
        expect(screen.getByText('kindle.addressHint')).toBeInTheDocument();
        expect(saveButton()).toBeDisabled();
    });

    it('explains a wrong address and refuses to send it', async () => {
        render(<KindleSettings />);

        await userEvent.type(field(), 'reader@gmail.com');

        expect(field()).toHaveAttribute('aria-invalid', 'true');
        expect(screen.getByText('kindle.addressInvalid')).toBeInTheDocument();
        expect(saveButton()).toBeDisabled();
        expect(kindleApi.setKindleEmail).not.toHaveBeenCalled();
    });

    it('saves a good address as it will be stored, and takes the profile back', async () => {
        const updated = { ...authState.user, kindle_email: 'reader@kindle.com' };
        vi.mocked(kindleApi.setKindleEmail).mockResolvedValue(updated);
        render(<KindleSettings />);

        await userEvent.type(field(), '  Reader@Kindle.com ');
        await userEvent.click(saveButton());

        expect(kindleApi.setKindleEmail).toHaveBeenCalledWith('reader@kindle.com');
        await waitFor(() => expect(updateUser).toHaveBeenCalledWith(updated));
        expect(toast.success).toHaveBeenCalledWith('kindle.saved');
    });

    it('says so when the server refuses', async () => {
        vi.mocked(kindleApi.setKindleEmail).mockRejectedValue(new Error('400'));
        render(<KindleSettings />);

        await userEvent.type(field(), 'reader@kindle.com');
        await userEvent.click(saveButton());

        await waitFor(() => expect(toast.error).toHaveBeenCalledWith('kindle.saveError'));
        expect(updateUser).not.toHaveBeenCalled();
    });

    it('removes a saved address', async () => {
        authState.user.kindle_email = 'reader@kindle.com';
        const cleared = { ...authState.user, kindle_email: '' };
        vi.mocked(kindleApi.clearKindleEmail).mockResolvedValue(cleared);
        render(<KindleSettings />);

        expect(field()).toHaveValue('reader@kindle.com');
        // Unchanged, so there is nothing to save.
        expect(saveButton()).toBeDisabled();
        await userEvent.click(screen.getByRole('button', { name: 'kindle.remove' }));

        expect(kindleApi.clearKindleEmail).toHaveBeenCalled();
        await waitFor(() => expect(updateUser).toHaveBeenCalledWith(cleared));
        expect(field()).toHaveValue('');
    });

    it('offers no Remove when there is nothing to remove', () => {
        render(<KindleSettings />);
        expect(screen.queryByRole('button', { name: 'kindle.remove' })).not.toBeInTheDocument();
    });
});

describe('the sender address', () => {
    it('is shown in full, to be copied', async () => {
        const writeText = vi.fn().mockResolvedValue(undefined);
        Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
        render(<KindleSettings />);

        expect(screen.getByTestId('kindle-sender')).toHaveTextContent(SENDER);
        await userEvent.click(screen.getByRole('button', { name: 'kindle.copySender' }));

        expect(writeText).toHaveBeenCalledWith(SENDER);
        expect(await screen.findByText('kindle.copied')).toBeInTheDocument();
    });
});

describe('the card button switches', () => {
    it('start on when the profile says nothing', () => {
        render(<CardButtonSettings />);

        expect(screen.getByRole('switch', { name: 'kindle.showKindleButton' })).toBeChecked();
        expect(screen.getByRole('switch', { name: 'kindle.showTelegramButton' })).toBeChecked();
    });

    it('send just the one setting that flipped', async () => {
        const updated = { ...authState.user, show_telegram_button: false };
        vi.mocked(authApi.updateCurrentUser).mockResolvedValue(updated);
        render(<CardButtonSettings />);

        await userEvent.click(screen.getByRole('switch', { name: 'kindle.showTelegramButton' }));

        expect(authApi.updateCurrentUser).toHaveBeenCalledWith({ show_telegram_button: false });
        await waitFor(() => expect(updateUser).toHaveBeenLastCalledWith(updated));
    });

    it('put the setting back when the server refuses it', async () => {
        authState.user.show_kindle_button = false;
        const before = authState.user;
        vi.mocked(authApi.updateCurrentUser).mockRejectedValue(new Error('500'));
        render(<CardButtonSettings />);

        await userEvent.click(screen.getByRole('switch', { name: 'kindle.showKindleButton' }));

        expect(authApi.updateCurrentUser).toHaveBeenCalledWith({ show_kindle_button: true });
        await waitFor(() => expect(updateUser).toHaveBeenLastCalledWith(before));
        expect(toast.error).toHaveBeenCalledWith('kindle.buttonsSaveError');
    });
});

describe('one change at a time', () => {
    it('holds both switches while one is being saved, so answers cannot cross', async () => {
        let answer: (user: User) => void = () => {};
        vi.mocked(authApi.updateCurrentUser).mockReturnValue(
            new Promise((resolve) => (answer = resolve)),
        );
        render(<CardButtonSettings />);
        const kindle = screen.getByRole('switch', { name: 'kindle.showKindleButton' });
        const telegram = screen.getByRole('switch', { name: 'kindle.showTelegramButton' });

        await userEvent.click(kindle);
        expect(kindle).toBeDisabled();
        expect(telegram).toBeDisabled();
        await userEvent.click(telegram);
        expect(authApi.updateCurrentUser).toHaveBeenCalledTimes(1);

        answer({ ...authState.user, show_kindle_button: false });
        await waitFor(() => expect(telegram).toBeEnabled());
    });

    it('are not among the Kindle settings any more', () => {
        render(<KindleSettings />);

        expect(screen.queryByRole('switch')).not.toBeInTheDocument();
    });

    // Both tabs stay mounted in the profile, so a switch can be flipped while
    // the address is still being saved. Whichever answer is set aside, the
    // user is read again once both have settled and keeps both changes.
    it('keeps an address and a switch changed at the same time', async () => {
        updateUser.mockImplementation((next: User) => {
            authState.user = next;
        });
        let saved: (user: User) => void = () => {};
        vi.mocked(kindleApi.setKindleEmail).mockReturnValue(
            new Promise((resolve) => (saved = resolve)),
        );
        vi.mocked(authApi.updateCurrentUser).mockImplementation(async (patch) => ({
            ...authState.user,
            kindle_email: '',
            ...patch,
        }));
        const stored = {
            ...authState.user,
            kindle_email: 'reader@kindle.com',
            show_telegram_button: false,
        };
        vi.mocked(authApi.getCurrentUser).mockResolvedValue(stored);
        render(
            <>
                <KindleSettings />
                <CardButtonSettings />
            </>,
        );

        await userEvent.type(field(), 'reader@kindle.com');
        await userEvent.click(saveButton());
        const telegram = screen.getByRole('switch', { name: 'kindle.showTelegramButton' });
        expect(telegram).toBeEnabled();
        await userEvent.click(telegram);
        await waitFor(() => expect(authApi.updateCurrentUser).toHaveBeenCalledTimes(1));

        // The address's answer is the older one, and is set aside.
        await act(async () => saved({ ...authState.user, kindle_email: 'reader@kindle.com' }));

        await waitFor(() => expect(authApi.getCurrentUser).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(authState.user).toEqual(stored));
        updateUser.mockReset();
    });
});

describe('the sender address on a narrow screen', () => {
    it('may break before the @ and nowhere in the domain', () => {
        render(<KindleSettings />);
        const code = screen.getByTestId('kindle-sender');

        expect(code.className).not.toMatch(/break-all/);
        expect(code.querySelectorAll('wbr')).toHaveLength(1);
        const domain = code.querySelector('span')!;
        expect(domain).toHaveTextContent('@booksdump.com');
        expect(domain.className).toMatch(/whitespace-nowrap/);
        expect(code).toHaveTextContent(SENDER);
    });
});

describe('a profile closed and opened again while a change is slow', () => {
    // updateUser stands in for AuthContext: it replaces the user, as the
    // real one does.
    afterEach(() => updateUser.mockReset());

    const installStore = () =>
        updateUser.mockImplementation((next: User) => {
            authState.user = next;
        });

    const pending = <T,>() => {
        let resolve: (value: T) => void = () => {};
        let reject: (reason: unknown) => void = () => {};
        const promise = new Promise<T>((res, rej) => {
            resolve = res;
            reject = rej;
        });
        return { promise, resolve, reject };
    };

    it.each([
        ['answers late', 'resolve'],
        ['fails late', 'reject'],
    ] as const)(
        'does not let the old change undo the new one when it %s',
        async (_name, outcome) => {
            installStore();
            const first = pending<User>();
            const second = pending<User>();
            vi.mocked(authApi.updateCurrentUser)
                .mockReturnValueOnce(first.promise)
                .mockReturnValueOnce(second.promise);

            const opening = render(<CardButtonSettings key="first" />);
            await userEvent.click(screen.getByRole('switch', { name: 'kindle.showKindleButton' }));
            opening.unmount();

            render(<CardButtonSettings key="second" />);
            await userEvent.click(
                screen.getByRole('switch', { name: 'kindle.showTelegramButton' }),
            );
            expect(authApi.updateCurrentUser).toHaveBeenCalledTimes(2);

            // The newer answer first, then the older one — a stale snapshot,
            // or a failure whose rollback would restore the user as it was.
            const both = {
                ...authState.user,
                show_kindle_button: false,
                show_telegram_button: false,
            };
            await act(async () => second.resolve(both));
            await act(async () => {
                if (outcome === 'resolve') {
                    first.resolve({ ...both, show_telegram_button: true });
                } else {
                    first.reject(new Error('500'));
                }
            });

            expect(authState.user.show_telegram_button).toBe(false);
            expect(
                screen.getByRole('switch', { name: 'kindle.showTelegramButton' }),
            ).not.toBeChecked();
            expect(toast.error).not.toHaveBeenCalled();
        },
    );

    // The older request may also be the later write. Its answer — false and
    // false, the row as stored — is set aside as stale, and the newer answer
    // still says Kindle is on. Once both have settled the user is read again,
    // once, and the page shows what the database holds.
    it('reads the user again when the older change committed last', async () => {
        installStore();
        const first = pending<User>();
        const second = pending<User>();
        vi.mocked(authApi.updateCurrentUser)
            .mockReturnValueOnce(first.promise)
            .mockReturnValueOnce(second.promise);
        const stored = {
            ...authState.user,
            show_kindle_button: false,
            show_telegram_button: false,
        };
        vi.mocked(authApi.getCurrentUser).mockResolvedValue(stored);

        const opening = render(<CardButtonSettings key="first" />);
        await userEvent.click(screen.getByRole('switch', { name: 'kindle.showKindleButton' }));
        opening.unmount();
        render(<CardButtonSettings key="second" />);
        await userEvent.click(screen.getByRole('switch', { name: 'kindle.showTelegramButton' }));

        // B committed first, before A's write: Kindle still on in its row.
        await act(async () =>
            second.resolve({
                ...authState.user,
                show_kindle_button: true,
                show_telegram_button: false,
            }),
        );
        expect(authApi.getCurrentUser).not.toHaveBeenCalled();
        await act(async () => first.resolve(stored));

        await waitFor(() => expect(authState.user.show_kindle_button).toBe(false));
        expect(authState.user.show_telegram_button).toBe(false);
        expect(authApi.getCurrentUser).toHaveBeenCalledTimes(1);
    });

    it('does not let an old address save overwrite a newer removal', async () => {
        installStore();
        authState.user.kindle_email = 'old@kindle.com';
        const saving = pending<User>();
        vi.mocked(kindleApi.setKindleEmail).mockReturnValueOnce(saving.promise);
        vi.mocked(kindleApi.clearKindleEmail).mockResolvedValueOnce({
            ...authState.user,
            kindle_email: '',
        });

        const opening = render(<KindleSettings key="first" />);
        await userEvent.clear(field());
        await userEvent.type(field(), 'new@kindle.com');
        await userEvent.click(saveButton());
        opening.unmount();

        render(<KindleSettings key="second" />);
        await userEvent.click(screen.getByRole('button', { name: 'kindle.remove' }));
        await waitFor(() => expect(authState.user.kindle_email).toBe(''));

        await act(async () =>
            saving.resolve({ ...authState.user, kindle_email: 'new@kindle.com' }),
        );
        expect(authState.user.kindle_email).toBe('');
    });
});
