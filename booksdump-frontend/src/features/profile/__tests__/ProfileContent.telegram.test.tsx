import React from 'react';
import { render, screen } from '@testing-library/react';

import ProfileContent from '@/features/profile/ProfileContent';

// The bot's «Open profile» button sends the reader to the profile "on the
// Telegram section": the profile must open on the connections tab, on its
// Telegram part, with the connection's state in the badge.

const translation = { t: (key: string) => key, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({
    useTranslation: () => translation,
    Trans: ({ i18nKey }: { i18nKey: string }) => i18nKey,
}));
vi.mock('@/features/profile/useProfileForm', () => ({
    useProfileForm: () => ({
        firstName: '',
        lastName: '',
        oldPassword: '',
        newPassword: '',
        showPasswordFields: false,
        botToken: '',
        botConnected: false,
        botLoading: false,
        user: { username: 'reader', kindle_email: '' },
        setFirstName: vi.fn(),
        setLastName: vi.fn(),
        setOldPassword: vi.fn(),
        setNewPassword: vi.fn(),
        setBotToken: vi.fn(),
        handleUserChange: vi.fn(),
        handleDropSessions: vi.fn(),
        handleLogout: vi.fn(),
        handleSetBotToken: vi.fn(),
        handleRemoveBotToken: vi.fn(),
        togglePasswordFields: vi.fn(),
        resetFields: vi.fn(),
    }),
}));
vi.mock('@/features/kindle/KindleSettings', () => ({
    default: () => <div>kindle-settings</div>,
    CardButtonSettings: () => <div>card-button-settings</div>,
}));
vi.mock('@/features/telegram/TelegramSettings', () => ({
    default: () => <div>telegram-settings</div>,
    useTelegramConnection: () => ({ status: { state: 'connected' }, setStatus: vi.fn() }),
    telegramBadge: () => ({ ok: true, key: 'telegram.badge.connected' }),
}));
vi.mock('@/features/opds/OpdsInfo', () => ({ OpdsConnection: () => <div>opds-connection</div> }));
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));

it('opens on the Telegram connection when asked for it', () => {
    render(<ProfileContent open section="telegram" onClose={vi.fn()} />);

    expect(screen.getByRole('tab', { name: 'profileTab.connections' })).toHaveAttribute(
        'aria-selected',
        'true',
    );
    expect(screen.getByRole('tab', { name: 'profileConnectionTab.telegram' })).toHaveAttribute(
        'aria-selected',
        'true',
    );
    expect(screen.getByText('telegram-settings')).toBeInTheDocument();
    expect(screen.getByTestId('telegram-badge')).toHaveTextContent('telegram.badge.connected');
});
