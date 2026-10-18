import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { toast } from 'sonner';

import KindleSettings from '@/features/kindle/KindleSettings';
import { isKindleAddress, normalizeKindleAddress } from '@/features/kindle/kindleAddress';
import * as kindleApi from '@/api/kindle';
import * as authApi from '@/api/auth';
import type { User } from '@/api/auth';

// The profile's Kindle group: the address is checked before it is sent, saved
// and removed on its own buttons, and the two switches apply as they flip.

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
vi.mock('@/api/auth', () => ({ updateCurrentUser: vi.fn() }));

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
        render(<KindleSettings />);

        expect(screen.getByRole('switch', { name: 'kindle.showKindleButton' })).toBeChecked();
        expect(screen.getByRole('switch', { name: 'kindle.showTelegramButton' })).toBeChecked();
    });

    it('send just the one setting that flipped', async () => {
        const updated = { ...authState.user, show_telegram_button: false };
        vi.mocked(authApi.updateCurrentUser).mockResolvedValue(updated);
        render(<KindleSettings />);

        await userEvent.click(screen.getByRole('switch', { name: 'kindle.showTelegramButton' }));

        expect(authApi.updateCurrentUser).toHaveBeenCalledWith({ show_telegram_button: false });
        await waitFor(() => expect(updateUser).toHaveBeenLastCalledWith(updated));
    });

    it('put the setting back when the server refuses it', async () => {
        authState.user.show_kindle_button = false;
        const before = authState.user;
        vi.mocked(authApi.updateCurrentUser).mockRejectedValue(new Error('500'));
        render(<KindleSettings />);

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
        render(<KindleSettings />);
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

    it('holds the switches while the address is being saved', async () => {
        vi.mocked(kindleApi.setKindleEmail).mockReturnValue(new Promise(() => {}));
        render(<KindleSettings />);

        await userEvent.type(field(), 'reader@kindle.com');
        await userEvent.click(saveButton());

        expect(screen.getByRole('switch', { name: 'kindle.showKindleButton' })).toBeDisabled();
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
