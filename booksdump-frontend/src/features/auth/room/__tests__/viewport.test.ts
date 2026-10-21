import {
    createCompositionTracker,
    keyboardMayBeOpen,
    ULTRAWIDE_ASPECT,
} from '@/features/auth/room/viewport';

/*
 * A phone's viewport changes height all the time without the phone turning:
 * the URL bar slides away and back, and on Firefox for Android the on-screen
 * keyboard shrinks the layout viewport itself. A 390 × 844 screen with the
 * keyboard up is 390 × 420 — wider than tall enough to read as a desktop. The
 * room must not rebuild itself for that, and must not reallocate its
 * post-processing for every few pixels of URL bar.
 */

describe('which composition a screen gets', () => {
    // Viewports as a browser really has them: full screen, and maximised with
    // its own chrome (tabs, address bar, bookmarks) and the taskbar taken off.
    it.each([
        ['16:10 1920 × 1200, maximised', 'desktop', 1920, 1089],
        ['16:10 2560 × 1600, full screen', 'desktop', 2560, 1600],
        ['16:9 1920 × 1080, full screen', 'desktop', 1920, 1080],
        ['16:9 1920 × 1080, maximised', 'desktop', 1920, 969],
        ['16:9 1920 × 1080, maximised with bookmarks', 'desktop', 1920, 937],
        ['16:9 2560 × 1440, maximised', 'desktop', 2560, 1297],
        ['21:9 2560 × 1080, full screen', 'ultrawide', 2560, 1080],
        ['21:9 2560 × 1080, maximised', 'ultrawide', 2560, 950],
        ['21:9 3440 × 1440, full screen', 'ultrawide', 3440, 1440],
        ['21:9 3440 × 1440, maximised', 'ultrawide', 3440, 1300],
        ['32:10 3840 × 1200, maximised', 'ultrawide', 3840, 1090],
        ['32:9 5120 × 1440, full screen', 'ultrawide', 5120, 1440],
        ['32:9 5120 × 1440, maximised', 'ultrawide', 5120, 1300],
        ['phone 390 × 844', 'phone', 390, 844],
        ['tablet 1024 × 768', 'desktop', 1024, 768],
    ] as const)('%s → %s', (_, expected, w, h) => {
        expect(createCompositionTracker().composition(w, h)).toBe(expected);
    });

    it('draws the ultrawide line where it says, on both sides of it', () => {
        expect(ULTRAWIDE_ASPECT).toBe(2.2);
        expect(createCompositionTracker().composition(2200, 1000)).toBe('ultrawide');
        expect(createCompositionTracker().composition(2199, 1000)).toBe('desktop');
        // The phone line is unchanged.
        expect(createCompositionTracker().composition(799, 1000)).toBe('phone');
        expect(createCompositionTracker().composition(800, 1000)).toBe('desktop');
    });

    it.each([
        ['a landscape phone', 844, 390, 180],
        ['a landscape tablet', 1024, 768, 400],
    ] as const)(
        'keeps %s on the desktop recording while its keyboard opens and closes',
        (_, w, h, shortened) => {
            const tracker = createCompositionTracker();
            expect(tracker.composition(w, h)).toBe('desktop');
            expect(tracker.composition(w, shortened, true)).toBe('desktop');
            expect(tracker.composition(w, h, true)).toBe('desktop');
        },
    );

    it('still follows a desktop window made genuinely wide by shortening it', () => {
        const tracker = createCompositionTracker();
        expect(tracker.composition(1024, 768)).toBe('desktop');
        expect(tracker.composition(1024, 400)).toBe('ultrawide');
        expect(tracker.composition(1024, 768)).toBe('desktop');
    });

    it('keeps a phone a phone while its keyboard squeezes it into a wide strip', () => {
        const tracker = createCompositionTracker();
        tracker.composition(390, 844);
        // 390 × 160 is wider than 2.2 : 1, read naively.
        expect(tracker.composition(390, 160, true)).toBe('phone');
    });
});

describe('createCompositionTracker', () => {
    it('reads a portrait phone as a phone and a desktop window as a desktop', () => {
        expect(createCompositionTracker().phone(390, 844)).toBe(true);
        expect(createCompositionTracker().phone(1440, 900)).toBe(false);
    });

    it('stays a phone while the keyboard is up, and after it goes', () => {
        const tracker = createCompositionTracker();
        expect(tracker.phone(390, 844)).toBe(true);

        expect(tracker.phone(390, 420, true)).toBe(true);
        expect(tracker.phone(390, 300, true)).toBe(true);
        expect(tracker.phone(390, 844, true)).toBe(true);
    });

    it('follows a desktop window made taller and back again: nothing is kept', () => {
        // The reviewer's case: only the bottom edge moves. Without a keyboard
        // there is nothing to wait out, and the aspect decides every time.
        const tracker = createCompositionTracker();

        expect(tracker.phone(600, 600)).toBe(false);
        expect(tracker.phone(600, 1000)).toBe(true);
        expect(tracker.phone(600, 600)).toBe(false);
    });

    it('lets go of the kept height once the keyboard can no longer be the reason', () => {
        const tracker = createCompositionTracker();
        tracker.phone(390, 844);
        tracker.phone(390, 420, true);

        // The field lost focus and the window is genuinely this shape now.
        expect(tracker.phone(390, 420, false)).toBe(false);
    });

    it('turns with the phone: a width change is a new composition', () => {
        const tracker = createCompositionTracker();
        tracker.phone(390, 844);

        expect(tracker.phone(844, 390)).toBe(false);
        expect(tracker.phone(390, 844)).toBe(true);
    });

    it('lets a desktop window grow into a phone shape when it is made taller', () => {
        const tracker = createCompositionTracker();
        expect(tracker.phone(600, 600)).toBe(false);

        expect(tracker.phone(600, 1000)).toBe(true);
    });
});

describe('keyboardMayBeOpen', () => {
    const pointer = (coarse: boolean, noHover = coarse) =>
        vi.spyOn(window, 'matchMedia').mockImplementation(
            (q: string) =>
                ({
                    matches:
                        (q === '(pointer: coarse)' && coarse) || (q === '(hover: none)' && noHover),
                    media: q,
                }) as MediaQueryList,
        );

    afterEach(() => {
        vi.restoreAllMocks();
        (document.activeElement as HTMLElement | null)?.blur?.();
        document.body.innerHTML = '';
    });

    it('is true on a touch device with a text field focused', () => {
        pointer(true);
        document.body.innerHTML = '<input id="f">';
        (document.getElementById('f') as HTMLInputElement).focus();

        expect(keyboardMayBeOpen()).toBe(true);
    });

    it('trusts a device without hover even if it does not call its pointer coarse', () => {
        pointer(false, true);
        document.body.innerHTML = '<input id="f">';
        (document.getElementById('f') as HTMLInputElement).focus();

        expect(keyboardMayBeOpen()).toBe(true);
    });

    it('is false on a touch device with nothing to type into', () => {
        pointer(true);
        document.body.innerHTML = '<button id="b">x</button>';
        (document.getElementById('b') as HTMLButtonElement).focus();

        expect(keyboardMayBeOpen()).toBe(false);
    });

    it('is false on a desktop, field focused or not: its windows really resize', () => {
        pointer(false);
        document.body.innerHTML = '<textarea id="t"></textarea>';
        (document.getElementById('t') as HTMLTextAreaElement).focus();

        expect(keyboardMayBeOpen()).toBe(false);
    });
});
