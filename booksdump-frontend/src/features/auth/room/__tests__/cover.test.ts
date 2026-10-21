import { coverFit, toBox } from '@/features/auth/room/cover';
import { alignFor, markersFor } from '@/features/auth/room/variants';

/*
 * The video fills the screen the way object-fit: cover does: scaled until it
 * covers both sides, centred, the overflow cut off. The Easter eggs sit on
 * top of it, so their positions have to follow the same arithmetic on every
 * screen shape.
 */

const video = { width: 1920, height: 1080 };

describe('coverFit', () => {
    it('is the identity for a screen the video’s own size', () => {
        expect(coverFit(video, { width: 1920, height: 1080 })).toEqual({ scale: 1, dx: 0, dy: 0 });
    });

    it('scales by width and crops top and bottom on a wider screen', () => {
        const c = coverFit(video, { width: 2560, height: 1080 });
        expect(c.scale).toBeCloseTo(2560 / 1920);
        expect(c.dx).toBeCloseTo(0);
        expect(c.dy).toBeCloseTo((1080 - 1080 * (2560 / 1920)) / 2);
        expect(c.dy).toBeLessThan(0);
    });

    it('scales by height and crops the sides on a narrower screen', () => {
        const c = coverFit(video, { width: 1024, height: 768 });
        expect(c.scale).toBeCloseTo(768 / 1080);
        expect(c.dy).toBeCloseTo(0);
        expect(c.dx).toBeCloseTo((1024 - 1920 * (768 / 1080)) / 2);
        expect(c.dx).toBeLessThan(0);
    });

    it('fills a phone with a portrait video, cropping whichever side overflows', () => {
        const phone = { width: 1080, height: 1920 };
        const tall = coverFit(phone, { width: 390, height: 844 });
        expect(tall.scale).toBeCloseTo(844 / 1920);
        expect(tall.dx).toBeLessThan(0);
        const short = coverFit(phone, { width: 390, height: 600 });
        expect(short.scale).toBeCloseTo(390 / 1080);
        expect(short.dy).toBeLessThan(0);
    });
});

describe('toBox', () => {
    it.each([
        [1920, 1080],
        [1440, 900],
        [2560, 1080],
        [1024, 768],
        [800, 1280],
    ])(
        'keeps the video’s centre at the screen’s centre and corners on the cover edges (%i × %i)',
        (w, h) => {
            const c = coverFit(video, { width: w, height: h });
            const [cx, cy] = toBox([960, 540], c);
            expect(cx).toBeCloseTo(w / 2);
            expect(cy).toBeCloseTo(h / 2);
            // The video covers the box: its corners lie on or beyond the box's.
            const [x0, y0] = toBox([0, 0], c);
            const [x1, y1] = toBox([1920, 1080], c);
            expect(x0).toBeLessThanOrEqual(1e-9);
            expect(y0).toBeLessThanOrEqual(1e-9);
            expect(x1).toBeGreaterThanOrEqual(w - 1e-9);
            expect(y1).toBeGreaterThanOrEqual(h - 1e-9);
        },
    );
});

describe('coverFit anchored, as object-position does it', () => {
    it('puts the whole crop on the side away from the anchor', () => {
        const bottom = coverFit(video, { width: 1920, height: 945 }, { x: 0.5, y: 1 });
        expect(bottom.scale).toBe(1);
        // All 135 rows of overflow come off the top.
        expect(bottom.dy).toBeCloseTo(945 - 1080);
        const top = coverFit(video, { width: 1920, height: 945 }, { x: 0.5, y: 0 });
        expect(top.dy).toBeCloseTo(0);
    });

    it('is the centred fit by default and at 50 % 50 %', () => {
        const box = { width: 2560, height: 1080 };
        expect(coverFit(video, box, { x: 0.5, y: 0.5 })).toEqual(coverFit(video, box));
    });

    it('anchors horizontally the same way when the sides overflow', () => {
        const c = coverFit(video, { width: 1024, height: 768 }, { x: 1, y: 1 });
        expect(c.dy).toBeCloseTo(0);
        expect(c.dx).toBeCloseTo(1024 - 1920 * (768 / 1080));
    });
});

describe('the desktop framing across the desktop band', () => {
    // Every viewport that gets the desktop recording and is wider than it:
    // 16:9 itself, maximised 1080p and 1440p windows with their browser chrome,
    // up to the ultrawide line (2.2 : 1).
    it.each([
        [1920, 1080],
        [1920, 969],
        [1920, 945],
        [1920, 937],
        [2560, 1270],
        [2560, 1297],
        [2199, 1000],
    ])('keeps the golden post and the emblem in view at %i × %i', (width, height) => {
        const m = markersFor('desktop');
        const fit = coverFit(
            { width: m.width, height: m.height },
            { width, height },
            alignFor('desktop'),
        );
        const inside = ([x, y]: [number, number]) => x >= 0 && x <= width && y >= 0 && y <= height;
        for (const p of m.goldenPost) expect(inside(toBox(p, fit))).toBe(true);
        expect(inside(toBox([m.mask.x, m.mask.y], fit))).toBe(true);
        expect(inside(toBox([m.mask.x + m.mask.w, m.mask.y + m.mask.h], fit))).toBe(true);
    });

    it('needed it: centred, the golden post falls off the bottom of a maximised 1080p window', () => {
        const m = markersFor('desktop');
        const fit = coverFit({ width: m.width, height: m.height }, { width: 1920, height: 945 });
        const lowest = Math.max(...m.goldenPost.map((p) => toBox(p, fit)[1]));
        expect(lowest).toBeGreaterThan(945);
    });

    it('leaves the ultrawide and phone recordings centred', () => {
        for (const v of ['ultrawide', 'ultrawide-torn', 'phone', 'phone-torn'] as const) {
            expect(alignFor(v)).toEqual({ x: 0.5, y: 0.5 });
        }
        expect(alignFor('desktop-torn')).toEqual(alignFor('desktop'));
    });
});
