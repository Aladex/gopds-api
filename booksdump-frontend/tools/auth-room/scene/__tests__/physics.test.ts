import { Vector3 } from 'three';

import {
    BOUND_R,
    FLOOR,
    HARD_DIST,
    PAGE_H,
    PAGE_W,
    halfExtentAt,
    sceneLayout,
    type Layout,
} from '../layout';
import {
    createSheet,
    deform,
    makeGust,
    placeSheets,
    separate,
    stepSwarm,
    type Sheet,
    type View,
} from '../physics';

/*
 * The sheets are the scene's only moving solids, and two promises keep them
 * looking like paper rather than like a texture glitch: they never pass
 * through each other, and they never pass through the table or anything on
 * it. Both are checked here against the same numbers the renderer uses.
 */

/** A deterministic random source, so a failure can be replayed. */
function seeded(seed: number): () => number {
    let s = seed >>> 0;
    return () => {
        s = (s + 0x6d2b79f5) >>> 0;
        let t = s;
        t = Math.imul(t ^ (t >>> 15), t | 1);
        t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
}

function viewFor(layout: Layout, aspect: number): View {
    return { halfExtent: (z) => halfExtentAt(z, layout.fov, aspect), cameraY: 1.3 };
}

function minPairDistance(sheets: Sheet[]): number {
    let min = Infinity;
    for (let i = 0; i < sheets.length; i++)
        for (let j = i + 1; j < sheets.length; j++)
            min = Math.min(min, sheets[i].position.distanceTo(sheets[j].position));
    return min;
}

const EPS = 1e-6;

/** Every non-lifting sheet is above the floor and outside every obstacle. */
function expectClearOfSolids(sheets: Sheet[], layout: Layout) {
    for (const s of sheets) {
        if (s.lifting) continue;
        expect(s.position.y).toBeGreaterThanOrEqual(FLOOR - EPS);
        for (const o of layout.obstacles)
            expect(s.position.distanceTo(o.c)).toBeGreaterThanOrEqual(o.r + BOUND_R - EPS);
    }
}

/** A cluster crowded on purpose: every sheet within a couple of units of every other. */
function crowd(n: number, random: () => number, centre: Vector3): Sheet[] {
    return Array.from({ length: n }, () => {
        const s = createSheet(null, random);
        s.position
            .copy(centre)
            .add(new Vector3(random() - 0.5, random() - 0.5, random() - 0.5).multiplyScalar(3));
        return s;
    });
}

describe('separate: sheets never cut through each other', () => {
    it.each([1, 2, 3, 4, 5])('pulls a crowded cluster apart to HARD_DIST (seed %i)', (seed) => {
        const random = seeded(seed);
        const layout = sceneLayout(false);
        const sheets = crowd(16, random, new Vector3(0, 2, -3));

        separate(sheets, 1 / 60, layout.obstacles, random);

        expect(minPairDistance(sheets)).toBeGreaterThanOrEqual(HARD_DIST - EPS);
    });

    it('holds sheets on top of each other apart, not only nearly so', () => {
        const random = seeded(7);
        const layout = sceneLayout(false);
        const a = createSheet(null, random);
        const b = createSheet(null, random);
        a.position.set(0, 2, 0);
        b.position.set(0, 2, 0);

        separate([a, b], 1 / 60, layout.obstacles, random);

        expect(a.position.distanceTo(b.position)).toBeGreaterThanOrEqual(HARD_DIST - EPS);
    });
});

describe('separate: sheets never cut through the table, the book or the still life', () => {
    it.each([false, true])('pushes every sheet out of every solid (phone: %s)', (phone) => {
        const random = seeded(11);
        const layout = sceneLayout(phone);
        // One sheet sunk into each obstacle, and one under the table.
        const sheets = layout.obstacles.map((o) => {
            const s = createSheet(null, random);
            s.position.copy(o.c);
            return s;
        });
        const low = createSheet(null, random);
        low.position.set(8, FLOOR - 3, -6);
        sheets.push(low);

        separate(sheets, 1 / 60, layout.obstacles, random);

        expectClearOfSolids(sheets, layout);
        expect(minPairDistance(sheets)).toBeGreaterThanOrEqual(HARD_DIST - EPS);
    });

    it('never snaps a lifting sheet: it is eased out by its velocity alone', () => {
        const random = seeded(13);
        const layout = sceneLayout(false);
        const torn = createSheet(null, random);
        torn.position.copy(layout.bookCenter).add(new Vector3(0.2, 0.3, 0));
        torn.lifting = true;
        torn.vel.set(0.05, 0.15, 0.02);
        // A neighbour drifting into it, to tempt the pair limit as well.
        const other = createSheet(null, random);
        other.position.copy(torn.position).add(new Vector3(1.5, 0.8, 0));
        other.lifting = false;

        const before = torn.position.clone();
        const velBefore = torn.vel.clone();
        separate([torn, other], 1 / 60, layout.obstacles, random);

        expect(torn.position.equals(before)).toBe(true);
        // The forces did act: inside the keep-out zone the air and the
        // obstacle lift it out.
        expect(torn.vel.y).toBeGreaterThan(velBefore.y);
        // The neighbour took the whole correction instead.
        expect(other.position.distanceTo(torn.position)).toBeGreaterThanOrEqual(HARD_DIST - EPS);
    });

    it('lets go of a lifting sheet only once it is clear of every solid, then holds it there', () => {
        const random = seeded(17);
        const layout = sceneLayout(false);
        const view = viewFor(layout, 16 / 10);
        const torn = createSheet(null, random);
        torn.position.copy(layout.bookCenter).add(new Vector3(0, 0.3, 0));
        torn.lifting = true;
        torn.vel.set(0.05, 0.15, 0.02);

        let released = -1;
        for (let frame = 0; frame < 60 * 20; frame++) {
            const prev = torn.position.clone();
            const wasLifting = torn.lifting;
            stepSwarm([torn], 1 / 60, frame / 60, null, view, layout.obstacles, random);
            if (wasLifting) {
                // Never teleported while lifting: one frame's travel at most
                // the speed cap allows.
                expect(torn.position.distanceTo(prev)).toBeLessThanOrEqual(1.8 / 60 + EPS);
            }
            if (wasLifting && !torn.lifting) released = frame;
            if (!torn.lifting) expectClearOfSolids([torn], layout);
        }
        expect(released).toBeGreaterThan(0);
    });
});

describe('the swarm over time', () => {
    it.each([
        [false, 16 / 10, 21],
        [false, 21 / 9, 22],
        [true, 390 / 844, 23],
    ])(
        'keeps both promises on every frame of a windy minute (phone: %s, aspect %f)',
        (phone, aspect, seed) => {
            const random = seeded(seed);
            const layout = sceneLayout(phone);
            const view = viewFor(layout, aspect);
            const sheets = placeSheets(layout.sheetCount, view, layout.obstacles, random);
            separate(sheets, 1 / 60, layout.obstacles, random);

            let worst = Infinity;
            for (let frame = 0; frame < 60 * 60; frame++) {
                const t = frame / 60;
                // The reader's hand sweeps through every other second.
                const ndc = { x: Math.sin(t * 1.3), y: Math.cos(t * 0.7) * 0.8 };
                const gust =
                    Math.floor(t) % 2 === 0
                        ? makeGust(
                              new Vector3(0, 1.3, 10),
                              new Vector3(ndc.x * 0.6, ndc.y * 0.35, -1).normalize(),
                              { x: 2.5 * Math.cos(t * 1.3), y: -0.6 },
                              1.6,
                              view,
                          )
                        : null;
                stepSwarm(sheets, 1 / 60, t, gust, view, layout.obstacles, random);
                worst = Math.min(worst, minPairDistance(sheets));
                expectClearOfSolids(sheets, layout);
            }
            expect(worst).toBeGreaterThanOrEqual(HARD_DIST - EPS);
        },
    );
});

describe('deform', () => {
    it('stays inside the bounding sphere however hard the air presses', () => {
        const random = seeded(29);
        const layout = sceneLayout(false);
        const view = viewFor(layout, 16 / 10);
        const s = createSheet(null, random);
        s.position.set(0, 1, 0);
        const pos = s.base.slice();

        // A violent hand right on the sheet for ten seconds, deforming every
        // frame: the shape numbers the air drives must stay in the range
        // BOUND_R was sized for.
        let maxDepth = 0;
        let maxBend = 0;
        let maxTwist = 0;
        let maxFlutter = 0;
        // How far any vertex moved in the plane: a count kept in plain
        // numbers, not an assertion per vertex per frame — two hundred
        // thousand of those made the test slow enough to time out on a
        // loaded machine.
        let maxInPlane = 0;
        for (let frame = 0; frame < 600; frame++) {
            const t = frame / 60;
            const gust = makeGust(
                new Vector3(0, 1.3, 10),
                s.position
                    .clone()
                    .sub(new Vector3(0, 1.3, 10))
                    .normalize(),
                { x: 40 * Math.sin(t * 9), y: 40 * Math.cos(t * 7) },
                1.6,
                view,
            );
            stepSwarm([s], 1 / 60, t, gust, view, [], random);
            maxBend = Math.max(maxBend, Math.abs(s.bend));
            maxTwist = Math.max(maxTwist, Math.abs(s.twist));
            maxFlutter = Math.max(maxFlutter, Math.abs(s.flutter));
            deform(s, pos, t);
            for (let i = 0; i < pos.length; i += 3) {
                maxInPlane = Math.max(
                    maxInPlane,
                    Math.abs(pos[i] - s.base[i]),
                    Math.abs(pos[i + 1] - s.base[i + 1]),
                );
                maxDepth = Math.max(maxDepth, Math.abs(pos[i + 2]));
            }
        }
        // In the plane the grid never moves: paper does not stretch.
        expect(maxInPlane).toBe(0);
        expect(maxDepth).toBeLessThanOrEqual(0.5);
        expect(Math.hypot(PAGE_W / 2, PAGE_H / 2, maxDepth)).toBeLessThanOrEqual(BOUND_R);
        // The ranges the shape is driven within: a curl of at most the
        // clamped pressure plus its idle sway, the idle twist, and a ripple of
        // at most its base plus its cap.
        expect(maxBend).toBeLessThanOrEqual(0.9 + 0.18);
        expect(maxTwist).toBeLessThanOrEqual(0.35);
        expect(maxFlutter).toBeLessThanOrEqual(0.025 + 0.08);
    });

    it('stays inside the bounding sphere at the extremes of every range at once', () => {
        const s = createSheet(null, seeded(31));
        s.bend = 0.9 + 0.18;
        s.twist = 0.35;
        s.flutter = 0.025 + 0.08;
        const pos = s.base.slice();
        let maxDepth = 0;
        for (let t = 0; t < 6.3; t += 0.05) {
            deform(s, pos, t);
            for (let i = 2; i < pos.length; i += 3) maxDepth = Math.max(maxDepth, Math.abs(pos[i]));
        }
        expect(maxDepth).toBeLessThanOrEqual(0.5);
    });
});
