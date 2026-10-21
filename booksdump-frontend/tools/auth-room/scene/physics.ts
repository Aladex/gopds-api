import { Euler, MathUtils, PlaneGeometry, Quaternion, Vector3 } from 'three';

import { FLIP_S, FLIP_Z } from './book';
import {
    BOUND_R,
    FLOOR,
    HARD_DIST,
    MID_Z,
    PAGE_H,
    PAGE_W,
    SOFT_DIST,
    type Obstacle,
} from './layout';

/**
 * Paper in moving air: how the free sheets fly, bend and keep out of each
 * other's way.
 *
 * None of this touches the GPU. A sheet here is its pose, its motion and the
 * three numbers its shape is made of; the glue copies the pose into a mesh and
 * runs `deform` into its vertex buffer. That is what lets the two promises the
 * scene makes — sheets never pass through each other, and never through the
 * table or anything on it — be checked frame by frame in a test.
 *
 * The model is deliberately not rigid-body physics. Paper is all drag: it
 * reaches the speed of the air around it within a second, falls barely at all,
 * and catches more air face-on than edge-on. So velocity relaxes toward the
 * wind, faster the more squarely the sheet faces it, and the shape is driven
 * by the same relative wind pressing on its face.
 */

export type SheetKind = 'gold' | 'plate' | null;

export interface Sheet {
    /** World position of the sheet's centre; the glue shares it with the mesh. */
    position: Vector3;
    quaternion: Quaternion;
    vel: Vector3;
    /** Angular velocity in the sheet's own frame, radians per second. */
    spin: Vector3;
    /** The idle tumble each sheet relaxes back to after a gust. */
    baseSpin: Vector3;
    /** Curl along the width, twist across the diagonal, and a travelling ripple. */
    bend: number;
    twist: number;
    flutter: number;
    phase: number;
    /**
     * Freshly torn off the book and still inside its keep-out zone. Such a
     * sheet is eased out by forces only; the hard limits take it over once it
     * has cleared every solid, so it is never snapped.
     */
    lifting: boolean;
    /** The golden post and the plate are one of a kind and never reused. */
    kind: SheetKind;
    /** A shape to start from (the turning sheet's), faded out over `morphT`. */
    morph: Float32Array | null;
    morphT: number;
    /** The flat grid, same as the turning sheet's, so vertices map one for one. */
    base: Float32Array;
}

/** What the physics needs to know about the camera. */
export interface View {
    /** Half the visible width and height at depth z. */
    halfExtent: (z: number) => { x: number; y: number };
    cameraY: number;
}

/** The cursor as a gust, evaluated at any depth: where it is and how fast it moves there. */
export interface Gust {
    strength: number;
    at: (z: number) => Vector3;
    vx: (z: number) => number;
    vy: (z: number) => number;
}

/** Seconds over which a torn sheet's shape eases from the turning one into free flight. */
export const MORPH_SECONDS = 1.1;
/** No sheet moves faster than this, whatever the hand does. */
export const MAX_SPEED = 1.8;
/** Iterations of the hard limits; one is enough unless something is crowded. */
const MAX_PASSES = 64;
/**
 * Slack in the hard limits. A pair pushed to exactly HARD_DIST measures a
 * hair under it in floating point, and without this every pass would count
 * as a correction and the loop would always run to MAX_PASSES.
 */
const SLACK = 1e-9;

const between = (random: () => number, a: number, b: number) => a + random() * (b - a);

/** The flat vertex grid every sheet starts from. */
export function sheetGrid(): Float32Array {
    const geo = new PlaneGeometry(PAGE_W, PAGE_H, FLIP_S, FLIP_Z);
    const grid = Float32Array.from(geo.attributes.position.array);
    geo.dispose();
    return grid;
}

export function createSheet(kind: SheetKind, random: () => number = Math.random): Sheet {
    return {
        position: new Vector3(),
        quaternion: new Quaternion().setFromEuler(
            new Euler(
                between(random, -1, 1),
                between(random, -Math.PI, Math.PI),
                between(random, -Math.PI, Math.PI),
            ),
        ),
        vel: new Vector3(between(random, 0.1, 0.3), between(random, -0.05, 0.05), 0),
        spin: new Vector3(
            between(random, -0.3, 0.3),
            between(random, -0.4, 0.4),
            between(random, -0.2, 0.2),
        ),
        baseSpin: new Vector3(
            between(random, -0.15, 0.15),
            between(random, -0.25, 0.25),
            between(random, -0.1, 0.1),
        ),
        bend: 0,
        twist: 0,
        flutter: 0.03,
        phase: between(random, 0, Math.PI * 2),
        lifting: false,
        kind,
        morph: null,
        morphT: 0,
        base: sheetGrid(),
    };
}

const clearOf = (pos: Vector3, obstacles: readonly Obstacle[], margin: number) =>
    obstacles.every((o) => pos.distanceTo(o.c) > o.r + margin);

/**
 * The opening swarm. The first sheet is the golden post and the second the
 * plate; both start near the middle of the depth band, where they can be seen
 * and caught. Each sheet gets a spot clear of the others and of the solids if
 * one can be found in a few tries; `separate` settles whatever is left.
 */
export function placeSheets(
    count: number,
    view: View,
    obstacles: readonly Obstacle[],
    random: () => number = Math.random,
): Sheet[] {
    const sheets: Sheet[] = [];
    for (let i = 0; i < count; i++) {
        const kind: SheetKind = i === 0 ? 'gold' : i === 1 ? 'plate' : null;
        const s = createSheet(kind, random);
        for (let tries = 0; tries < 60; tries++) {
            const z = kind ? between(random, -1.5, 1.5) : between(random, MID_Z[0], MID_Z[1]);
            const half = view.halfExtent(z);
            const spread = kind ? 0.75 : 1;
            s.position.set(
                between(random, -half.x, half.x) * spread,
                between(random, FLOOR + 0.2, view.cameraY + half.y * spread),
                z,
            );
            if (
                sheets.every((q) => q.position.distanceTo(s.position) >= SOFT_DIST) &&
                clearOf(s.position, obstacles, SOFT_DIST)
            )
                break;
        }
        sheets.push(s);
    }
    return sheets;
}

/**
 * The cursor as moving air. `origin` and `direction` are the ray under the
 * pointer; `pointerVel` its speed in screen units per second. The speed is
 * capped so a flick across the screen stirs the sheets rather than flinging
 * them out of the frame.
 */
export function makeGust(
    origin: Vector3,
    direction: Vector3,
    pointerVel: { x: number; y: number },
    cap: number,
    view: View,
): Gust {
    const speed = Math.hypot(pointerVel.x, pointerVel.y);
    const strength = Math.min(cap, speed);
    const scale = strength / Math.max(1e-3, speed);
    const pt = new Vector3();
    return {
        strength,
        at: (z) => pt.copy(origin).addScaledVector(direction, (z - origin.z) / direction.z),
        vx: (z) => pointerVel.x * scale * view.halfExtent(z).x,
        vy: (z) => pointerVel.y * scale * view.halfExtent(z).y,
    };
}

/** The room's own slow draught, different for every sheet. */
export function ambientWind(out: Vector3, t: number, phase: number): Vector3 {
    return out.set(
        0.22 + 0.12 * Math.sin(t * 0.21 + phase),
        0.05 + 0.1 * Math.cos(t * 0.17 + phase * 1.7),
        0.05 * Math.sin(t * 0.13 + phase),
    );
}

const wind = new Vector3();
const rel = new Vector3();
const normal = new Vector3();
const tmpV = new Vector3();
const tmpQ = new Quaternion();
const tmpE = new Euler();

/** One step of flight for one sheet: air, drag, spin and the shape the air gives it. */
export function flySheet(
    p: Sheet,
    dt: number,
    t: number,
    gust: Gust | null,
    ambient: Vector3,
    view: View,
    random: () => number = Math.random,
): void {
    const g = p.position;
    wind.copy(ambient);
    if (gust && gust.strength > 0.01) {
        const c = gust.at(g.z);
        const half = view.halfExtent(g.z);
        const dx = g.x - c.x;
        const dy = g.y - c.y;
        const R = 0.28 * half.y + 0.6;
        const d = Math.hypot(dx, dy);
        if (d < R) {
            const u = 1 - d / R;
            const f = u * u * (3 - 2 * u);
            wind.x += gust.vx(g.z) * 0.55 * f + (dx / (d || 1)) * 0.25 * f * gust.strength;
            wind.y += gust.vy(g.z) * 0.55 * f + (dy / (d || 1)) * 0.25 * f * gust.strength;
            wind.z += 0.3 * f * gust.strength;
            p.spin.x += between(random, -1, 1) * f * gust.strength * 5.6 * dt;
            p.spin.y += between(random, -1, 1) * f * gust.strength * 7.2 * dt;
        }
    }
    normal.set(0, 0, 1).applyQuaternion(p.quaternion);
    rel.copy(wind).sub(p.vel);
    // Face-on to the air, a sheet is a sail; edge-on, a blade.
    const facing = Math.abs(normal.dot(tmpV.copy(rel).normalize()));
    p.vel.addScaledVector(rel, Math.min(1, (0.9 + 1.6 * facing) * dt));
    p.vel.y -= 0.03 * dt;
    if (p.vel.length() > MAX_SPEED) p.vel.setLength(MAX_SPEED);
    g.addScaledVector(p.vel, dt);

    p.spin.lerp(p.baseSpin, Math.min(1, dt * 0.8));
    p.quaternion.multiply(tmpQ.setFromEuler(tmpE.set(p.spin.x * dt, p.spin.y * dt, p.spin.z * dt)));

    const press = MathUtils.clamp(rel.dot(normal) * 0.9, -0.9, 0.9);
    p.bend += (0.18 * Math.sin(t * 0.6 + p.phase) + press - p.bend) * Math.min(1, dt * 2.5);
    p.twist += (0.35 * Math.sin(t * 0.37 + p.phase * 2) - p.twist) * Math.min(1, dt * 1.5);
    p.flutter += (0.025 + Math.min(0.08, rel.length() * 0.06) - p.flutter) * Math.min(1, dt * 2);
}

/**
 * Leaving the frame: right or top re-enters from the left, left re-enters from
 * the right. The margin exceeds HARD_DIST, so a sheet coming back in never
 * shoves a visible one. The depth band is a wall the sheet bounces off.
 */
export function wrapSheet(p: Sheet, view: View, random: () => number = Math.random): void {
    const g = p.position;
    const half = view.halfExtent(g.z);
    const mx = half.x + 2.4;
    const top = view.cameraY + half.y + 2.4;
    if (g.x > mx || g.y > top) {
        g.x = -mx;
        g.y = between(random, FLOOR + 0.5, view.cameraY + half.y);
    } else if (g.x < -mx) {
        g.x = mx;
        g.y = between(random, FLOOR + 0.5, view.cameraY + half.y);
    }
    if (g.z > MID_Z[1]) {
        g.z = MID_Z[1];
        p.vel.z = -Math.abs(p.vel.z);
    }
    if (g.z < MID_Z[0]) {
        g.z = MID_Z[0];
        p.vel.z = Math.abs(p.vel.z);
    }
}

const sep = new Vector3();
const relV = new Vector3();

/**
 * Keep the sheets apart from each other and out of the solids.
 *
 * Two layers. Forces first: inside SOFT_DIST two sheets push apart and stop
 * closing in, and a sheet near the table feels a cushion of air — so in the
 * ordinary course of things nothing ever reaches a hard limit. Then the hard
 * limits, which move positions: no two centres closer than HARD_DIST, nothing
 * below FLOOR, nothing inside an obstacle's sphere grown by BOUND_R.
 *
 * The hard limits are repeated until a pass changes nothing. Pushing one
 * sheet out of a neighbour can push it into another, or into the book; one
 * pass, as the prototype had, leaves that second contact open until the next
 * frame, and the invariant is per frame.
 *
 * A lifting sheet is the exception. It starts inside the book's keep-out zone
 * and is eased out by forces only — the air under it and a push away from
 * each solid — until it has cleared all of them. In a pair it does not move:
 * the other sheet takes the whole correction, so the torn sheet is never
 * snapped by anything.
 */
export function separate(
    sheets: readonly Sheet[],
    dt: number,
    obstacles: readonly Obstacle[],
    random: () => number = Math.random,
): void {
    for (let i = 0; i < sheets.length; i++) {
        for (let j = i + 1; j < sheets.length; j++) {
            const a = sheets[i];
            const b = sheets[j];
            sep.subVectors(b.position, a.position);
            let d = sep.length();
            if (d >= SOFT_DIST) continue;
            if (d < 1e-4) {
                sep.set(between(random, -1, 1), between(random, -1, 1), between(random, -1, 1));
                d = sep.length();
            }
            sep.divideScalar(d);
            const u = Math.min(1, 1 - (d - HARD_DIST) / (SOFT_DIST - HARD_DIST));
            a.vel.addScaledVector(sep, -u * 0.9 * dt);
            b.vel.addScaledVector(sep, u * 0.9 * dt);
            const closing = relV.subVectors(b.vel, a.vel).dot(sep);
            if (closing < 0) {
                a.vel.addScaledVector(sep, closing * 0.5 * u);
                b.vel.addScaledVector(sep, -closing * 0.5 * u);
            }
        }
    }

    for (const p of sheets) {
        const g = p.position;
        if (p.lifting) {
            p.vel.y += 0.7 * dt; // the air under a lifting sheet
            let clear = g.y >= FLOOR;
            for (const o of obstacles) {
                sep.subVectors(g, o.c);
                const d = sep.length();
                const min = o.r + BOUND_R;
                if (d < min) {
                    p.vel.addScaledVector(sep, ((min - d) / (d || 1)) * 0.8 * dt);
                    clear = false;
                }
            }
            if (clear) p.lifting = false;
            continue;
        }
        if (g.y < FLOOR + 0.6) p.vel.y += (FLOOR + 0.6 - g.y) * 1.5 * dt;
    }

    for (let pass = 0; pass < MAX_PASSES; pass++) {
        let moved = false;
        for (let i = 0; i < sheets.length; i++) {
            for (let j = i + 1; j < sheets.length; j++) {
                const a = sheets[i];
                const b = sheets[j];
                sep.subVectors(b.position, a.position);
                let d = sep.length();
                if (d >= HARD_DIST - SLACK) continue;
                if (d < 1e-4) {
                    sep.set(between(random, -1, 1), between(random, -1, 1), between(random, -1, 1));
                    d = 0;
                }
                sep.normalize();
                let wa = a.lifting ? 0 : 1;
                let wb = b.lifting ? 0 : 1;
                if (wa + wb === 0) wa = wb = 1;
                const fix = HARD_DIST - d;
                a.position.addScaledVector(sep, (-fix * wa) / (wa + wb));
                b.position.addScaledVector(sep, (fix * wb) / (wa + wb));
                moved = true;
            }
        }
        for (const p of sheets) {
            if (p.lifting) continue;
            const g = p.position;
            if (g.y < FLOOR) {
                g.y = FLOOR;
                if (p.vel.y < 0) p.vel.y = 0;
                moved = true;
            }
            for (const o of obstacles) {
                sep.subVectors(g, o.c);
                const d = sep.length();
                const min = o.r + BOUND_R;
                if (d < min - SLACK) {
                    if (d < 1e-6) sep.set(0, 1, 0);
                    g.addScaledVector(sep.normalize(), min - d);
                    moved = true;
                }
            }
        }
        if (!moved) break;
    }
}

const ambient = new Vector3();

/** One frame for the whole swarm: every sheet flies, wraps, and then all are separated. */
export function stepSwarm(
    sheets: readonly Sheet[],
    dt: number,
    t: number,
    gust: Gust | null,
    view: View,
    obstacles: readonly Obstacle[],
    random: () => number = Math.random,
): void {
    for (const p of sheets) {
        flySheet(p, dt, t, gust, ambientWind(ambient, t, p.phase), view, random);
        if (p.morphT > 0) p.morphT = Math.max(0, p.morphT - dt / MORPH_SECONDS);
        wrapSheet(p, view, random);
    }
    separate(sheets, dt, obstacles, random);
}

/**
 * The sheet's shape this frame, written into `pos` (a copy of its grid).
 *
 * Out of the plane only: a curl along the width, a twist across the diagonal,
 * and a ripple travelling from one edge, stronger toward the free side. A
 * torn sheet starts from the turning sheet's shape and eases out of it — x
 * and y back onto the grid, z toward the free-flight shape — so it never
 * stretches and never snaps.
 */
export function deform(p: Sheet, pos: Float32Array, t: number): void {
    const b = p.base;
    const m = p.morph;
    const k = m && p.morphT > 0 ? p.morphT * p.morphT * (3 - 2 * p.morphT) : 0;
    for (let i = 0; i < pos.length; i += 3) {
        const u = b[i] / (PAGE_W / 2);
        const v = b[i + 1] / (PAGE_H / 2);
        const z =
            p.bend * 0.32 * u * u +
            p.twist * 0.12 * u * v +
            p.flutter *
                Math.sin(v * 2.4 + u * 0.8 + t * 3.1 + p.phase) *
                (0.35 + 0.65 * (u * 0.5 + 0.5));
        if (m && k > 0) {
            pos[i] = b[i] + (m[i] - b[i]) * k;
            pos[i + 1] = b[i + 1] + (m[i + 1] - b[i + 1]) * k;
            pos[i + 2] = z + (m[i + 2] - z) * k;
        } else {
            pos[i] = b[i];
            pos[i + 1] = b[i + 1];
            pos[i + 2] = z;
        }
    }
    if (m && p.morphT <= 0) p.morph = null;
}
