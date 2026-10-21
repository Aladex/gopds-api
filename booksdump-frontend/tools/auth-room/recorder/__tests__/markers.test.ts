import { mergeMarkers, type Markers } from '../markers';

const at = (dx: number, width = 1920, height = 1080): Markers => ({
    width,
    height,
    goldenPost: [
        [700 + dx, 950],
        [870 + dx, 930],
        [950 + dx, 1030],
        [750 + dx, 1060],
    ],
    pupils: [
        [1426 + dx, 802],
        [1519 + dx, 810],
    ],
    mask: { x: 1362 + dx, y: 702, w: 228, h: 239 },
});
const desktop = at(0);
const phone = at(0, 1080, 1920);

describe('mergeMarkers', () => {
    it('keeps the desktop entry when only a phone variant is re-rendered', () => {
        const merged = mergeMarkers({ desktop, phone }, { phone: at(1, 1080, 1920) });

        expect(merged.desktop).toBe(desktop);
        expect(merged.phone).toEqual(at(1, 1080, 1920));
    });

    it('keeps the whole book’s entry when only the torn one is re-rendered', () => {
        const merged = mergeMarkers({ desktop, phone }, { 'phone-torn': at(1, 1080, 1920) });

        expect(merged.phone).toBe(phone);
        expect(merged.desktop).toBe(desktop);
    });

    it('keeps the phone entry on a markers-only run for desktop', () => {
        const merged = mergeMarkers({ desktop, phone }, { desktop: at(0) });

        expect(Object.keys(merged).sort()).toEqual(['desktop', 'phone']);
        expect(merged.phone).toBe(phone);
    });

    it('takes every composition from a full run', () => {
        const merged = mergeMarkers(
            {},
            {
                'desktop-torn': at(1),
                desktop: at(0),
                phone: at(0, 1080, 1920),
                'phone-torn': at(0, 1080, 1920),
            },
        );

        // The whole book is the reference, whichever variant came first.
        expect(merged).toEqual({ desktop: at(0), phone: at(0, 1080, 1920) });
    });

    it('refuses two variants of one composition that disagree', () => {
        expect(() => mergeMarkers({}, { 'desktop-torn': at(5), desktop: at(0), phone })).toThrow(
            /desktop-torn: Easter eggs moved by 5px from desktop/,
        );
    });

    it('refuses a variant rendered alone that no longer agrees with the kept entry', () => {
        expect(() => mergeMarkers({ desktop, phone }, { 'desktop-torn': at(4) })).toThrow(
            /re-render both desktop variants/,
        );
    });

    it('refuses to leave a composition without markers', () => {
        expect(() => mergeMarkers({}, { phone })).toThrow(/markers for desktop missing/);
    });
});
