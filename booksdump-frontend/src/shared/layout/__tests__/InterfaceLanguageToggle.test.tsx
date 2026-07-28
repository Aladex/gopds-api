import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import InterfaceLanguageToggle from '@/shared/layout/InterfaceLanguageToggle';
import {
    InterfaceLanguageProvider,
    resetChosenThisSession,
} from '@/context/InterfaceLanguageContext';

const changeLanguage = vi.fn();
vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: (key: string) => key, i18n: { changeLanguage } }),
}));

vi.mock('@/context/AuthContext', () => ({
    useAuth: () => ({ user: null, updateUser: vi.fn() }),
}));

vi.mock('@/api/auth', () => ({ updateCurrentUser: vi.fn() }));

function renderToggle() {
    return render(
        <InterfaceLanguageProvider>
            <InterfaceLanguageToggle />
        </InterfaceLanguageProvider>,
    );
}

describe('InterfaceLanguageToggle', () => {
    beforeEach(() => {
        window.localStorage.clear();
        resetChosenThisSession();
        vi.clearAllMocks();
        vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-GB');
    });

    it('offers both locales and marks the one in force', () => {
        renderToggle();

        expect(screen.getByRole('button', { name: 'English' })).toHaveAttribute(
            'aria-pressed',
            'true',
        );
        expect(screen.getByRole('button', { name: 'Русский' })).toHaveAttribute(
            'aria-pressed',
            'false',
        );
    });

    // Two letters is all the corner of a card affords, so the name has to reach
    // a screen reader some other way.
    it('names each language for anyone who cannot see the code', () => {
        renderToggle();

        expect(screen.getByRole('button', { name: 'Русский' })).toHaveTextContent('ru');
    });

    it('switches on click and remembers the choice', async () => {
        renderToggle();

        await userEvent.click(screen.getByRole('button', { name: 'Русский' }));

        expect(screen.getByRole('button', { name: 'Русский' })).toHaveAttribute(
            'aria-pressed',
            'true',
        );
        expect(window.localStorage.getItem('interfaceLang')).toBe('ru');
    });
});
