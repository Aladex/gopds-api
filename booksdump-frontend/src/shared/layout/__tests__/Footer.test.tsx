import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import React from 'react';
import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import i18n from '@/app/i18n';
import { ProfileOpenerContext } from '@/features/profile/ProfileOpenerContext';
import Footer from '@/shared/layout/Footer';
import { CONTENT_COLUMN } from '@/shared/layout/breakpoints';

// The real locales: the sentence's link lives inside the translated string, so
// a stubbed t() could not see it go missing.

const openProfile = vi.fn();
const onOpenDonate = vi.fn();

const renderFooter = (hasDonate = true) =>
    render(
        <ProfileOpenerContext.Provider value={{ openProfile }}>
            <Footer hasDonate={hasDonate} onOpenDonate={onOpenDonate} />
        </ProfileOpenerContext.Provider>,
    );

const sentence = () => screen.queryByRole('button', { name: /^(донаты|donations)$/ })?.closest('p');

beforeEach(async () => {
    vi.clearAllMocks();
    await act(() => i18n.changeLanguage('ru'));
});

describe('Footer', () => {
    it.each([
        ['ru', ['Читалки (OPDS)', 'Telegram-бот', 'Kindle']],
        ['en', ['Reading apps (OPDS)', 'Telegram bot', 'Kindle']],
    ])('names the three ways out of the site in %s', async (lang, names) => {
        await act(() => i18n.changeLanguage(lang));
        renderFooter();

        const links = within(screen.getByRole('navigation')).getAllByRole('button');
        expect(links.map((link) => link.textContent)).toEqual(names);
    });

    it.each([
        ['ru', 'Библиотека бесплатна и живёт на донаты', 'донаты'],
        ['en', 'The library is free and runs on donations', 'donations'],
    ])('carries the sentence in %s with its last word a link', async (lang, text, word) => {
        await act(() => i18n.changeLanguage(lang));
        renderFooter();

        expect(sentence()).toHaveTextContent(new RegExp(`^${text}$`));
        expect(screen.getByRole('button', { name: word })).toBeInTheDocument();
    });

    it('opens the existing donate modal from the word, and nothing else from it', async () => {
        renderFooter();

        await userEvent.click(screen.getByRole('button', { name: 'донаты' }));

        expect(onOpenDonate).toHaveBeenCalledTimes(1);
        expect(openProfile).not.toHaveBeenCalled();
    });

    it.each([
        ['Читалки (OPDS)', 'opds'],
        ['Telegram-бот', 'telegram'],
        ['Kindle', 'kindle'],
    ])('opens the profile on %s', async (name, section) => {
        renderFooter();

        await userEvent.click(screen.getByRole('button', { name }));

        expect(openProfile).toHaveBeenCalledTimes(1);
        expect(openProfile).toHaveBeenCalledWith(section);
        expect(onOpenDonate).not.toHaveBeenCalled();
    });

    it('drops the sentence, and only the sentence, with nothing to donate to', () => {
        renderFooter(false);

        expect(sentence()).toBeUndefined();
        expect(screen.queryByText(/Библиотека/)).toBeNull();
        expect(screen.getAllByRole('button')).toHaveLength(3);
    });

    it('offers no theme and no version', () => {
        renderFooter();

        expect(screen.getByRole('contentinfo').textContent).not.toMatch(/Тема|Theme|Version/);
        expect(screen.getAllByRole('button')).toHaveLength(4);
    });

    it('asks the server for nothing', () => {
        const source = readFileSync(join(import.meta.dirname, '../Footer.tsx'), 'utf8');

        expect(source).not.toMatch(/@\/api\/|useTheme/);
    });

    it('stays in the page, never fixed to the screen', () => {
        renderFooter();

        expect(screen.getByRole('contentinfo').className).not.toMatch(/fixed|sticky/);
    });

    it('holds its rows in the list column while the band runs the full width', () => {
        renderFooter();

        const band = screen.getByRole('contentinfo');
        const rows = screen.getByRole('navigation').parentElement!;
        expect(rows).toHaveClass(...CONTENT_COLUMN.split(' '));
        expect(band).toHaveClass('bg-chrome');
        expect(band.className).not.toContain('max-w');
    });

    it('gives a finger 40px and a pointer 32px, split where the phone layout splits', () => {
        renderFooter();

        for (const control of screen.getAllByRole('button')) {
            expect(control).toHaveClass('h-10', 'min-[601px]:h-8', 'whitespace-nowrap');
        }
    });

    it('scales below the widest row rather than letting a row break', () => {
        renderFooter();

        expect(screen.getByRole('contentinfo').className).toContain(
            'min(13px,calc((100vw_-_2rem)/22.6))',
        );
    });
});
