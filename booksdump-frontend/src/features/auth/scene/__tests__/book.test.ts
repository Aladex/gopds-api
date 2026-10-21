import { Euler, Matrix4, Quaternion, Vector3 } from 'three';

import {
    FLIP_S,
    FLIP_Z,
    TEAR_VELOCITY,
    blockHeight,
    easeTurn,
    finishTurn,
    flipArray,
    layoutFlip,
    newBookState,
    pickSpare,
    startTurn,
    tearHandOff,
    tornStub,
} from '@/features/auth/scene/book';
import { PAGE_H } from '@/features/auth/scene/layout';
import { BOUND_R } from '@/features/auth/scene/layout';
import { createSheet, deform, sheetGrid, type Sheet } from '@/features/auth/scene/physics';

/** The book as the desktop scene places it: on the table, turned toward the room. */
const bookWorld = new Matrix4().compose(
    new Vector3(-3.0, -2.4, 0.9),
    new Quaternion().setFromEuler(new Euler(0, 0.32, 0)),
    new Vector3(1, 1, 1),
);

/** The world position of every vertex of a free sheet as the renderer would draw it. */
function worldVertices(sheet: Sheet, local: Float32Array): Vector3[] {
    const world = new Matrix4().compose(sheet.position, sheet.quaternion, new Vector3(1, 1, 1));
    const out: Vector3[] = [];
    for (let i = 0; i < local.length; i += 3)
        out.push(new Vector3(local[i], local[i + 1], local[i + 2]).applyMatrix4(world));
    return out;
}

describe('the turning sheet', () => {
    it('shares its grid with every free sheet, so a tear can map vertices one for one', () => {
        expect(sheetGrid().length).toBe(flipArray().length);
        expect(flipArray().length).toBe((FLIP_S + 1) * (FLIP_Z + 1) * 3);
    });

    it('lies on the right page at the start and on the left at the end', () => {
        const pos = flipArray();
        const fore = (FLIP_S + 0) * 3; // fore-edge of the first row
        layoutFlip(pos, 0);
        expect(pos[fore]).toBeGreaterThan(0.9);
        layoutFlip(pos, 1);
        expect(pos[fore]).toBeLessThan(-0.9);
    });

    it('never stretches: each column step stays one column long at every point of the turn', () => {
        const pos = flipArray();
        for (const p of [0, 0.2, 0.5, 0.8, 1]) {
            layoutFlip(pos, easeTurn(p));
            for (let i = 0; i < FLIP_S; i++) {
                const a = i * 3;
                const b = (i + 1) * 3;
                expect(Math.hypot(pos[b] - pos[a], pos[b + 1] - pos[a + 1])).toBeCloseTo(
                    1 / FLIP_S,
                    6,
                );
            }
        }
    });

    it('starts on the page block surface, not inside it', () => {
        const pos = flipArray();
        layoutFlip(pos, 0);
        expect(pos[1]).toBeGreaterThan(blockHeight(0));
    });
});

describe('tearing a sheet off', () => {
    it.each([0.45, 0.5, 0.52, 0.6])(
        'puts every vertex exactly where the turning sheet had it (tear at %f)',
        (p) => {
            const turning = flipArray();
            layoutFlip(turning, easeTurn(p));
            const expected: Vector3[] = [];
            for (let i = 0; i < turning.length; i += 3)
                expected.push(
                    new Vector3(turning[i], turning[i + 1], turning[i + 2]).applyMatrix4(bookWorld),
                );

            const sheet = createSheet(null);
            const handOff = tearHandOff(turning, bookWorld);
            sheet.position.copy(handOff.position);
            sheet.quaternion.copy(handOff.quaternion);
            sheet.morph = handOff.morph;
            sheet.morphT = 1;
            // Whatever shape the free sheet had before must not leak through.
            sheet.bend = 0.9;
            sheet.twist = -0.4;
            sheet.flutter = 0.1;

            const local = sheet.base.slice();
            deform(sheet, local, 3.21);
            const drawn = worldVertices(sheet, local);

            expect(drawn).toHaveLength(expected.length);
            let worst = 0;
            drawn.forEach((v, i) => (worst = Math.max(worst, v.distanceTo(expected[i]))));
            expect(worst).toBeLessThan(1e-5);
        },
    );

    it.each([0.45, 0.5, 0.6])(
        'centres the free sheet so its bounding sphere holds the torn shape (tear at %f)',
        (p) => {
            // The separation keeps sheets apart by their centres, trusting that
            // every vertex is within BOUND_R of it. A frame anchored anywhere
            // but the middle of the sheet would break that for the torn one.
            const turning = flipArray();
            layoutFlip(turning, easeTurn(p));
            const { morph } = tearHandOff(turning, bookWorld);
            let far = 0;
            for (let i = 0; i < morph.length; i += 3)
                far = Math.max(far, Math.hypot(morph[i], morph[i + 1], morph[i + 2]));
            expect(far).toBeLessThanOrEqual(BOUND_R);
            expect(far).toBeLessThan(0.9);
        },
    );

    it('keeps the orientation proper, so lighting does not flip on the frame of the tear', () => {
        const turning = flipArray();
        layoutFlip(turning, easeTurn(0.5));
        const { quaternion } = tearHandOff(turning, bookWorld);
        const m = new Matrix4().makeRotationFromQuaternion(quaternion);
        expect(m.determinant()).toBeCloseTo(1, 6);
    });

    it('leaves gently and without spin, as the owner approved', () => {
        // Not the turning sheet's own speed: at the tear point that is up to
        // six units a second, which flung the page across the room.
        expect(Math.hypot(...TEAR_VELOCITY)).toBeLessThan(0.2);
    });

    it('then eases out of the turning shape rather than snapping to its own', () => {
        const turning = flipArray();
        layoutFlip(turning, easeTurn(0.5));
        const sheet = createSheet(null);
        const handOff = tearHandOff(turning, bookWorld);
        sheet.morph = handOff.morph;

        const a = sheet.base.slice();
        const b = sheet.base.slice();
        let worstStep = 0;
        // One frame at a time over the morph: no vertex may jump.
        for (let k = 60; k > 0; k--) {
            sheet.morphT = k / 60;
            deform(sheet, a, 0);
            sheet.morphT = (k - 1) / 60;
            deform(sheet, b, 0);
            for (let i = 0; i < a.length; i++)
                worstStep = Math.max(worstStep, Math.abs(a[i] - b[i]));
        }
        expect(worstStep).toBeLessThan(0.05);
    });
});

describe('which sheet a tear reuses', () => {
    const off = () => true;

    it('never the golden post or the plate, even when only they are out of sight', () => {
        const gold = createSheet('gold');
        const plate = createSheet('plate');
        const plain = createSheet(null);
        plain.position.set(0, 0, 0);
        expect(pickSpare([gold, plate, plain], (p) => p !== plain.position)).toBeUndefined();
    });

    it('an ordinary sheet that is out of sight', () => {
        const gold = createSheet('gold');
        const plain = createSheet(null);
        expect(pickSpare([gold, plain], off)).toBe(plain);
    });

    it('none that the reader can see', () => {
        expect(pickSpare([createSheet(null)], () => false)).toBeUndefined();
    });
});

describe('the turn schedule', () => {
    it('opens on the plate spread and waits before the first turn', () => {
        const st = newBookState();
        expect(st.page).toBe(1);
        expect(st.flipping).toBe(false);
        expect(st.next).toBeGreaterThan(8);
    });

    it('never tears a sheet that is not tearable', () => {
        const st = newBookState();
        for (let i = 0; i < 200; i++) {
            startTurn(st, true, false, () => 0);
            expect(st.tearAt).toBe(-1);
        }
    });

    it('tears in the middle of the turn, when it tears', () => {
        const st = newBookState();
        startTurn(st, true, true, () => 0);
        expect(st.tearAt).toBeGreaterThanOrEqual(0.45);
        expect(st.tearAt).toBeLessThanOrEqual(0.6);
    });

    it('moves on to the next spread after a turn', () => {
        const st = newBookState();
        startTurn(st, false, true);
        finishTurn(st);
        expect(st.page).toBe(2);
        expect(st.flipping).toBe(false);
    });
});

describe('the torn-out page on the error page', () => {
    /** A deterministic random source, so the edge is the same on every run. */
    const seeded = (seed: number) => () => {
        seed = (seed * 16807) % 2147483647;
        return seed / 2147483647;
    };
    const stub = tornStub(seeded(42));

    it('is bound in at the gutter along the whole height of the page', () => {
        const atGutter = stub.filter(([x]) => x === 0);
        expect(atGutter).toHaveLength(2);
        expect(Math.min(...stub.map(([, z]) => z))).toBeCloseTo(-PAGE_H / 2, 9);
        expect(Math.max(...stub.map(([, z]) => z))).toBeCloseTo(PAGE_H / 2, 9);
    });

    it('is a narrow strip: most of the page is gone', () => {
        for (const [x] of stub) {
            expect(x).toBeGreaterThanOrEqual(0);
            expect(x).toBeLessThanOrEqual(0.18);
        }
    });

    it('is torn, not cut: the free edge is ragged', () => {
        const edge = stub.filter(([x]) => x > 0);
        expect(edge.length).toBeGreaterThan(20);
        const xs = edge.map(([x]) => x);
        const mean = xs.reduce((a, b) => a + b, 0) / xs.length;
        const spread = Math.sqrt(xs.reduce((a, b) => a + (b - mean) ** 2, 0) / xs.length);
        expect(spread).toBeGreaterThan(0.008);
        // Never folding back on itself: the edge runs from tail to head.
        for (let i = 1; i < edge.length; i++) expect(edge[i][1]).toBeGreaterThan(edge[i - 1][1]);
    });
});
