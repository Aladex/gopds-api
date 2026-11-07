import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import React from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import i18n from '@/app/i18n';
import PrivateRoute from '@/app/PrivateRoute';
import * as systemApi from '@/api/system';

// One foot at every width, and one donate modal for the pill and the sentence.

vi.mock('@/context/AuthContext', () => ({
    useAuth: () => ({
        isAuthenticated: true,
        isLoaded: true,
        isLoading: false,
        user: { username: 'reader', is_superuser: false },
    }),
}));
vi.mock('@/api/system', () => ({ getDonateMethods: vi.fn() }));
vi.mock('@/shared/layout/Header', () => ({
    default: ({
        donateMethods,
        onOpenDonate,
    }: {
        donateMethods: unknown[];
        onOpenDonate: () => void;
    }) => (
        <div data-testid="header">
            {donateMethods.length}
            <button onClick={onOpenDonate}>pill</button>
        </div>
    ),
}));
vi.mock('@/shared/layout/DonateModal', () => ({
    default: ({ methods, onClose }: { methods: unknown[]; onClose: () => void }) => (
        <button onClick={onClose}>donate-modal:{methods.length}</button>
    ),
}));
vi.mock('@/shared/layout/SectionRow', () => ({ default: () => <nav>sections</nav> }));
vi.mock('@/features/catalogue/LanguagePrompt', () => ({ default: () => null }));
vi.mock('@/features/catalogue/CatalogueSearchContext', () => ({
    CatalogueSearchProvider: ({ children }: { children: React.ReactNode }) => children,
}));
type Shell = { open: boolean; section?: string };
vi.mock('@/features/profile/ProfileDrawer', () => ({
    default: ({ open, section }: Shell) => (open ? <div>drawer:{section}</div> : null),
}));
vi.mock('@/features/profile/ProfileDialog', () => ({
    default: ({ open, section }: Shell) => (open ? <div>dialog:{section}</div> : null),
}));

const phone = { current: true };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => phone.current,
    default: () => phone.current,
}));

const getDonateMethods = vi.mocked(systemApi.getDonateMethods);
const method = { id: 'a', label: 'A', kind: 'link', value: 'x', qr: false } as const;

const renderRoute = () =>
    render(
        <MemoryRouter>
            <PrivateRoute>
                <div>page</div>
            </PrivateRoute>
        </MemoryRouter>,
    );

describe('PrivateRoute', () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        phone.current = true;
        await act(() => i18n.changeLanguage('ru'));
        getDonateMethods.mockResolvedValue([method]);
    });

    it.each([
        ['a phone', true],
        ['a desktop', false],
    ])('ends the page in the one foot on %s', async (_, isPhone) => {
        phone.current = isPhone;
        renderRoute();

        expect(await screen.findByText(/Библиотека бесплатна/)).toBeInTheDocument();
        expect(screen.getAllByRole('contentinfo')).toHaveLength(1);
        expect(screen.getByRole('main')).toContainElement(screen.getByRole('contentinfo'));
    });

    it('asks for the donate methods once for the header and the foot', async () => {
        renderRoute();

        await screen.findByText(/Библиотека бесплатна/);
        expect(screen.getByTestId('header')).toHaveTextContent('1');
        expect(getDonateMethods).toHaveBeenCalledTimes(1);
    });

    it('tells the foot when there is nothing to offer', async () => {
        getDonateMethods.mockResolvedValue([]);
        renderRoute();

        await waitFor(() => expect(getDonateMethods).toHaveBeenCalled());
        expect(screen.getByRole('contentinfo')).toBeInTheDocument();
        expect(screen.queryByText(/Библиотека бесплатна/)).toBeNull();
    });

    it.each([
        ['the pill', 'pill'],
        ['the sentence', 'донаты'],
    ])('opens the one donate modal from %s, with the methods, and closes it', async (_, name) => {
        renderRoute();
        await screen.findByRole('button', { name: 'донаты' });
        expect(screen.queryByRole('button', { name: /^donate-modal/ })).toBeNull();

        await userEvent.click(screen.getByRole('button', { name }));
        await userEvent.click(await screen.findByRole('button', { name: 'donate-modal:1' }));

        expect(screen.queryByRole('button', { name: /^donate-modal/ })).toBeNull();
    });

    it.each([
        ['the sheet on a phone', true, 'Kindle', 'drawer:kindle'],
        ['the sheet on a phone', true, 'Читалки (OPDS)', 'drawer:opds'],
        ['the dialog on a desktop', false, 'Telegram-бот', 'dialog:telegram'],
        ['the dialog on a desktop', false, 'Читалки (OPDS)', 'dialog:opds'],
    ])('opens %s from the foot', async (_, isPhone, name, shown) => {
        phone.current = isPhone;
        renderRoute();

        await userEvent.click(screen.getByRole('button', { name }));

        expect(screen.getByText(shown)).toBeInTheDocument();
    });

    it('holds the sections on every private page, in the page', () => {
        renderRoute();

        expect(screen.getByRole('main')).toContainElement(screen.getByText('sections'));
    });

    it('keeps no room at the foot for a bar that has gone', () => {
        renderRoute();

        expect(screen.getByRole('main').className).not.toContain('pb-20');
    });

    it('lets a short page end at the end of the screen', () => {
        // #root is already a screen tall. A second screen-tall box under the
        // header put a header's height of scroll on every short list.
        renderRoute();

        expect(screen.getByRole('main').className).not.toContain('min-h-screen');
        // And the column it relies on: jsdom lays nothing out, so the rule that
        // fills the screen is read from the stylesheet. The browser check is
        // what proves the foot lands on the screen's end.
        const css = readFileSync(join(import.meta.dirname, '../../index.css'), 'utf8');
        expect(css).toMatch(/#root\s*\{[^}]*flex-direction:\s*column[^}]*min-height:\s*100vh/);
        expect(css).toMatch(/\bmain\s*\{\s*flex:\s*1;?\s*\}/);
    });
});
