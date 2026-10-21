import {
    LEVELS,
    LOWEST_MOVING,
    STILL,
    createGovernor,
    startLevel,
} from '@/features/auth/scene/quality';

/*
 * The quality ladder decides how much of the room a device can afford from
 * how long its frames take. It must leave a fast machine alone, find the level
 * a slow one can hold within a few seconds, and not oscillate between two.
 */

/**
 * Feed `seconds` worth of frames; return every level change. A frame's cost
 * may depend on the level it is drawn at.
 */
function run(
    governor: ReturnType<typeof createGovernor>,
    seconds: number,
    interval: number | ((level: number) => number),
    step = 2,
): number[] {
    const changes: number[] = [];
    const cost = typeof interval === 'number' ? () => interval : interval;
    for (let t = 0; t < seconds * 1000;) {
        const ms = cost(governor.level);
        const next = governor.sample(ms, step);
        if (next !== null) changes.push(next);
        t += ms;
    }
    return changes;
}

describe('the ladder', () => {
    it('runs from the full room down to a still frame, the still frame last', () => {
        expect(LEVELS[0].moving).toBe(true);
        expect(LEVELS[STILL].moving).toBe(false);
        expect(STILL).toBe(LEVELS.length - 1);
        expect(LOWEST_MOVING).toBe(STILL - 1);
        // Each step down costs less: never more pixels, post or particles.
        for (let i = 1; i < STILL; i++) {
            const [a, b] = [LEVELS[i - 1], LEVELS[i]];
            expect(Number(b.heavyPost)).toBeLessThanOrEqual(Number(a.heavyPost));
            expect(b.pixelRatio ?? 9).toBeLessThanOrEqual(a.pixelRatio ?? 9);
            expect(b.share).toBeLessThanOrEqual(a.share);
            expect(b.shadowEvery).toBeGreaterThanOrEqual(a.shadowEvery);
        }
    });

    it('starts a phone lower than a desktop, as the phone composition always did', () => {
        expect(startLevel(false)).toBe(0);
        expect(startLevel(true)).toBeGreaterThan(0);
        expect(LEVELS[startLevel(true)].heavyPost).toBe(false);
    });
});

describe('createGovernor', () => {
    it('leaves a fast machine at full, a minute at 60 frames a second', () => {
        const g = createGovernor(0);
        expect(run(g, 60, 1000 / 60, 3)).toEqual([]);
        expect(g.level).toBe(0);
    });

    it('steps down one level when frames run long, after enough of them to be sure', () => {
        const g = createGovernor(0);
        const changes = run(g, 0.3, 40);
        expect(changes).toEqual([]);

        expect(run(g, 1, 40)[0]).toBe(1);
    });

    it('steps down when the main thread is busy, even at a steady frame rate', () => {
        // Sixty frames a second, but fourteen of every sixteen milliseconds
        // spent in the scene: the interface has no room left.
        const g = createGovernor(0);
        expect(run(g, 2, 1000 / 60, 14)[0]).toBe(1);
    });

    it('holds a level in the dead band between stepping down and stepping up', () => {
        const g = createGovernor(1);
        expect(run(g, 30, 25, 4)).toEqual([]);
    });

    it('ignores the first frame after a change: it pays for shader compiles', () => {
        const g = createGovernor(0);
        expect(g.sample(5000, 5)).toBeNull();
        expect(g.level).toBe(0);
    });

    it('drops fast on a device that takes seconds per frame, settling within a few seconds', () => {
        const g = createGovernor(0);
        let elapsed = 0;
        // Each level is cheaper than the last, and none can be held.
        const cost = [3000, 1500, 800, 300];
        while (g.level !== STILL && elapsed < 20000) {
            const interval = cost[g.level];
            g.sample(interval, 5);
            elapsed += interval;
        }
        expect(g.level).toBe(STILL);
        expect(elapsed).toBeLessThan(12000);
    });

    it('steps two levels for a frame of a large fraction of a second, but not into still', () => {
        const g = createGovernor(LOWEST_MOVING - 1);
        g.sample(1000, 5); // warm-up
        g.sample(1000, 5);
        expect(g.level).toBe(LOWEST_MOVING);
    });

    it('goes straight to the still frame for a frame of whole seconds, from any level', () => {
        // A device that takes seconds for a frame of the full room will not
        // hold even the lowest moving level; finding that out a level at a
        // time costs the reader a few more frozen seconds each.
        const g = createGovernor(0);
        g.sample(3000, 5); // warm-up
        g.sample(3000, 5);
        expect(g.level).toBe(STILL);
    });

    it('stops at the lowest moving level on the first frame it cannot afford', () => {
        const g = createGovernor(LOWEST_MOVING);
        g.sample(300, 4); // warm-up
        g.sample(300, 4);
        expect(g.level).toBe(STILL);
    });

    it('keeps the lowest moving level when the device can hold it', () => {
        const g = createGovernor(LOWEST_MOVING);
        expect(run(g, 20, 24, 4)).toEqual([]);
        expect(g.level).toBe(LOWEST_MOVING);
    });

    it('goes still when even the lowest moving level is too much', () => {
        const g = createGovernor(LOWEST_MOVING);
        run(g, 2, 60, 4);
        expect(g.level).toBe(STILL);
    });

    it('stays still: a stopped loop has no frames to judge by', () => {
        const g = createGovernor(STILL);
        expect(run(g, 10, 5, 1)).toEqual([]);
        expect(g.level).toBe(STILL);
    });

    it('steps back up only after a stretch of clear headroom', () => {
        const g = createGovernor(2);
        expect(run(g, 3, 1000 / 60, 3)).toEqual([]);
        expect(run(g, 2, 1000 / 60, 3)).toEqual([1]);
    });

    it('stops trying a level that has failed twice: no endless oscillation', () => {
        const g = createGovernor(0);
        // Full is too much, the next level is easy: down, up, down, up, down —
        // and then no more attempts at full.
        const changes = run(g, 60, (level) => (level === 0 ? 40 : 1000 / 60), 3);
        expect(changes.filter((l) => l === 0)).toHaveLength(1);
        expect(g.level).toBe(1);
    });
});

describe('createGovernor: probing a device it has not seen', () => {
    it('starts below the top and climbs to it within a second on a fast device', () => {
        const g = createGovernor(0, { probe: true });
        expect(g.level).toBeGreaterThan(0);

        let elapsed = 0;
        while (g.level !== 0 && elapsed < 5000) {
            g.sample(1000 / 60, 3);
            elapsed += 1000 / 60;
        }
        // Under the canvas's one-second fade-in: nobody sees the climb.
        expect(g.level).toBe(0);
        expect(elapsed).toBeLessThanOrEqual(1200);
    });

    it('never climbs above the top it was given', () => {
        const g = createGovernor(1, { top: 1, probe: true });
        run(g, 10, 1000 / 60, 3);
        expect(g.level).toBe(1);
    });

    it('settles a device that takes seconds per frame within a few seconds of starting', () => {
        const g = createGovernor(0, { probe: true });
        let elapsed = 0;
        // What a software renderer measured per level: whole seconds even
        // without the heavy post-processing.
        const cost = [7000, 4000, 2500, 300];
        while (g.level !== STILL && elapsed < 30000) {
            const interval = cost[g.level];
            g.sample(interval, 5);
            elapsed += interval;
        }
        expect(g.level).toBe(STILL);
        expect(elapsed).toBeLessThanOrEqual(5000);
    });

    it('stops probing once it has stepped down: the ordinary patience applies again', () => {
        const g = createGovernor(0, { probe: true });
        const start = g.level;
        run(g, 1, 40, 3);
        expect(g.level).toBeGreaterThan(start);
        expect(run(g, 1, 1000 / 60, 3)).toEqual([]);
    });
});

describe('createGovernor: not fooled by a burst', () => {
    it('does not climb on a few quick frames after a slow one: the GPU may just be queueing them', () => {
        const g = createGovernor(0, { probe: true });
        const start = g.level;
        g.sample(1000, 4); // warm-up: a second
        g.sample(17, 3);
        g.sample(17, 3);
        g.sample(17, 3);

        expect(g.level).toBe(start);
    });
});
