import React from 'react';
import { render, screen } from '@testing-library/react';

import ProfileDrawer from '@/features/profile/ProfileDrawer';
import ProfileDialog from '@/features/profile/ProfileDialog';

// The phone's sheet stacks the profile's tabs, icon over label; the desktop's
// dialog does not. The shell is what decides, so it is the shell that is
// rendered here.

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
        user: { username: 'reader' },
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
    default: () => null,
    CardButtonSettings: () => null,
}));
vi.mock('@/features/opds/OpdsInfo', () => ({ OpdsConnection: () => null }));
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));

const mainTabs = () => screen.getAllByRole('tablist')[0];

it('stacks the tabs in the phone’s sheet', () => {
    render(<ProfileDrawer open onClose={vi.fn()} />);

    expect(mainTabs()).toHaveAttribute('data-stacked', 'true');
});

it('keeps them in a row in the desktop’s dialog', () => {
    render(<ProfileDialog open onClose={vi.fn()} />);

    expect(mainTabs()).not.toHaveAttribute('data-stacked');
});
