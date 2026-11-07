import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import ThemeChoice from '@/shared/layout/ThemeChoice';

const translation = { t: (key: string) => key };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const theme = { mode: 'light', setThemeMode: vi.fn() };
vi.mock('@/context/ThemeContext', () => ({ useTheme: () => theme }));

describe('ThemeChoice', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        theme.mode = 'dark';
    });

    it('marks the theme in force', () => {
        render(<ThemeChoice />);

        expect(screen.getByRole('button', { name: 'themeDark' })).toHaveAttribute(
            'aria-pressed',
            'true',
        );
        expect(screen.getByRole('button', { name: 'themeLight' })).toHaveAttribute(
            'aria-pressed',
            'false',
        );
    });

    it('sets the one asked for, not the other one', async () => {
        render(<ThemeChoice />);

        await userEvent.click(screen.getByRole('button', { name: 'themeLight' }));

        expect(theme.setThemeMode).toHaveBeenCalledWith('light');
    });
});
