import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router';

import ProfileContent from '@/features/profile/ProfileContent';

// The phone's header holds no section links, so the one section that is not for
// readers is reached from here. The desktop's row already has it.

const translation = { t: (key: string) => key, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({
    useTranslation: () => translation,
    Trans: ({ i18nKey }: { i18nKey: string }) => i18nKey,
}));

const form = {
    firstName: '',
    lastName: '',
    oldPassword: '',
    newPassword: '',
    showPasswordFields: false,
    user: { username: 'reader', is_superuser: false },
    setFirstName: vi.fn(),
    setLastName: vi.fn(),
    setOldPassword: vi.fn(),
    setNewPassword: vi.fn(),
    handleUserChange: vi.fn(),
    handleDropSessions: vi.fn(),
    handleLogout: vi.fn(),
    togglePasswordFields: vi.fn(),
    resetFields: vi.fn(),
};
vi.mock('@/features/profile/useProfileForm', () => ({ useProfileForm: () => form }));
vi.mock('@/features/kindle/KindleSettings', () => ({
    default: () => null,
    CardButtonSettings: () => null,
}));
vi.mock('@/features/opds/OpdsInfo', () => ({ OpdsConnection: () => null }));
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));
vi.mock('@/shared/layout/ThemeChoice', () => ({ default: () => <div>theme-choice</div> }));

const Where: React.FC = () => <span data-testid="where">{useLocation().pathname}</span>;

const renderProfile = (inSheet: boolean, onClose = vi.fn()) =>
    render(
        <MemoryRouter initialEntries={['/books/page/1']}>
            <Where />
            <ProfileContent open inSheet={inSheet} onClose={onClose} />
        </MemoryRouter>,
    );

describe('ProfileContent', () => {
    beforeEach(() => {
        form.user = { username: 'reader', is_superuser: false };
    });

    it('offers the theme beside the interface language', () => {
        renderProfile(true);

        expect(screen.getByText('theme')).toBeInTheDocument();
        expect(screen.getByText('theme-choice')).toBeInTheDocument();
    });

    it('offers a reader no way to the admin panel', () => {
        renderProfile(true);

        expect(screen.queryByRole('link', { name: 'adminLink' })).toBeNull();
    });

    it('takes a superuser there from the phone and closes behind them', async () => {
        form.user = { username: 'keeper', is_superuser: true };
        const onClose = vi.fn();
        renderProfile(true, onClose);

        await userEvent.click(screen.getByRole('link', { name: 'adminLink' }));

        expect(screen.getByTestId('where')).toHaveTextContent('/admin');
        expect(onClose).toHaveBeenCalled();
    });

    it('leaves it to the row on a desktop', () => {
        form.user = { username: 'keeper', is_superuser: true };
        renderProfile(false);

        expect(screen.queryByRole('link', { name: 'adminLink' })).toBeNull();
    });
});
