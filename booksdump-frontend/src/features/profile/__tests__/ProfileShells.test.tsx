import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';

import ProfileDrawer from '@/features/profile/ProfileDrawer';
import { PROFILE_FIRST_FIELD_ID } from '@/features/profile/ProfileContent';
import ProfileDialog from '@/features/profile/ProfileDialog';
import type { ProfileSection } from '@/features/profile/ProfileOpenerContext';

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
vi.mock('@/shared/layout/ThemeChoice', () => ({ default: () => null }));

const mainTabs = () => screen.getAllByRole('tablist')[0];

it('stacks the tabs in the phone’s sheet', () => {
    render(<ProfileDrawer open onClose={vi.fn()} />);

    expect(mainTabs()).toHaveAttribute('data-stacked', 'true');
});

it('keeps them in a row in the desktop’s dialog', () => {
    render(<ProfileDialog open onClose={vi.fn()} />);

    expect(mainTabs()).not.toHaveAttribute('data-stacked');
});

describe('focus in the desktop’s dialog', () => {
    const Page: React.FC<{ open: boolean; section?: ProfileSection }> = ({ open, section }) => (
        <>
            <button>opener</button>
            <ProfileDialog open={open} section={section} onClose={vi.fn()} />
        </>
    );

    const openFromTheButton = (section?: ProfileSection) => {
        const view = render(<Page open={false} section={section} />);
        screen.getByRole('button', { name: 'opener' }).focus();
        view.rerender(<Page open section={section} />);
        return view;
    };

    it.each(['kindle', 'telegram', 'opds'] as const)(
        'takes focus on the dialog itself when opened on %s',
        (section) => {
            // Not "somewhere inside": the name field stays mounted in its hidden
            // panel, and jsdom, which has no layout, focuses it where a browser
            // refuses and leaves focus on the page behind.
            openFromTheButton(section);

            expect(document.activeElement).toBe(screen.getByRole('dialog'));
        },
    );

    it('still lands on the first field on a plain opening, not the logout', () => {
        openFromTheButton();

        expect(document.activeElement).toBe(document.getElementById(PROFILE_FIRST_FIELD_ID));
    });

    it('goes back to what opened it', async () => {
        const view = openFromTheButton('kindle');
        expect(screen.queryByRole('dialog')).toBeInTheDocument();

        view.rerender(<Page open={false} section="kindle" />);

        await waitFor(() => expect(screen.getByRole('button', { name: 'opener' })).toHaveFocus());
    });
});
