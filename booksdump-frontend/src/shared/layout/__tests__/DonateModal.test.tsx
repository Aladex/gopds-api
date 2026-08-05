import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import DonateModal from '@/shared/layout/DonateModal';

// The modal's whole point is that it knows nothing about any particular way of
// giving: what it shows, and how many tabs it has, comes from the server by way
// of the header. These tests therefore hand it lists it has never seen rather
// than the operator's real one.

// A stable t: useTranslation must not hand back a fresh function each render,
// or effects keyed on it loop forever.
const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'en' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/context/ThemeContext', () => ({ useTheme: () => ({ mode: 'light' }) }));

// jsdom has no viewport, so useMediaQuery reports the wide layout and the
// mobile branch would never run. narrow() switches it for a test.
const matches = { current: false };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => matches.current,
    default: () => matches.current,
}));
const narrow = () => {
    matches.current = true;
};

// The code itself is drawn on a canvas jsdom does not implement; that it was
// asked for at all is what these tests care about.
vi.mock('qrcode', () => ({
    default: { toDataURL: vi.fn().mockResolvedValue('data:image/png;base64,qr') },
}));

const bitcoin = { id: 'bitcoin', label: 'Bitcoin', kind: 'address', value: 'bc1qtest', qr: true };
const card = { id: 'card', label: 'Card', kind: 'card', value: '5536913994186852', qr: false };
const boosty = {
    id: 'boosty',
    label: 'Boosty',
    kind: 'link',
    value: 'https://example.test/b',
    qr: false,
};

const openWith = async (methods: unknown[]) =>
    render(<DonateModal open methods={methods as never} onClose={() => {}} />);

beforeEach(() => {
    vi.clearAllMocks();
    matches.current = false;
});

test('gives every configured method a tab, in the order configured', async () => {
    await openWith([bitcoin, card, boosty]);

    const tabs = await screen.findAllByRole('tab');
    expect(tabs.map((tab) => tab.textContent)).toEqual(['Bitcoin', 'Card', 'Boosty']);
});

/** The tab panel holding a given value, whether or not it is the one on show. */
const panelFor = (value: string | RegExp) => screen.getByText(value).closest('[role="tabpanel"]');

test('opens on the first method and shows only that one', async () => {
    await openWith([bitcoin, card, boosty]);

    expect(await screen.findByRole('tab', { name: 'Bitcoin' })).toHaveAttribute(
        'aria-selected',
        'true',
    );
    expect(screen.getByText('bc1qtest')).toBeInTheDocument();
    /*
     * Every method is mounted now, so that switching between them can hand over
     * rather than substitute — there is nothing to fade out otherwise. The cost
     * is that each one's QR code is generated when the dialog opens rather than
     * when its tab is first opened.
     *
     * So the others are present, and what keeps them out of the way is asserted
     * rather than their absence. Which panel is *drawn* cannot be checked here:
     * no stylesheet is loaded under test, so the class that hides one has no
     * effect. That was measured in a browser; `inert` is the half jsdom can see,
     * and it is the half that matters for anyone tabbing through.
     */
    expect(panelFor(/5536/)).toHaveAttribute('inert');
    expect(panelFor('bc1qtest')).not.toHaveAttribute('inert');
});

test('shows the method whose tab was chosen', async () => {
    await openWith([bitcoin, card, boosty]);

    await userEvent.click(await screen.findByRole('tab', { name: 'Card' }));

    // Shown grouped the way it is printed on the card.
    expect(await screen.findByText('5536 9139 9418 6852')).toBeInTheDocument();
    expect(panelFor(/5536/)).not.toHaveAttribute('inert');
    expect(panelFor('bc1qtest')).toHaveAttribute('inert');
});

test('offers no choice when only one method is configured', async () => {
    await openWith([bitcoin]);

    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
    expect(screen.getByText('bc1qtest')).toBeInTheDocument();
});

test('shows nothing to give to when nothing is configured', async () => {
    await openWith([]);

    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /copy/i })).not.toBeInTheDocument();
});

/*
 * The dialog used to fetch the methods for itself when opened, which the header
 * had already done in order to decide whether to offer the button at all. So it
 * appeared as a title over an empty space and filled in a moment later, the tab
 * strip and the panel arriving together.
 *
 * Handed the list, it is complete in the frame it first renders — which is what
 * this asserts by looking before yielding to anything.
 */
test('is whole in its first frame, with nothing left to arrive', () => {
    render(<DonateModal open methods={[bitcoin, card, boosty] as never} onClose={() => {}} />);

    expect(screen.getAllByRole('tab')).toHaveLength(3);
    expect(screen.getByText('bc1qtest')).toBeInTheDocument();
});

test('on a phone the methods arrive in a sheet, tabs and all', async () => {
    narrow();
    await openWith([bitcoin, card, boosty]);

    // A dialog is inset from both edges; the sheet is not, which is the whole
    // reason for the branch.
    expect(document.querySelector('[data-slot="drawer-content"]')).toBeInTheDocument();
    expect(document.querySelector('[data-slot="dialog-content"]')).not.toBeInTheDocument();

    const tabs = await screen.findAllByRole('tab');
    expect(tabs.map((tab) => tab.textContent)).toEqual(['Bitcoin', 'Card', 'Boosty']);
    expect(screen.getByText('bc1qtest')).toBeInTheDocument();
});

test('a link method is followed rather than copied', async () => {
    await openWith([boosty]);

    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('href', 'https://example.test/b');
    expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'));
    expect(screen.queryByRole('button', { name: /copy/i })).not.toBeInTheDocument();
});
