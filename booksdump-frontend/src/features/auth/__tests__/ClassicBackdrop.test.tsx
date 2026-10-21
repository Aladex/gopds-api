import React from 'react';
import { render } from '@testing-library/react';

import ClassicBackdrop from '@/features/auth/ClassicBackdrop';
import { PASSAGES, passageFor } from '@/features/auth/passages';

// The verse is decoration, and decoration that reaches a screen reader or a
// pointer stops being decoration. jsdom lays nothing out, so how it looks was
// measured in a browser; what is held here is everything else.

const language = { current: 'ru' };
vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: (key: string) => key, i18n: { language: language.current } }),
}));

beforeEach(() => {
    language.current = 'ru';
});

describe('the verse behind the sign-in screens', () => {
    it('says nothing to a screen reader', () => {
        const { container } = render(<ClassicBackdrop moment="returning" />);

        // Sixty lines of Pushkin announced before the password field would be
        // a cruel joke, so every part of this is hidden from the tree.
        expect(container.querySelector('[aria-hidden="true"]')).toBeInTheDocument();
        expect(container.textContent).not.toBe('');
        expect(container.querySelectorAll(':not([aria-hidden]) > p')).toHaveLength(0);
    });

    it('cannot be clicked or selected', () => {
        const { container } = render(<ClassicBackdrop moment="returning" />);

        const layer = container.firstElementChild as HTMLElement;
        expect(layer.className).toMatch(/pointer-events-none/);
        expect(layer.className).toMatch(/select-none/);
    });

    // Two copies ride one after the other and the animation moves the pair by
    // exactly one copy. A single copy would leave a screen-high gap before the
    // poem came round again.
    it('carries the poem twice so the loop has no gap', () => {
        const { container } = render(<ClassicBackdrop moment="returning" />);

        const lines = PASSAGES.returning.ru.lines.length;
        expect(container.querySelectorAll('p')).toHaveLength(lines * 2);
    });

    it('moves by exactly one copy, at the same speed whatever the poem', () => {
        for (const moment of ['returning', 'beginning', 'lost'] as const) {
            const { container } = render(<ClassicBackdrop moment={moment} />);
            const drifting = container.querySelector('[style*="--verse-height"]') as HTMLElement;

            const lines = PASSAGES[moment].ru.lines.length;
            const height = Number(
                drifting.style.getPropertyValue('--verse-height').replace('px', ''),
            );
            const duration = Number(drifting.style.animationDuration.replace('s', ''));

            expect(height).toBe(lines * 44);
            // Speed is the constant, not the timing: a longer poem simply takes
            // longer to pass.
            expect(height / duration).toBeCloseTo(25, 5);
        }
    });

    it('stops moving for anyone who asked their system for less motion', () => {
        const { container } = render(<ClassicBackdrop moment="lost" />);

        const drifting = container.querySelector('[style*="--verse-height"]') as HTMLElement;
        expect(drifting.className).toMatch(/motion-reduce:animate-none/);
    });

    it('follows the interface language', () => {
        const { container: ru } = render(<ClassicBackdrop moment="returning" />);
        expect(ru.textContent).toContain('Я помню чудное мгновенье');

        language.current = 'en';
        const { container: en } = render(<ClassicBackdrop moment="returning" />);
        expect(en.textContent).toContain('When to the sessions of sweet silent thought');
        expect(en.textContent).not.toContain('Я помню');
    });
});

describe('choosing a passage', () => {
    it('gives Russian only to a Russian interface', () => {
        expect(passageFor('lost', 'ru')).toBe(PASSAGES.lost.ru);
        expect(passageFor('lost', 'ru-RU')).toBe(PASSAGES.lost.ru);
        expect(passageFor('lost', 'en')).toBe(PASSAGES.lost.en);
    });

    // Two languages are what the interface offers, and English is the safer of
    // the two to be wrong about — the same rule the emails follow.
    it('falls back to English for anything else', () => {
        for (const language of ['', 'fr', 'de-DE', 'klingon']) {
            expect(passageFor('beginning', language)).toBe(PASSAGES.beginning.en);
        }
    });

    // Not translations of each other: a Russian reader gets Pushkin and an
    // English one gets a poem of their own tradition about the same thing.
    it('pairs each moment with a poem in each language', () => {
        for (const moment of ['returning', 'beginning', 'lost'] as const) {
            const pair = PASSAGES[moment];
            expect(pair.ru.lines.length).toBeGreaterThan(10);
            expect(pair.en.lines.length).toBeGreaterThan(10);
            expect(pair.ru.source).not.toBe(pair.en.source);
            // Whoever wrote it is recorded even though it is never shown.
            expect(pair.ru.source).toMatch(/\d{4}/);
            expect(pair.en.source).toMatch(/\d{4}/);
        }
    });
});
