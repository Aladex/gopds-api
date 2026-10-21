/**
 * Portrait phones get their own composition rather than a squeezed desktop
 * one: a wider lens, the objects pulled in toward the centre, and a lighter
 * pipeline — fewer sheets, smaller shadow maps, no ambient occlusion or depth
 * of field — because the same frame on a phone GPU costs several times more.
 *
 * On its own, without three.js, because the screens ask it before the scene
 * chunk is loaded and must not pull the library in to do so.
 */
export function isPhoneViewport(width: number, height: number): boolean {
    return width / height < 0.8;
}

export type Composition = 'phone' | 'desktop' | 'ultrawide';

/**
 * From this aspect up a screen gets the ultrawide recording (32:9, see
 * tools/auth-room). It is the same camera as the desktop one, so on any screen
 * wider than 16:9 it only loses its sides, never the desk; the desktop video
 * there loses top and bottom, and the golden post lies at the very bottom.
 * The line sits in the gap between real browser viewports: 16:9 and 16:10
 * monitors, maximised with their chrome, come to 1.76–2.05; 21:9 ones to
 * 2.37–2.69, and 32:9 beyond. A desktop that is not wide keeps the lighter
 * desktop recording.
 */
export const ULTRAWIDE_ASPECT = 2.2;

/**
 * Which composition the viewport calls for, without being fooled by a phone
 * changing height in place.
 *
 * On Firefox for Android the on-screen keyboard shrinks the layout viewport
 * itself: read naively, a 390 × 844 phone with the keyboard up is 390 × 420
 * and reads as a desktop, and the whole room was rebuilt — twice, keyboard up
 * and down — the moment the reader tapped the login field. So while the
 * keyboard may be up (see keyboardMayBeOpen) the tallest height seen at the
 * current width is what counts.
 *
 * Only then. A desktop window made taller and back again, or split-screen,
 * changes height for real, and the aspect decides every time — a kept height
 * once left a square desktop window in the phone composition until reload.
 * A width change always starts over: a phone turning, a window resized.
 */
export function createCompositionTracker() {
    let width = -1;
    let tallest = 0;
    return {
        /**
         * The composition for the viewport. The keyboard rule applies to the
         * whole decision, the ultrawide line included: a landscape phone or
         * tablet squeezed by its keyboard is not a wide screen either.
         */
        composition(w: number, h: number, keyboard = false): Composition {
            tallest = w === width && keyboard ? Math.max(tallest, h) : h;
            width = w;
            if (isPhoneViewport(w, tallest)) return 'phone';
            return w / tallest >= ULTRAWIDE_ASPECT ? 'ultrawide' : 'desktop';
        },
        phone(w: number, h: number, keyboard = false): boolean {
            return this.composition(w, h, keyboard) === 'phone';
        },
    };
}

/**
 * A device whose on-screen keyboard can take the viewport's height: its
 * primary pointer is coarse, or it cannot hover at all. Either is enough, so a
 * phone that misreports one is still caught; a desktop — a touchscreen laptop
 * included, whose primary pointer is its touchpad — has neither.
 */
export function touchFirst(): boolean {
    return (
        window.matchMedia('(pointer: coarse)').matches || window.matchMedia('(hover: none)').matches
    );
}

/**
 * Whether a height change may be the on-screen keyboard: a touch-first device
 * with a field focused that a keyboard types into. The keyboard only comes up
 * for such a field, and a desktop is never touch-first, so a desktop resize is
 * never mistaken for one.
 *
 * The visual viewport was the other candidate and is no use here: Firefox for
 * Android shrinks the layout viewport for the keyboard, so the two agree.
 */
export function keyboardMayBeOpen(): boolean {
    if (!touchFirst()) return false;
    const el = document.activeElement as HTMLElement | null;
    if (!el) return false;
    if (el.isContentEditable) return true;
    if (el.tagName === 'TEXTAREA') return true;
    if (el.tagName !== 'INPUT') return false;
    const type = (el as HTMLInputElement).type;
    return ![
        'button',
        'checkbox',
        'color',
        'file',
        'hidden',
        'image',
        'radio',
        'range',
        'reset',
        'submit',
    ].includes(type);
}
