import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import PhoneFoot from '@/shared/layout/PhoneFoot';

// The foot of a phone's page: the one sentence about money, and the theme. It
// asks for nothing, so it holds no button that pays and no version.

const translation = { t: (key: string) => key };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const theme = { mode: 'light', toggleTheme: vi.fn() };
vi.mock('@/context/ThemeContext', () => ({ useTheme: () => theme }));

beforeEach(() => {
    vi.clearAllMocks();
    theme.mode = 'light';
});

describe('PhoneFoot', () => {
    it('says what keeps the library going when there is a way to help', () => {
        render(<PhoneFoot hasDonate />);

        expect(screen.getByText('foot.sentence')).toBeInTheDocument();
    });

    it('says nothing about donations when there is no way to make one', () => {
        render(<PhoneFoot hasDonate={false} />);

        expect(screen.queryByText('foot.sentence')).toBeNull();
        expect(screen.getByRole('button')).toBeInTheDocument();
    });

    it('offers nothing to pay with', () => {
        render(<PhoneFoot hasDonate />);

        expect(screen.getAllByRole('button')).toHaveLength(1);
        expect(screen.queryByRole('link')).toBeNull();
    });

    it('names the theme in force and switches it', async () => {
        const { rerender } = render(<PhoneFoot hasDonate />);
        expect(screen.getByRole('button', { name: 'foot.themeLight' })).toBeInTheDocument();

        await userEvent.click(screen.getByRole('button', { name: 'foot.themeLight' }));
        expect(theme.toggleTheme).toHaveBeenCalledTimes(1);

        theme.mode = 'dark';
        rerender(<PhoneFoot hasDonate />);
        expect(screen.getByRole('button', { name: 'foot.themeDark' })).toBeInTheDocument();
    });

    it('stays in the page, never fixed to the screen', () => {
        render(<PhoneFoot hasDonate />);

        expect(screen.getByRole('contentinfo').className).not.toMatch(/fixed|sticky/);
    });
});
