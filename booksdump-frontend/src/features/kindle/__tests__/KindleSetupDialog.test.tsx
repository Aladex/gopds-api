import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import KindleSetupDialog from '@/features/kindle/KindleSetupDialog';

// On a phone the setup is a sheet from the bottom edge, as the profile and the
// donate dialog are there, not a centred modal: the video gets the whole
// width, and the sheet scrolls when the steps run past the screen.

const translation = { t: (key: string) => key, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({
    useTranslation: () => translation,
    Trans: ({ i18nKey }: { i18nKey: string }) => i18nKey,
}));

// jsdom has no viewport, so useMediaQuery reports the wide layout unless a
// test narrows it.
const matches = { current: false };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => matches.current,
}));

// The sheet follows the pointer with pointer capture, which jsdom lacks.
beforeAll(() => {
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
    Element.prototype.hasPointerCapture ??= () => false;
});

const onClose = vi.fn();
const onOpenSettings = vi.fn();
const renderSetup = () =>
    render(<KindleSetupDialog open onClose={onClose} onOpenSettings={onOpenSettings} />);

beforeEach(() => {
    vi.clearAllMocks();
    matches.current = false;
});

describe('on a phone', () => {
    beforeEach(() => {
        matches.current = true;
    });

    it('opens as a bottom sheet, not a centred dialog', () => {
        renderSetup();

        const sheet = document.querySelector('[data-slot="drawer-content"]');
        expect(sheet).toBeInTheDocument();
        expect(document.querySelector('[data-slot="dialog-content"]')).not.toBeInTheDocument();
        expect(screen.getByRole('dialog')).toHaveAccessibleName('kindleSetup.title');
        // Taller than the bottom sheet's default cap allows, and scrolling
        // inside it when that is still not enough.
        expect(sheet?.className).toMatch(/max-h-\[92vh\]/);
        expect(screen.getByTestId('kindle-setup-scroll').className).toMatch(/overflow-y-auto/);
    });

    it('keeps the title above the scrolling body, as the donate sheet does', () => {
        renderSetup();
        const scroller = screen.getByTestId('kindle-setup-scroll');
        const title = screen.getByText('kindleSetup.title');

        expect(scroller).not.toContainElement(title);
        expect(scroller.className).toMatch(/overflow-y-auto/);
        expect(scroller).toContainElement(screen.getByText('kindleSetup.intro'));
    });

    // Side by side the two buttons needed 312px of the 288 a 320px phone has
    // and pushed Close off the left edge; the shared sheet footer stacks them.
    it('stacks the two buttons in the shared sheet footer, below the scroller', () => {
        renderSetup();
        const footer = screen.getByTestId('kindle-setup-footer');

        expect(footer).toHaveAttribute('data-slot', 'drawer-footer');
        expect(footer.className).toMatch(/flex-col/);
        expect(screen.getByTestId('kindle-setup-scroll')).not.toContainElement(footer);
        const names = [...footer.querySelectorAll('button')].map((b) => b.textContent);
        expect(names).toEqual(['kindleSetup.openSettings', 'close']);
    });

    it('plays the video inline at the sheet’s width, without the sheet dragging off', () => {
        renderSetup();
        const video = document.querySelector('video')!;

        expect(video).toHaveAttribute('playsinline');
        expect(video.className).toMatch(/\bw-full\b/);
        expect(video.closest('[data-vaul-no-drag]')).not.toBeNull();
        expect(screen.getByTestId('kindle-setup-scroll')).toContainElement(video);
    });

    it('still leads to the settings', async () => {
        renderSetup();

        await userEvent.click(screen.getByRole('button', { name: 'kindleSetup.openSettings' }));

        expect(onClose).toHaveBeenCalled();
        expect(onOpenSettings).toHaveBeenCalled();
    });
});

describe('on a desktop', () => {
    it('stays a dialog, under the same 85vh cap as the profile and the donate dialog', () => {
        renderSetup();

        const content = document.querySelector('[data-slot="dialog-content"]')!;
        expect(content.className).toMatch(/max-h-\[85vh\]/);
        const footer = document.querySelector('[data-slot="dialog-footer"]')!;
        expect([...footer.querySelectorAll('button')].map((b) => b.textContent)).toEqual([
            'close',
            'kindleSetup.openSettings',
        ]);

        expect(document.querySelector('[data-slot="dialog-content"]')).toBeInTheDocument();
        expect(document.querySelector('[data-slot="drawer-content"]')).not.toBeInTheDocument();
        expect(document.querySelector('video')).toHaveAttribute('playsinline');
    });
});
