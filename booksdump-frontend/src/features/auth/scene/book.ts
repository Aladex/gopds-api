import { Matrix4, Quaternion, Vector3 } from 'three';

import { PAGE_H } from '@/features/auth/scene/layout';

/**
 * The open book: the shape of its page blocks, the turning sheet, and the
 * moment a turning sheet is torn off and handed to the swarm.
 *
 * Everything here is arithmetic on vertex arrays and matrices, so the hand-off
 * — the part that has to be exact — can be checked without a GPU.
 */

/** Columns (spine to fore-edge) and rows (head to tail) of every sheet's grid. */
export const FLIP_S = 24;
export const FLIP_Z = 10;

const clamp01 = (s: number) => Math.min(1, Math.max(0, s));

/**
 * Height of the page block surface at distance s (0 spine .. 1 fore-edge): it
 * rises out of the gutter and flattens out toward the edge, as a sewn book
 * does when it lies open.
 */
export function blockHeight(s: number): number {
    s = clamp01(s);
    return 0.05 + 0.13 * (1 - (1 - s) ** 3) - 0.03 * s;
}

/** The angle of that surface at s; the turning sheet starts lying along it. */
export function blockSlope(s: number): number {
    s = clamp01(s);
    return Math.atan(0.39 * (1 - s) ** 2 - 0.03);
}

/** The sheet lifts slowly and settles slowly. */
export function easeTurn(p: number): number {
    return p < 0.5 ? 2 * p * p : 1 - (-2 * p + 2) ** 2 / 2;
}

/**
 * Lay out the turning sheet in the book's own frame. p: 0 lying on the right
 * .. 1 lying on the left. The spine leads and the fore-edge trails, and one
 * corner leads a little more than the other, so the sheet curls instead of
 * rotating as a board.
 *
 * The grid is walked column by column from the spine, each step one column
 * long at the angle its point in the turn gives it, so the sheet never
 * stretches however it curls.
 */
export function layoutFlip(pos: Float32Array, p: number): void {
    const cols = FLIP_S + 1;
    for (let j = 0; j <= FLIP_Z; j++) {
        const zl = -(PAGE_H / 2 - (j / FLIP_Z) * PAGE_H);
        const lag = 0.6 * (1 + (0.3 * zl) / (PAGE_H / 2));
        let x = 0;
        let y = blockHeight(0) + 0.006;
        for (let i = 0; i <= FLIP_S; i++) {
            const s = i / FLIP_S;
            const idx = (j * cols + i) * 3;
            pos[idx] = x;
            pos[idx + 1] = y;
            pos[idx + 2] = zl;
            let ps = clamp01(p * (1 + lag) - lag * s);
            ps = ps * ps * (3 - 2 * ps);
            const a = blockSlope(s);
            const phi = a + ps * (Math.PI - 2 * a);
            x += Math.cos(phi) / FLIP_S;
            y += Math.sin(phi) / FLIP_S;
        }
    }
}

/** A fresh vertex array for one sheet's grid. */
export function flipArray(): Float32Array {
    return new Float32Array((FLIP_S + 1) * (FLIP_Z + 1) * 3);
}

/** What the book is doing: which spread is open and how far the turn has got. */
export interface BookState {
    /** The sheet lying on the right is sequence[page]. */
    page: number;
    flipping: boolean;
    /** Progress of the current turn, 0..1, before easing. */
    p: number;
    /** Seconds the current turn takes. */
    dur: number;
    /** Seconds until the next turn starts. */
    next: number;
    /** Progress at which this turn tears the sheet off, or -1 for a full turn. */
    tearAt: number;
}

/** The book opens on the stamped verso facing the plate; the first turn is ~9 s in. */
export function newBookState(): BookState {
    return { page: 1, flipping: false, p: 0, dur: 1.6, next: 9, tearAt: -1 };
}

const between = (random: () => number, a: number, b: number) => a + random() * (b - a);

/**
 * Start a turn. A hand hovering near the book makes it a quick one, and quick
 * turns tear more often — the air the hand moves is what tears them. A sheet
 * that is not tearable (the plate: it already flies as a sheet of its own)
 * always turns all the way.
 */
export function startTurn(
    st: BookState,
    fast: boolean,
    tearable: boolean,
    random: () => number = Math.random,
): void {
    st.flipping = true;
    st.p = 0;
    st.dur = fast ? between(random, 0.9, 1.2) : between(random, 1.5, 2.0);
    st.tearAt = tearable && random() < (fast ? 0.55 : 0.3) ? between(random, 0.45, 0.6) : -1;
}

/** A full turn or a tear has finished: the next spread, and a pause before the next turn. */
export function finishTurn(st: BookState, random: () => number = Math.random): void {
    st.flipping = false;
    st.page++;
    st.next = between(random, 3.5, 7);
}

/** Everything a free sheet needs to carry on exactly where the turning sheet left off. */
export interface TearHandOff {
    position: Vector3;
    quaternion: Quaternion;
    /** The turning sheet's vertices in the free sheet's own frame, same grid. */
    morph: Float32Array;
}

/**
 * How a torn sheet leaves: slowly, a little up and away from the spine, not
 * spinning. The turn it came out of is fast — its centre moves at up to six
 * units a second at the point of tearing — and carrying that over flung the
 * page across the room, which is the jerk the owner turned down. The sheet's
 * shape and place carry over exactly; its motion starts gentle and the air
 * takes it from there.
 */
export const TEAR_VELOCITY: readonly [number, number, number] = [0.05, 0.15, 0.02];

const MID_ROW = Math.floor(FLIP_Z / 2);
const MID_COL = Math.floor(FLIP_S / 2);
const vertex = (row: number, col: number) => (row * (FLIP_S + 1) + col) * 3;

const tA = new Vector3();
const tB = new Vector3();

/**
 * Hand the turning sheet over to the swarm.
 *
 * `current` is the layout on screen this frame, in the book's frame;
 * `bookWorld` takes it to the world. The free sheet is placed at the turning
 * sheet's centre, turned to its orientation — x along the curl through the
 * middle, y from tail to head — and given its vertices one for one, the grids
 * being the same. On the frame of the tear nothing on screen moves.
 */
export function tearHandOff(current: Float32Array, bookWorld: Matrix4): TearHandOff {
    const mid = vertex(MID_ROW, MID_COL);
    const a = vertex(MID_ROW, MID_COL - 2);
    const b = vertex(MID_ROW, MID_COL + 2);
    const position = new Vector3(current[mid], current[mid + 1], current[mid + 2]).applyMatrix4(
        bookWorld,
    );
    const xAxis = new Vector3(current[b], current[b + 1], current[b + 2])
        .applyMatrix4(bookWorld)
        .sub(tB.set(current[a], current[a + 1], current[a + 2]).applyMatrix4(bookWorld))
        .normalize();
    // The sheet's own y runs from tail to head, which in the book is -z.
    const yAxis = new Vector3(0, 0, -1).transformDirection(bookWorld);
    yAxis.addScaledVector(xAxis, -yAxis.dot(xAxis)).normalize();
    const zAxis = new Vector3().crossVectors(xAxis, yAxis);
    const quaternion = new Quaternion().setFromRotationMatrix(
        new Matrix4().makeBasis(xAxis, yAxis, zAxis),
    );

    const inv = new Matrix4().compose(position, quaternion, new Vector3(1, 1, 1)).invert();
    const morph = new Float32Array(current.length);
    for (let i = 0; i < current.length; i += 3) {
        tA.set(current[i], current[i + 1], current[i + 2])
            .applyMatrix4(bookWorld)
            .applyMatrix4(inv);
        morph[i] = tA.x;
        morph[i + 1] = tA.y;
        morph[i + 2] = tA.z;
    }
    return { position, quaternion, morph };
}

/** The sheet a tear may reuse: an ordinary one, out of sight. */
export function pickSpare<T extends { kind: string | null; position: Vector3 }>(
    sheets: readonly T[],
    offscreen: (p: Vector3) => boolean,
): T | undefined {
    // The golden post and the plate are one of a kind: reusing either for a
    // torn page would make it vanish from wherever it was.
    return sheets.find((q) => !q.kind && offscreen(q.position));
}

/**
 * What is left of a page torn out of the book, for the error page: a ragged
 * strip at the gutter where the right-hand page was. The page is lost, like
 * the route.
 *
 * Returned as an outline in the right page's own plane — x from the gutter
 * toward the fore-edge, z from tail to head — starting and ending at the
 * gutter. The free edge is a tear rather than a cut: it wanders between a few
 * and a dozen-odd hundredths of a page in from the gutter, with the small
 * spikes paper fibres leave.
 */
export function tornStub(random: () => number = Math.random): [number, number][] {
    const steps = 32;
    const edge: [number, number][] = [];
    let x = 0.08;
    for (let i = 0; i <= steps; i++) {
        const z = -PAGE_H / 2 + (i / steps) * PAGE_H;
        // A slow wander, pulled back toward the middle of the band, plus a
        // fibre spike now and then.
        x += (random() - 0.5) * 0.035 + (0.085 - x) * 0.25;
        const spike = random() < 0.2 ? random() * 0.025 : 0;
        edge.push([Math.min(0.17, Math.max(0.03, x + spike)), z]);
    }
    return [[0, -PAGE_H / 2], ...edge, [0, PAGE_H / 2]];
}
