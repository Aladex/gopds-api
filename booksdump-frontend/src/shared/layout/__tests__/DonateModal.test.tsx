import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import DonateModal from '@/shared/layout/DonateModal';
import * as systemApi from '@/api/system';

// The modal's whole point is that it knows nothing about any particular way of
// giving: what it shows, and how many tabs it has, comes from the server. These
// tests therefore hand it lists it has never seen rather than the operator's
// real one.

vi.mock('@/api/system', () => ({ getDonateMethods: vi.fn() }));

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

const getDonateMethods = vi.mocked(systemApi.getDonateMethods);

const bitcoin = { id: 'bitcoin', label: 'Bitcoin', kind: 'address', value: 'bc1qtest', qr: true };
const card = { id: 'card', label: 'Card', kind: 'card', value: '5536913994186852', qr: false };
const boosty = { id: 'boosty', label: 'Boosty', kind: 'link', value: 'https://example.test/b', qr: false };

const openWith = async (methods: unknown[]) => {
    getDonateMethods.mockResolvedValue(methods as never);
    render(<DonateModal open onClose={() => {}} />);
    if (methods.length > 0) {
        await screen.findByText((methods[0] as { label: string }).label);
    }
};

beforeEach(() => {
    vi.clearAllMocks();
    matches.current = false;
});

test('gives every configured method a tab, in the order configured', async () => {
    await openWith([bitcoin, card, boosty]);

    const tabs = await screen.findAllByRole('tab');
    expect(tabs.map((tab) => tab.textContent)).toEqual(['Bitcoin', 'Card', 'Boosty']);
});

test('opens on the first method and shows only that one', async () => {
    await openWith([bitcoin, card, boosty]);

    expect(await screen.findByRole('tab', { name: 'Bitcoin' })).toHaveAttribute(
        'aria-selected',
        'true',
    );
    expect(screen.getByText('bc1qtest')).toBeInTheDocument();
    // The other methods' values are not merely hidden — they are not mounted,
    // which is what keeps their QR codes from being generated.
    expect(screen.queryByText(/5536/)).not.toBeInTheDocument();
});

test('shows the method whose tab was chosen', async () => {
    await openWith([bitcoin, card, boosty]);

    await userEvent.click(await screen.findByRole('tab', { name: 'Card' }));

    // Shown grouped the way it is printed on the card.
    expect(await screen.findByText('5536 9139 9418 6852')).toBeInTheDocument();
    expect(screen.queryByText('bc1qtest')).not.toBeInTheDocument();
});

test('offers no choice when only one method is configured', async () => {
    await openWith([bitcoin]);

    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
    expect(screen.getByText('bc1qtest')).toBeInTheDocument();
});

test('shows nothing to give to when nothing is configured', async () => {
    await openWith([]);

    await waitFor(() => expect(getDonateMethods).toHaveBeenCalled());
    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /copy/i })).not.toBeInTheDocument();
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
