import React from 'react';
import { render, screen } from '@testing-library/react';

import ProfileContent from '@/features/profile/ProfileContent';

// The setup dialog sends the reader to the profile "on the Kindle section":
// the profile must open on the tab that holds it, not on the account tab.

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
vi.mock('@/features/kindle/KindleSettings', () => ({ default: () => <div>kindle-settings</div> }));
vi.mock('@/features/opds/OpdsInfo', () => ({ OpdsConnection: () => null }));
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));

it('opens on the connections tab, with the Kindle settings in it, when asked for them', () => {
    render(<ProfileContent open section="kindle" onClose={vi.fn()} />);

    expect(screen.getByRole('tab', { name: 'profileTab.connections' })).toHaveAttribute(
        'aria-selected',
        'true',
    );
    expect(screen.getByText('kindle-settings')).toBeInTheDocument();
    expect(screen.getByText('profileSection.kindle')).toBeInTheDocument();
});

it('opens on the account tab otherwise', () => {
    render(<ProfileContent open onClose={vi.fn()} />);

    expect(screen.getByRole('tab', { name: 'profileTab.account' })).toHaveAttribute(
        'aria-selected',
        'true',
    );
});
