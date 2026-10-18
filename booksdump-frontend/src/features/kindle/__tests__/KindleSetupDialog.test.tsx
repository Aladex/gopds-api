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
    it('stays a dialog', () => {
        renderSetup();

        expect(document.querySelector('[data-slot="dialog-content"]')).toBeInTheDocument();
        expect(document.querySelector('[data-slot="drawer-content"]')).not.toBeInTheDocument();
        expect(document.querySelector('video')).toHaveAttribute('playsinline');
    });
});
