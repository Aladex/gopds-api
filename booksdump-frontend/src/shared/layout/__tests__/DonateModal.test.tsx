import React from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import DonateModal from '@/shared/layout/DonateModal';
import en from '@/locales/en/translation.json';
import ru from '@/locales/ru/translation.json';

// The modal's whole point is that it knows nothing about any particular way of
// giving: what it shows comes from the server by way of the header. These tests
// therefore hand it lists it has never seen rather than the operator's real one.

// A stable t: useTranslation must not hand back a fresh function each render,
// or effects keyed on it loop forever. Interpolated values are kept in what it
// returns, so a name can be told from its neighbour's.
const asked = new Set<string>();
const translate = (key: string, options?: Record<string, unknown> | string) => {
    asked.add(key);
    if (typeof options === 'string') return options;
    return options ? `${key} ${Object.values(options).join(' ')}` : key;
};
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

// The code itself is drawn on a canvas jsdom does not implement. The stand-in
// echoes what it was asked to encode, so a code can be told from another's.
vi.mock('qrcode', () => ({
    default: {
        toDataURL: vi.fn((value: string) => Promise.resolve(`data:image/png;base64,${value}`)),
    },
}));

const bitcoin = { id: 'bitcoin', label: 'Bitcoin', kind: 'address', value: 'bc1qtest', qr: true };
const ether = { id: 'ether', label: 'Ether', kind: 'address', value: '0xether', qr: true };
const tron = { id: 'tron', label: 'Tron', kind: 'address', value: 'Ttron', qr: true };
const card = { id: 'card', label: 'Card', kind: 'card', value: '5536913994186852', qr: false };
const boosty = {
    id: 'boosty',
    label: 'Boosty',
    kind: 'link',
    value: 'https://www.example.test/b',
    qr: false,
};
const payable = (n: number, link?: string) => ({
    id: `card-${n}`,
    label: `Card ${n}`,
    kind: 'card',
    value: `${n}${n}${n}${n}000011112222`,
    qr: false,
    ...(link ? { link } : {}),
});

const openWith = (methods: unknown[]) =>
    render(<DonateModal open methods={methods as never} onClose={() => {}} />);

/** Sets jsdom's clipboard up after user-event has installed its own stand-in. */
const clipboard = () => {
    const user = userEvent.setup();
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
    return { user, writeText };
};

const trackEl = () => screen.getByRole('group', { name: 'donateCarouselHint' });
// By attribute: a hidden slide has no accessible name for a role query to match.
const slideEl = (index: number) =>
    trackEl().querySelector(`[data-slide="${index - 1}"]`) as HTMLElement;

/**
 * Gives the track a geometry, which jsdom has none of. Slides sit 100px apart
 * and move with scrollLeft, and scrollTo lands at once and reports it the way
 * a browser does. How that behaves with real snapping is measured in a browser;
 * this is here for what the component makes of the position it is told.
 */
const layOut = () => {
    const track = trackEl();
    const slides = Array.from(track.querySelectorAll<HTMLElement>('[data-slide]'));
    slides.forEach((slide, index) => {
        slide.getBoundingClientRect = () =>
            ({ left: index * 100 - track.scrollLeft, width: 100 }) as DOMRect;
        Object.defineProperty(slide, 'offsetLeft', { value: index * 100 });
        Object.defineProperty(slide, 'clientWidth', { value: 100 });
    });
    track.getBoundingClientRect = () => ({ left: 0, width: 100 }) as DOMRect;
    Object.defineProperty(track, 'clientWidth', { value: 100 });
    const scrollTo = vi.fn(({ left }: { left: number }) => {
        track.scrollLeft = left;
        fireEvent.scroll(track);
    });
    track.scrollTo = scrollTo as never;
    return { track, scrollTo };
};

/** How long a swipe's tail keeps a tap from counting, and a little more. */
const settle = () => act(() => new Promise((resolve) => setTimeout(resolve, 200)));

beforeEach(() => {
    vi.clearAllMocks();
    matches.current = false;
});

// Unmounting the last dialog leaves Radix's focus scope a zero-delay timer to
// fire. Under a loaded full run the file's environment could be torn down
// first, and the timer then dispatched into a window that no longer existed —
// an unhandled error after every test here had passed. One macrotask lets it
// run while the window is still there.
afterAll(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
});

describe('every configured method is on show', () => {
    const addresses = (n: number) =>
        Array.from({ length: n }, (_, i) => ({
            id: `a${i}`,
            label: `Address ${i}`,
            kind: 'address',
            value: `address-value-${i}`,
            qr: i % 2 === 0,
        }));

    test.each([0, 1, 2, 3])('with %i cards', (count) => {
        const cards = Array.from({ length: count }, (_, i) => payable(i + 1));
        openWith([...cards, bitcoin, boosty]);

        for (const each of cards) {
            expect(
                screen.getByText(each.value.replace(/(.{4})/g, '$1 ').trim()),
            ).toBeInTheDocument();
        }
        expect(screen.getByText('bc1qtest')).toBeInTheDocument();
        expect(screen.getByRole('link', { name: /Boosty/ })).toBeInTheDocument();
        expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
        expect(screen.queryByRole('tab')).not.toBeInTheDocument();
    });

    test.each([1, 2, 6, 10])('with %i methods', (count) => {
        const methods = addresses(count);
        openWith(methods);

        for (const method of methods) {
            expect(screen.getByText(method.value)).toBeInTheDocument();
            expect(screen.getByText(method.label)).toBeInTheDocument();
        }
        expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    });

    test('in the order configured', () => {
        openWith([tron, boosty, bitcoin]);

        const dialog = screen.getByRole('dialog');
        const text = dialog.textContent ?? '';
        expect(text.indexOf('Tron')).toBeLessThan(text.indexOf('Boosty'));
        expect(text.indexOf('Boosty')).toBeLessThan(text.indexOf('Bitcoin'));
    });
});

test('shows nothing to give to when nothing is configured', () => {
    openWith([]);

    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^copy/ })).not.toBeInTheDocument();
});

/*
 * The dialog used to fetch the methods for itself when opened, which the header
 * had already done in order to decide whether to offer the button at all. So it
 * appeared as a title over an empty space and filled in a moment later.
 *
 * Handed the list, it is complete in the frame it first renders — which is what
 * this asserts by looking before yielding to anything.
 */
test('is whole in its first frame, with nothing left to arrive', () => {
    render(<DonateModal open methods={[bitcoin, card, boosty] as never} onClose={() => {}} />);

    expect(screen.getByText('bc1qtest')).toBeInTheDocument();
    expect(screen.getByText('5536 9139 9418 6852')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Boosty/ })).toBeInTheDocument();
});

test('on a phone the methods arrive in a sheet', () => {
    narrow();
    openWith([bitcoin, card, boosty]);

    // A dialog is inset from both edges; the sheet is not, which is the whole
    // reason for the branch.
    expect(document.querySelector('[data-slot="drawer-content"]')).toBeInTheDocument();
    expect(document.querySelector('[data-slot="dialog-content"]')).not.toBeInTheDocument();
    expect(screen.getByText('bc1qtest')).toBeInTheDocument();
});

test('nothing depends on what a method is called', () => {
    const named = [
        { ...card, label: 'Alpha' },
        { ...payable(2, 'https://bank.test/2'), label: 'Beta' },
        { ...bitcoin, label: 'Gamma' },
        { ...ether, label: 'Delta' },
        { ...boosty, label: 'Omega' },
    ];
    const structure = () =>
        (document.querySelector('[role="dialog"]')?.outerHTML ?? '')
            // Radix's generated ids, which differ between any two renders.
            .replace(/radix-[^"\s]+/g, 'id');

    const first = openWith(named);
    const shown = named.reduce(
        (html, method, i) => html.replaceAll(method.label, `Label ${i}`),
        structure(),
    );
    first.unmount();

    // The same methods under names no one could have written a branch for.
    openWith(named.map((method, i) => ({ ...method, id: `x${i}`, label: `Label ${i}` })));
    expect(structure()).toBe(shown);
});

describe('copying', () => {
    test('a card copies its digits, ungrouped as they are configured', async () => {
        const { user, writeText } = clipboard();
        openWith([card]);

        await user.click(screen.getByRole('button', { name: 'copy: Card' }));

        expect(writeText).toHaveBeenCalledTimes(1);
        expect(writeText).toHaveBeenCalledWith('5536913994186852');
        expect(await screen.findByRole('button', { name: 'copied: Card' })).toBeInTheDocument();
    });

    test('an address copies itself, from anywhere on its row', async () => {
        const { user, writeText } = clipboard();
        openWith([bitcoin, ether]);

        await user.click(screen.getByText('0xether'));

        expect(writeText).toHaveBeenCalledTimes(1);
        expect(writeText).toHaveBeenCalledWith('0xether');
        expect(await screen.findByRole('button', { name: 'copied: Ether' })).toBeInTheDocument();
        // Only the row that was used says so.
        expect(screen.getByRole('button', { name: 'copy: Bitcoin' })).toBeInTheDocument();
    });

    test('claims nothing when the clipboard refused', async () => {
        const { user, writeText } = clipboard();
        writeText.mockRejectedValue(new Error('denied'));
        document.execCommand = vi.fn(() => false);
        openWith([bitcoin]);

        await user.click(screen.getByRole('button', { name: 'copy: Bitcoin' }));
        await settle();

        expect(writeText).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole('button', { name: 'copied: Bitcoin' })).not.toBeInTheDocument();
    });
});

describe('a long address', () => {
    const value = 'x'.repeat(120);

    test('is shown whole and allowed to wrap', () => {
        openWith([{ ...bitcoin, value }, ether, tron]);

        const shown = screen.getByText(value);
        expect(shown.textContent).toBe(value);
        expect(shown.className).toContain('[overflow-wrap:anywhere]');
        // Up to the row, nothing may cut it. Whether it fits is measured in a
        // browser: no stylesheet is loaded here.
        for (
            let node: HTMLElement | null = shown;
            node && node.tagName !== 'BUTTON';
            node = node.parentElement
        ) {
            expect(node.className).not.toMatch(
                /truncate|ellipsis|line-clamp|whitespace-nowrap|text-nowrap/,
            );
        }
    });
});

describe('a link', () => {
    test('is followed rather than copied, and says where it goes', () => {
        openWith([boosty]);

        const link = screen.getByRole('link');
        expect(link).toHaveAttribute('href', 'https://www.example.test/b');
        expect(link).toHaveAttribute('target', '_blank');
        expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'));
        expect(link).toHaveTextContent('example.test');
        expect(link).not.toHaveTextContent('www.');
        expect(screen.queryByRole('button', { name: /^copy/ })).not.toBeInTheDocument();
    });

    test('one the operator mistyped still renders', () => {
        openWith([{ ...boosty, value: 'not a url' }, payable(1, 'also not a url')]);

        expect(screen.getByRole('link', { name: /Boosty/ })).toBeInTheDocument();
        expect(screen.getByRole('link', { name: /donatePayOnline/ })).toHaveAttribute(
            'href',
            'also not a url',
        );
    });

    test('an address may carry one of its own beside its copy', () => {
        openWith([{ ...bitcoin, link: 'https://pay.test/btc' }, ether, tron]);

        expect(screen.getByRole('link', { name: /Bitcoin/ })).toHaveAttribute(
            'href',
            'https://pay.test/btc',
        );
        expect(screen.getByRole('button', { name: 'copy: Bitcoin' })).toBeInTheDocument();
    });
});

describe('one card', () => {
    test('has its action beside it, and nothing to page through', () => {
        openWith([payable(1, 'https://bank.test/1'), bitcoin]);

        const link = screen.getByRole('link', { name: /donatePayOnline/ });
        expect(link).toHaveAttribute('href', 'https://bank.test/1');
        expect(link).toHaveTextContent('bank.test');
        expect(screen.queryByRole('region')).not.toBeInTheDocument();
        expect(screen.queryByText('1 / 1')).not.toBeInTheDocument();
    });

    test('leaves the sheet draggable from the card', () => {
        openWith([payable(1, 'https://bank.test/1'), bitcoin]);

        expect(document.querySelector('[data-vaul-no-drag]')).toBeNull();
    });

    test('without a link has no action at all', () => {
        openWith([payable(1), bitcoin]);

        expect(screen.queryByRole('link')).not.toBeInTheDocument();
    });
});

describe('several cards', () => {
    const three = [
        payable(1, 'https://first.test/pay'),
        payable(2),
        payable(3, 'https://www.third.test/pay'),
        bitcoin,
    ];

    test('page through dots, one for each, and a count', () => {
        openWith(three);

        expect(screen.getByText('1 / 3')).toHaveAttribute('aria-live', 'polite');
        const dots = within(screen.getByRole('group', { name: 'donateCarouselPick' })).getAllByRole(
            'button',
        );
        expect(dots.map((dot) => dot.getAttribute('aria-label'))).toEqual([
            'donateCarouselCard 1 Card 1',
            'donateCarouselCard 2 Card 2',
            'donateCarouselCard 3 Card 3',
        ]);
        expect(dots.map((dot) => dot.getAttribute('aria-current'))).toEqual([
            'true',
            'false',
            'false',
        ]);
    });

    test('every slide says which of how many it is', () => {
        openWith(three);

        expect(slideEl(2)).toHaveAttribute('aria-label', 'donateCarouselSlide 2 3');
    });

    test('each sleeve holds its own link and no other', () => {
        openWith(three);

        const anchors = (slide: HTMLElement) =>
            Array.from(slide.querySelectorAll('a')).map((a) => a.getAttribute('href'));
        expect(anchors(slideEl(1))).toEqual(['https://first.test/pay']);
        expect(anchors(slideEl(2))).toEqual([]);
        expect(anchors(slideEl(3))).toEqual(['https://www.third.test/pay']);
        expect(slideEl(3)).toHaveTextContent('third.test');
        expect(slideEl(3)).toHaveTextContent('3333 0000 1111 2222');
    });

    test('a dot brings its card to the centre', async () => {
        const { user, writeText } = clipboard();
        openWith(three);
        const { scrollTo } = layOut();

        await user.click(screen.getByRole('button', { name: 'donateCarouselCard 3 Card 3' }));

        expect(scrollTo).toHaveBeenCalledWith(expect.objectContaining({ left: 200 }));
        expect(screen.getByText('3 / 3')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'donateCarouselCard 3 Card 3' })).toHaveAttribute(
            'aria-current',
            'true',
        );
        expect(writeText).not.toHaveBeenCalled();
    });

    test('tapping a peeking card centres it and does not copy it', async () => {
        const { user, writeText } = clipboard();
        openWith(three);
        const { scrollTo } = layOut();

        await user.click(within(slideEl(2)).getByText('2222 0000 1111 2222'));

        expect(writeText).not.toHaveBeenCalled();
        expect(scrollTo).toHaveBeenCalledWith(expect.objectContaining({ left: 100 }));
        expect(screen.getByText('2 / 3')).toBeInTheDocument();
    });

    test('tapping a peeking sleeve’s link centres it and does not follow it', () => {
        const { writeText } = clipboard();
        openWith(three);
        const { scrollTo } = layOut();

        const link = slideEl(3).querySelector('a') as HTMLAnchorElement;
        // fireEvent reports false when the default action was prevented.
        expect(fireEvent.click(link)).toBe(false);

        expect(writeText).not.toHaveBeenCalled();
        expect(scrollTo).toHaveBeenCalledWith(expect.objectContaining({ left: 200 }));
    });

    test('once centred, the same card copies and the same link is followed', async () => {
        const { user, writeText } = clipboard();
        openWith(three);
        layOut();

        await user.click(within(slideEl(3)).getByText('3333 0000 1111 2222'));
        await settle();

        const link = slideEl(3).querySelector('a') as HTMLAnchorElement;
        expect(fireEvent.click(link)).toBe(true);
        await user.click(within(slideEl(3)).getByText('3333 0000 1111 2222'));

        expect(writeText).toHaveBeenCalledTimes(1);
        expect(writeText).toHaveBeenCalledWith('3333000011112222');
    });

    test('the tail of a swipe neither copies nor follows', async () => {
        const { writeText } = clipboard();
        openWith(three);
        const { track } = layOut();

        track.scrollLeft = 8;
        fireEvent.scroll(track);

        const current = slideEl(1);
        fireEvent.click(within(current).getByText('1111 0000 1111 2222'));
        expect(fireEvent.click(current.querySelector('a') as HTMLAnchorElement)).toBe(false);
        expect(writeText).not.toHaveBeenCalled();

        // And it is only the tail: the same card copies once the track is still.
        await settle();
        fireEvent.click(within(current).getByText('1111 0000 1111 2222'));
        expect(writeText).toHaveBeenCalledWith('1111000011112222');
    });

    test('a mouse drag selects without copying, and lands on a slide', async () => {
        const { writeText } = clipboard();
        openWith(three);
        const { track } = layOut();
        track.setPointerCapture = vi.fn();
        // Nothing moves on release: the click has to be stopped by the release
        // itself, not by a scroll event that happens to arrive first.
        const landing = vi.fn();
        track.scrollTo = landing as never;

        const face = within(slideEl(1)).getByText('1111 0000 1111 2222');
        fireEvent.pointerDown(face, { pointerType: 'mouse', button: 0, clientX: 200, clientY: 10 });
        fireEvent.pointerMove(face, { pointerType: 'mouse', clientX: 70, clientY: 12 });
        fireEvent.pointerUp(face, { pointerType: 'mouse', clientX: 70, clientY: 12 });
        fireEvent.click(face);

        expect(track.setPointerCapture).toHaveBeenCalled();
        expect(landing).toHaveBeenCalledWith(expect.objectContaining({ left: 100 }));
        expect(writeText).not.toHaveBeenCalled();
        await settle();
    });

    test('a finger is left to the browser', () => {
        openWith(three);
        const { track } = layOut();
        track.setPointerCapture = vi.fn();

        const face = within(slideEl(1)).getByText('1111 0000 1111 2222');
        fireEvent.pointerDown(face, { pointerType: 'touch', button: 0, clientX: 200, clientY: 10 });
        fireEvent.pointerMove(face, { pointerType: 'touch', clientX: 100, clientY: 12 });

        expect(track.scrollLeft).toBe(0);
        expect(track.setPointerCapture).not.toHaveBeenCalled();
    });

    test('a mostly vertical mouse drag is left alone', () => {
        openWith(three);
        const { track } = layOut();
        track.setPointerCapture = vi.fn();

        const face = within(slideEl(1)).getByText('1111 0000 1111 2222');
        fireEvent.pointerDown(face, { pointerType: 'mouse', button: 0, clientX: 200, clientY: 10 });
        fireEvent.pointerMove(face, { pointerType: 'mouse', clientX: 196, clientY: 60 });
        // Decided: wandering sideways afterwards does not reopen the question.
        fireEvent.pointerMove(face, { pointerType: 'mouse', clientX: 100, clientY: 70 });

        expect(track.scrollLeft).toBe(0);
        expect(track.setPointerCapture).not.toHaveBeenCalled();
    });

    test('the keyboard moves through them, and off neither end', () => {
        openWith(three);
        const { track } = layOut();

        fireEvent.keyDown(track, { key: 'ArrowRight' });
        expect(screen.getByText('2 / 3')).toBeInTheDocument();

        fireEvent.keyDown(track, { key: 'End' });
        expect(screen.getByText('3 / 3')).toBeInTheDocument();

        fireEvent.keyDown(track, { key: 'ArrowRight' });
        expect(screen.getByText('3 / 3')).toBeInTheDocument();

        fireEvent.keyDown(track, { key: 'ArrowLeft' });
        expect(screen.getByText('2 / 3')).toBeInTheDocument();

        fireEvent.keyDown(track, { key: 'Home' });
        expect(screen.getByText('1 / 3')).toBeInTheDocument();

        fireEvent.keyDown(track, { key: 'ArrowLeft' });
        expect(screen.getByText('1 / 3')).toBeInTheDocument();
    });

    test('only the centred sleeve can be tabbed to or read', () => {
        openWith(three);
        const { track } = layOut();

        const tabbable = (slide: HTMLElement) =>
            Array.from(slide.querySelectorAll('button, a')).map((node) =>
                node.getAttribute('tabindex'),
            );
        expect(tabbable(slideEl(1))).toEqual(['0', '0']);
        expect(tabbable(slideEl(2))).toEqual(['-1']);
        expect(tabbable(slideEl(3))).toEqual(['-1', '-1']);
        expect(slideEl(2)).toHaveAttribute('aria-hidden', 'true');

        fireEvent.keyDown(track, { key: 'End' });

        expect(tabbable(slideEl(1))).toEqual(['-1', '-1']);
        expect(tabbable(slideEl(3))).toEqual(['0', '0']);
        expect(slideEl(3)).toHaveAttribute('aria-hidden', 'false');
    });

    test('a swipe takes focus along rather than leaving it on a hidden slide', () => {
        openWith(three);
        const { track } = layOut();

        within(slideEl(1)).getByRole('button').focus();
        track.scrollLeft = 100;
        fireEvent.scroll(track);

        expect(document.activeElement).toBe(within(slideEl(2)).getByRole('button'));
    });

    test('a swipe leaves focus alone if it was elsewhere', () => {
        openWith(three);
        const { track } = layOut();

        const dot = screen.getByRole('button', { name: 'donateCarouselCard 1 Card 1' });
        dot.focus();
        track.scrollLeft = 100;
        fireEvent.scroll(track);

        expect(document.activeElement).toBe(dot);
    });

    test('focus goes with the selection', () => {
        openWith(three);
        const { track } = layOut();

        const first = within(slideEl(1)).getByRole('button');
        first.focus();
        fireEvent.keyDown(first, { key: 'ArrowRight' });

        expect(document.activeElement).toBe(within(slideEl(2)).getByRole('button'));
        expect(track.contains(document.activeElement)).toBe(true);
    });
});

describe('the QR code', () => {
    const qrName = (label: string) => `donateQr ${label}`;

    test('replaces the list and comes back', async () => {
        const { user } = clipboard();
        openWith([card, bitcoin, ether, boosty]);

        const trigger = screen.getByRole('button', { name: qrName('Ether') });
        expect(trigger).toHaveAttribute('aria-expanded', 'false');
        const list = screen.getByTestId('donate-list');
        expect(list).not.toHaveAttribute('inert');

        await user.click(trigger);

        const back = await screen.findByRole('button', { name: 'donateBack: Ether' });
        expect(document.activeElement).toBe(back);
        expect(trigger).toHaveAttribute('aria-expanded', 'true');
        expect(list).toHaveAttribute('inert');
        const view = screen.getByTestId('donate-qr');
        expect(within(view).getByText('0xether')).toBeInTheDocument();
        await vi.waitFor(() => {
            expect(view.querySelector('img')).toHaveAttribute(
                'src',
                'data:image/png;base64,0xether',
            );
        });

        await user.click(back);

        expect(screen.queryByTestId('donate-qr')).not.toBeInTheDocument();
        expect(list).not.toHaveAttribute('inert');
        expect(document.activeElement).toBe(screen.getByRole('button', { name: qrName('Ether') }));
    });

    test('comes back to where the list was left', async () => {
        const { user } = clipboard();
        openWith([card, bitcoin, ether, boosty]);

        const body = document.querySelector('[data-donate-body]') as HTMLElement;
        Object.defineProperty(body, 'clientHeight', { value: 100 });
        const list = screen.getByTestId('donate-list').parentElement as HTMLElement;
        list.getBoundingClientRect = () => ({ top: 60 }) as DOMRect;
        body.scrollTop = 37;

        await user.click(screen.getByRole('button', { name: qrName('Ether') }));

        // Too near the end for the code to fit, so it is given room and brought up.
        expect(list.style.minHeight).toBe('100px');
        expect(body.scrollTop).toBe(97);

        await user.click(screen.getByRole('button', { name: 'donateBack: Ether' }));

        expect(list.style.minHeight).toBe('');
        expect(body.scrollTop).toBe(37);
    });

    test('is the code of the method it was opened from, and copies that one', async () => {
        const { user, writeText } = clipboard();
        openWith([bitcoin, ether, tron]);

        await user.click(screen.getByRole('button', { name: qrName('Tron') }));
        await user.click(
            within(screen.getByTestId('donate-qr')).getByRole('button', { name: 'copy: Tron' }),
        );

        expect(writeText).toHaveBeenCalledWith('Ttron');
    });

    test('draws no code until one is asked for', async () => {
        const { default: QRCode } = await import('qrcode');
        openWith([bitcoin, ether, tron]);

        expect(QRCode.toDataURL).not.toHaveBeenCalled();
    });

    test('is drawn in the list when it is the only one and the list is short', async () => {
        openWith([bitcoin, boosty]);

        expect(screen.queryByRole('button', { name: qrName('Bitcoin') })).not.toBeInTheDocument();
        await vi.waitFor(() => {
            expect(document.querySelector('[role="dialog"] img')).toHaveAttribute(
                'src',
                'data:image/png;base64,bc1qtest',
            );
        });
    });

    test('is offered only where it was configured', () => {
        openWith([bitcoin, { ...ether, qr: false }, tron]);

        expect(screen.getByRole('button', { name: qrName('Bitcoin') })).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: qrName('Ether') })).not.toBeInTheDocument();
    });
});

describe('a card with a code of its own', () => {
    const qrName = (label: string) => `donateQr ${label}`;
    const coded = (n: number, link?: string) => ({ ...payable(n, link), qr: true });
    const encoded = async (value: string) => {
        const view = screen.getByTestId('donate-qr');
        await vi.waitFor(() => {
            expect(view.querySelector('img')).toHaveAttribute(
                'src',
                `data:image/png;base64,${value}`,
            );
        });
    };
    const drawing = async () => {
        const { default: QRCode } = await import('qrcode');
        return vi.mocked(QRCode.toDataURL);
    };

    test('alone: asked for, drawn from its own value, and back', async () => {
        const { user } = clipboard();
        const toDataURL = await drawing();
        openWith([coded(1, 'https://bank.test/1')]);

        const trigger = screen.getByRole('button', { name: qrName('Card 1') });
        expect(trigger).toHaveAttribute('aria-expanded', 'false');
        expect(toDataURL).not.toHaveBeenCalled();
        // Beside the copy target, not inside it: a button cannot hold a button.
        const face = screen.getByRole('button', { name: 'copy: Card 1' });
        expect(face.contains(trigger)).toBe(false);

        await user.click(trigger);

        const back = await screen.findByRole('button', { name: 'donateBack: Card 1' });
        expect(document.activeElement).toBe(back);
        expect(trigger).toHaveAttribute('aria-expanded', 'true');
        await encoded('1111000011112222');

        await user.click(back);

        expect(screen.queryByTestId('donate-qr')).not.toBeInTheDocument();
        expect(document.activeElement).toBe(trigger);
    });

    test('without a page to link to it still gets the action', async () => {
        const { user } = clipboard();
        openWith([coded(1)]);

        await user.click(screen.getByRole('button', { name: qrName('Card 1') }));

        await encoded('1111000011112222');
        expect(screen.queryByRole('link')).not.toBeInTheDocument();
    });

    test('among rows: takes the list’s place, the same as an address does', async () => {
        const { user } = clipboard();
        const toDataURL = await drawing();
        openWith([coded(1, 'https://bank.test/1'), bitcoin, ether, boosty]);

        const list = screen.getByTestId('donate-list');
        await user.click(screen.getByRole('button', { name: qrName('Card 1') }));

        expect(list).toHaveAttribute('inert');
        await encoded('1111000011112222');
        expect(toDataURL.mock.calls.map(([value]) => value)).toEqual(['1111000011112222']);

        await user.click(screen.getByRole('button', { name: 'donateBack: Card 1' }));

        expect(list).not.toHaveAttribute('inert');
    });

    test('keeps the room for its code reserved before it is asked for', () => {
        openWith([coded(1), { ...boosty }]);

        const list = screen.getByTestId('donate-list').parentElement as HTMLElement;
        expect(list.style.gridTemplateRows).toBe('minmax(272px, auto)');
    });

    test('opened from an address’s code, goes back to its own trigger', async () => {
        const { user } = clipboard();
        openWith([coded(1), bitcoin, ether, tron]);

        const body = document.querySelector('[data-donate-body]') as HTMLElement;
        body.scrollTop = 37;
        await user.click(screen.getByRole('button', { name: qrName('Bitcoin') }));
        // The first code brought itself into view; the list was left at 37.
        body.scrollTop = 500;
        await user.click(screen.getByRole('button', { name: qrName('Card 1') }));

        await encoded('1111000011112222');
        await user.click(screen.getByRole('button', { name: 'donateBack: Card 1' }));

        expect(document.activeElement).toBe(screen.getByRole('button', { name: qrName('Card 1') }));
        expect(body.scrollTop).toBe(37);
    });

    describe('in a carousel', () => {
        const three = [
            coded(1, 'https://first.test/pay'),
            coded(2),
            payable(3, 'https://third.test/pay'),
            boosty,
        ];
        const slideQr = (index: number) =>
            within(slideEl(index)).getByRole('button', {
                name: qrName(`Card ${index}`),
                hidden: true,
            });

        test('is that card’s code and not a neighbour’s', async () => {
            const { user } = clipboard();
            const toDataURL = await drawing();
            openWith(three);
            layOut();

            await user.click(screen.getByRole('button', { name: 'donateCarouselCard 2 Card 2' }));
            await settle();
            await user.click(slideQr(2));

            await encoded('2222000011112222');
            expect(toDataURL.mock.calls.map(([value]) => value)).toEqual(['2222000011112222']);
        });

        test('a peeking card’s action selects it and opens nothing', async () => {
            const { user } = clipboard();
            const toDataURL = await drawing();
            openWith(three);
            const { scrollTo } = layOut();

            await user.click(slideQr(2));

            expect(screen.queryByTestId('donate-qr')).not.toBeInTheDocument();
            expect(toDataURL).not.toHaveBeenCalled();
            expect(scrollTo).toHaveBeenCalledWith(expect.objectContaining({ left: 100 }));
            expect(screen.getByText('2 / 3')).toBeInTheDocument();
        });

        test('the tail of a swipe opens nothing', async () => {
            clipboard();
            openWith(three);
            const { track } = layOut();

            track.scrollLeft = 8;
            fireEvent.scroll(track);
            fireEvent.click(slideQr(1));

            expect(screen.queryByTestId('donate-qr')).not.toBeInTheDocument();
            await settle();
        });

        test('only the centred sleeve’s action can be tabbed to', () => {
            openWith(three);
            layOut();

            expect(slideQr(1)).toHaveAttribute('tabindex', '0');
            expect(slideQr(2)).toHaveAttribute('tabindex', '-1');
        });

        test('holds still while its code is up, so the code cannot outlive its card', async () => {
            const { user } = clipboard();
            openWith(three);
            layOut();
            const region = screen.getByRole('region');

            await user.click(slideQr(1));
            expect(region).toHaveAttribute('inert');

            await user.click(screen.getByRole('button', { name: 'donateBack: Card 1' }));
            expect(region).not.toHaveAttribute('inert');
            expect(document.activeElement).toBe(slideQr(1));
        });

        // An inert element is no pointer target: the press lands on its nearest
        // live ancestor, and vaul looks for the guard from there upwards. Inside
        // the frozen carousel it would never be seen, and the sheet would drag.
        test('its no-drag guard stays live while the carousel is inert', async () => {
            const { user } = clipboard();
            openWith(three);
            layOut();
            const region = screen.getByRole('region');
            const guard = trackEl().closest('[data-vaul-no-drag]');

            await user.click(slideQr(1));

            expect(region).toHaveAttribute('inert');
            expect(guard).not.toBeNull();
            expect(guard?.contains(region)).toBe(true);
            expect(guard?.closest('[inert]')).toBeNull();
            expect(region.parentElement?.closest('[data-vaul-no-drag]')).toBe(guard);
        });

        test('an address’s code leaves the carousel free', async () => {
            const { user } = clipboard();
            openWith([...three, bitcoin, ether]);
            layOut();

            await user.click(screen.getByRole('button', { name: qrName('Bitcoin') }));

            expect(screen.getByRole('region')).not.toHaveAttribute('inert');
        });

        test('a card without one has no action', () => {
            openWith(three);
            layOut();

            expect(
                within(slideEl(3)).queryByRole('button', {
                    name: qrName('Card 3'),
                    hidden: true,
                }),
            ).not.toBeInTheDocument();
        });
    });

    test('qr: false and no link is still the bare card, with no frame or empty row', () => {
        openWith([payable(1)]);

        const face = screen.getByRole('button', { name: 'copy: Card 1' });
        expect(face.parentElement?.className).not.toContain('rounded-2xl');
        expect(face.nextElementSibling).toBeNull();
    });

    test.each([
        ['alone', [payable(1)]],
        ['with a link', [payable(1, 'https://bank.test/1')]],
        ['among rows', [payable(1), { ...bitcoin, qr: false }]],
    ])('qr: false shows no action %s', (_, methods) => {
        openWith(methods);

        expect(screen.queryByRole('button', { name: /^donateQr/ })).not.toBeInTheDocument();
    });
});

describe('on a small phone', () => {
    const face = () => document.querySelector('.font-mono')?.parentElement;

    test('the chrome gives way when cards and rows share the height', () => {
        narrow();
        openWith([card, bitcoin]);

        expect(face()?.className).toContain('p-3');
    });

    test('and not when the card has the sheet to itself', () => {
        narrow();
        openWith([card]);

        expect(face()?.className).toContain('p-4');
    });
});

test('every word it asks for exists in both languages', () => {
    asked.clear();
    openWith([payable(1, 'https://bank.test/1'), payable(2), bitcoin, ether, tron, boosty]);

    const words = Array.from(asked).filter((key) => key.startsWith('donate'));
    expect(words).toContain('donatePayOnline');
    expect(words).toContain('donateCarouselHint');
    for (const key of words) {
        expect(ru).toHaveProperty([key]);
        expect(en).toHaveProperty([key]);
    }
});
