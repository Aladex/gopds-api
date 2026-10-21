import { coverFit, toBox } from '@/features/auth/room/cover';

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
