import { Vector3 } from 'three';

import { CAMERA_Z, TABLE_Y, halfExtentAt, sceneLayout } from '../layout';

/*
 * The ultrawide recording is cropped to 21:9 on most ultrawide screens and
 * shown whole on 32:9 ones, and the app treats its middle as the desktop
 * picture: same camera, same desk. What it adds lies out at the sides.
 */

const desktop = sceneLayout(false);
const wide = sceneLayout(false, true);

/** Where a point on the table lands across the frame, as a fraction of half the width. */
function acrossFrame(x: number, z: number, aspect: number): number {
    // Half the frame's width at that depth, from the same lens as the scene.
    return x / halfExtentAt(z, desktop.fov, aspect).x;
}

describe('sceneLayout: the ultrawide composition', () => {
    it('keeps the desktop camera and desk exactly', () => {
        expect(wide.fov).toBe(desktop.fov);
        expect(wide.lookAtY).toBe(desktop.lookAtY);
        expect(wide.bookPos).toEqual(desktop.bookPos);
        expect(wide.maskPos).toEqual(desktop.maskPos);
        expect(wide.obstacles).toEqual(desktop.obstacles);
        // The golden post is the first lying page, and stays where it was.
        expect(wide.lyingSpots.slice(0, desktop.lyingSpots.length)).toEqual(desktop.lyingSpots);
    });

    it('fills the extra width: more sheets, and a far layer as wide as the frame', () => {
        expect(wide.sheetCount).toBeGreaterThan(desktop.sheetCount);
        expect(wide.farCount).toBeGreaterThan(desktop.farCount);
        // The far layer's nearest depth, at 32:9, must be covered edge to edge.
        expect(wide.farHalfX).toBeGreaterThanOrEqual(halfExtentAt(-13, wide.fov, 32 / 9).x);
        expect(desktop.farHalfX).toBeGreaterThanOrEqual(halfExtentAt(-13, desktop.fov, 16 / 9).x);
    });

    it('lays its extra pages where only the wide frame sees them, and inside it', () => {
        const extra = wide.lyingSpots.slice(desktop.lyingSpots.length);
        expect(extra.length).toBeGreaterThan(0);
        for (const [x, z] of extra) {
            expect(Math.abs(acrossFrame(x, z, 16 / 9))).toBeGreaterThan(1);
            expect(Math.abs(acrossFrame(x, z, 32 / 9))).toBeLessThan(0.95);
            expect(z).toBeLessThan(CAMERA_Z);
            // Clear of the desk's solids.
            for (const o of wide.obstacles) {
                expect(new Vector3(x, TABLE_Y, z).distanceTo(o.c)).toBeGreaterThan(o.r + 0.7);
            }
        }
    });

    it('is a desktop composition only', () => {
        expect(() => sceneLayout(true, true)).toThrow();
    });
});
