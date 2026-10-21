import {
    createCompositionTracker,
    keyboardMayBeOpen,
    needsResize,
} from '@/features/auth/scene/viewport';

/*
 * A phone's viewport changes height all the time without the phone turning:
 * the URL bar slides away and back, and on Firefox for Android the on-screen
 * keyboard shrinks the layout viewport itself. A 390 × 844 screen with the
 * keyboard up is 390 × 420 — wider than tall enough to read as a desktop. The
 * room must not rebuild itself for that, and must not reallocate its
 * post-processing for every few pixels of URL bar.
 */

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

describe('needsResize', () => {
    const was = { width: 390, height: 844 };

    it('ignores the URL bar sliding: a small height-only change', () => {
        expect(needsResize(was, { width: 390, height: 788 })).toBe(false);
        expect(needsResize(was, { width: 390, height: 900 })).toBe(false);
    });

    it('follows the keyboard: a large height-only change', () => {
        expect(needsResize(was, { width: 390, height: 420 })).toBe(true);
    });

    it('always follows a width change', () => {
        expect(needsResize(was, { width: 391, height: 844 })).toBe(true);
    });

    it('does nothing for no change', () => {
        expect(needsResize(was, { ...was })).toBe(false);
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
