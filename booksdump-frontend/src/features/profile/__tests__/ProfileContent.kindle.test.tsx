import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

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
vi.mock('@/features/opds/OpdsInfo', () => ({ OpdsConnection: () => <div>opds-connection</div> }));
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

// On the connections tab the footer is not stuck over the content: it covered
// the Kindle switches, where the setup dialog scrolls the reader to.
it('keeps the footer off the Kindle settings, and stuck where there is a Save', async () => {
    render(<ProfileContent open section="kindle" onClose={vi.fn()} />);
    expect(screen.getByTestId('profile-footer').className).not.toMatch(/sticky/);

    await userEvent.click(screen.getByRole('tab', { name: 'profileTab.account' }));
    expect(screen.getByTestId('profile-footer').className).toMatch(/sticky/);
});

// The connections tab is three tabs of its own: Kindle, Telegram, OPDS. All
// three stay mounted, as the main tabs' panels do — fading in one grid cell,
// so the sheet does not change height — and only the chosen one is shown and
// reachable: the others are inactive and inert.
const subTab = (name: string) => screen.getByRole('tab', { name });
const panelOf = (text: string) =>
    screen.getByText(text).closest('[data-slot="tabs-content"]') as HTMLElement;
const onlyShows = (shown: 'kindle' | 'telegram' | 'opds') => {
    const groups = {
        kindle: 'kindle-settings',
        telegram: 'profileSection.telegramBot',
        opds: 'opds-connection',
    };
    for (const [name, text] of Object.entries(groups)) {
        const panel = panelOf(text);
        if (name === shown) {
            expect(panel).toHaveAttribute('data-state', 'active');
            expect(panel).not.toHaveAttribute('inert');
        } else {
            expect(panel).toHaveAttribute('data-state', 'inactive');
            expect(panel).toHaveAttribute('inert');
        }
    }
};

describe('the connections tab', () => {
    it('opens on Kindle when the reader goes there by hand', async () => {
        render(<ProfileContent open onClose={vi.fn()} />);

        await userEvent.click(screen.getByRole('tab', { name: 'profileTab.connections' }));

        expect(subTab('profileConnectionTab.kindle')).toHaveAttribute('aria-selected', 'true');
        onlyShows('kindle');
    });

    it('lands on Kindle when asked for the Kindle settings', () => {
        render(<ProfileContent open section="kindle" onClose={vi.fn()} />);

        expect(screen.getByRole('tab', { name: 'profileTab.connections' })).toHaveAttribute(
            'aria-selected',
            'true',
        );
        expect(subTab('profileConnectionTab.kindle')).toHaveAttribute('aria-selected', 'true');
        onlyShows('kindle');
    });

    it('shows each connection only in its own tab', async () => {
        render(<ProfileContent open section="kindle" onClose={vi.fn()} />);

        await userEvent.click(subTab('profileConnectionTab.telegram'));
        expect(subTab('profileConnectionTab.telegram')).toHaveAttribute('aria-selected', 'true');
        onlyShows('telegram');

        await userEvent.click(subTab('profileConnectionTab.opds'));
        onlyShows('opds');

        await userEvent.click(subTab('profileConnectionTab.kindle'));
        onlyShows('kindle');
    });

    it('remembers the connection last chosen when the profile opens again', async () => {
        const first = render(<ProfileContent open onClose={vi.fn()} />);
        await userEvent.click(screen.getByRole('tab', { name: 'profileTab.connections' }));
        await userEvent.click(subTab('profileConnectionTab.opds'));
        first.unmount();

        const second = render(<ProfileContent open onClose={vi.fn()} />);
        await userEvent.click(screen.getByRole('tab', { name: 'profileTab.connections' }));
        expect(subTab('profileConnectionTab.opds')).toHaveAttribute('aria-selected', 'true');
        onlyShows('opds');
        second.unmount();

        // Asked for Kindle, it still opens on Kindle.
        render(<ProfileContent open section="kindle" onClose={vi.fn()} />);
        onlyShows('kindle');
        // Back to the default for whatever runs next in this file.
        await userEvent.click(subTab('profileConnectionTab.kindle'));
    });

    it('keeps nothing in storage', async () => {
        const setItem = vi.spyOn(Storage.prototype, 'setItem');
        render(<ProfileContent open onClose={vi.fn()} />);
        await userEvent.click(screen.getByRole('tab', { name: 'profileTab.connections' }));
        await userEvent.click(subTab('profileConnectionTab.telegram'));
        await userEvent.click(subTab('profileConnectionTab.kindle'));

        expect(setItem).not.toHaveBeenCalled();
        setItem.mockRestore();
    });
});

describe('the main tabs', () => {
    it('stack icon over label in the phone’s sheet', () => {
        render(<ProfileContent open inSheet onClose={vi.fn()} />);
        const [list] = screen.getAllByRole('tablist');

        expect(list).toHaveAttribute('data-stacked', 'true');
        for (const name of [
            'profileTab.account',
            'profileTab.security',
            'profileTab.connections',
        ]) {
            const tab = screen.getByRole('tab', { name });
            expect(tab.querySelector('svg')).not.toBeNull();
            expect(tab.textContent).toBe(name);
            expect(tab.querySelector('.truncate')).toBeNull();
        }
    });

    it('put the icon beside the label in the desktop dialog', () => {
        render(<ProfileContent open onClose={vi.fn()} />);
        const [list] = screen.getAllByRole('tablist');

        expect(list).not.toHaveAttribute('data-stacked');
        expect(
            screen.getByRole('tab', { name: 'profileTab.account' }).querySelector('svg'),
        ).not.toBeNull();
    });

    it('show the connections as the shared underline tabs', async () => {
        render(<ProfileContent open section="kindle" onClose={vi.fn()} />);
        const lists = screen.getAllByRole('tablist');

        expect(lists[1]).toHaveAttribute('data-variant', 'underline');
        expect(lists[1]).toHaveAccessibleName('profileTab.connections');
        // No look of its own: only the shared component's classes.
        expect(
            screen.getByRole('tab', { name: 'profileConnectionTab.kindle' }).className,
        ).not.toMatch(/h-8|px-0\.5/);
    });
});
