import markers from '@/features/auth/room/markers.json';
import { markersFor, pickSource, sourcesFor, variantFor } from '@/features/auth/room/variants';

describe('variantFor', () => {
    it.each([
        [false, false, 'desktop'],
        [false, true, 'desktop-torn'],
        [true, false, 'phone'],
        [true, true, 'phone-torn'],
    ] as const)('phone %s, torn %s → %s', (phone, torn, name) => {
        expect(variantFor(phone, torn)).toBe(name);
    });
});

describe('sourcesFor', () => {
    it('serves every variant from the public room folder, in both containers, with a poster', () => {
        expect(sourcesFor('phone-torn')).toEqual({
            webm: '/auth-room/phone-torn.webm',
            mp4: '/auth-room/phone-torn.mp4',
            poster: '/auth-room/phone-torn.webp',
        });
    });
});

describe('pickSource', () => {
    it('prefers WebM where VP9 plays, and falls back to MP4 elsewhere (Safari, iOS)', () => {
        const s = sourcesFor('desktop');
        expect(pickSource(s, (type) => (type.includes('webm') ? 'probably' : ''))).toBe(s.webm);
        expect(pickSource(s, () => '')).toBe(s.mp4);
    });
});

describe('markersFor', () => {
    it.each(['desktop', 'desktop-torn', 'phone', 'phone-torn'] as const)(
        'has the Easter eggs of %s inside its own frame',
        (name) => {
            const m = markersFor(name);
            const inside = ([x, y]: [number, number]) =>
                x >= 0 && y >= 0 && x <= m.width && y <= m.height;
            expect(m.goldenPost).toHaveLength(4);
            expect(m.goldenPost.every(inside)).toBe(true);
            expect(m.pupils).toHaveLength(2);
            expect(m.pupils.every(inside)).toBe(true);
            // The pupils are inside the mask.
            for (const [x, y] of m.pupils) {
                expect(x).toBeGreaterThan(m.mask.x);
                expect(x).toBeLessThan(m.mask.x + m.mask.w);
                expect(y).toBeGreaterThan(m.mask.y);
                expect(y).toBeLessThan(m.mask.y + m.mask.h);
            }
        },
    );
});

describe('markers.json', () => {
    // A partial re-render once wrote only the composition it rendered; the
    // other's golden post and eyes then vanished without a sound.
    it('carries both compositions, each in its recording’s size', () => {
        expect(Object.keys(markers).sort()).toEqual(['desktop', 'phone']);
        expect([markers.desktop.width, markers.desktop.height]).toEqual([1920, 1080]);
        expect([markers.phone.width, markers.phone.height]).toEqual([1080, 1920]);
    });
});
