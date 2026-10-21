/**
 * How much of the room a device can afford, decided from how long its frames
 * actually take.
 *
 * The room is decoration behind a form. On a fast machine it is drawn in full;
 * on a slow one it has to give way, because a scene that takes the main thread
 * for a second per frame makes every click on the form wait behind it — the
 * card of the next screen painted three seconds after the click. So the scene
 * measures itself and steps down a ladder until it fits: first the expensive
 * post-processing, then pixels, then particles and sheets, and in the end it
 * stops moving altogether and keeps its last frame. It steps back up only
 * after a stretch of clear headroom, and never back to a level that has
 * already failed twice, so it does not oscillate.
 *
 * Pure: the decisions are made from numbers, and tested from numbers.
 */

export interface QualityLevel {
    name: string;
    /** Ambient occlusion and depth of field (desktop composition only). */
    heavyPost: boolean;
    /** Upper bound on the renderer's pixel ratio, or null for the composition's own cap. */
    pixelRatio: number | null;
    bloom: boolean;
    /** Share of dust, far sheets and free sheets kept. */
    share: number;
    /**
     * Render the shadow maps every this many frames. Both lights cast soft
     * (VSM) shadows, re-rendered and blurred every frame by default — one of
     * the costliest things in the frame, and on a phone the costliest of all.
     */
    shadowEvery: number;
    /** False for the last resort: no loop, the last frame stays on screen. */
    moving: boolean;
}

export const LEVELS: readonly QualityLevel[] = [
    {
        name: 'full',
        heavyPost: true,
        pixelRatio: null,
        bloom: true,
        share: 1,
        shadowEvery: 1,
        moving: true,
    },
    {
        name: 'no-dof',
        heavyPost: false,
        pixelRatio: null,
        bloom: true,
        share: 1,
        shadowEvery: 1,
        moving: true,
    },
    {
        name: 'ratio-1',
        heavyPost: false,
        pixelRatio: 1,
        bloom: true,
        share: 1,
        shadowEvery: 2,
        moving: true,
    },
    {
        name: 'low',
        heavyPost: false,
        pixelRatio: 0.75,
        bloom: false,
        share: 0.5,
        shadowEvery: 3,
        moving: true,
    },
    {
        name: 'still',
        heavyPost: false,
        pixelRatio: 0.75,
        bloom: false,
        share: 0.5,
        shadowEvery: 3,
        moving: false,
    },
];
export const STILL = LEVELS.length - 1;
export const LOWEST_MOVING = STILL - 1;

/** A phone starts without ambient occlusion and depth of field, as its composition always has. */
export const startLevel = (phone: boolean): number => (phone ? 1 : 0);

/**
 * The budget, in milliseconds.
 *
 * Down when frames average more than 30 ms (under ~33 a second), or when the
 * scene's own script takes more than 10 ms of a frame — at a steady sixty a
 * second that still leaves the interface less than half the main thread.
 * Up only below 20 ms and 6 ms: the gap between the two is the dead band a
 * level can sit in without being moved either way.
 */
export const BUDGET = {
    downIntervalMs: 30,
    downStepMs: 10,
    upIntervalMs: 20,
    upStepMs: 6,
    /** A frame this long is a device that cannot keep up at all. */
    severeMs: 120,
    /**
     * A frame this long will not be rescued by any moving level: the
     * cheapest is a few times cheaper than the full room, not a hundred.
     */
    catastrophicMs: 2000,
    /** Frames needed at a level before an ordinary step down. */
    minSamples: 10,
    /** Time at a level with headroom before trying the one above. */
    settleUpMs: 4000,
    /** The same while probing a new device from below: well inside the fade-in. */
    probeUpMs: 400,
    /** Times a level may fail before it is not tried again. */
    maxFailures: 2,
};

const EMA = 0.15;

export interface Governor {
    readonly level: number;
    /**
     * One drawn frame: the interval since the previous one and the script
     * time spent drawing it. Returns the new level when it changes.
     */
    sample(intervalMs: number, stepMs: number): number | null;
}

/**
 * `start` is where the room begins: the composition's best level, or the one
 * the last room in this tab settled on. `top` is the best it may ever reach —
 * the composition's own — so a room that starts lower can still climb back
 * when the device turns out to have room.
 *
 * With `probe`, for a device not seen before in this tab, the room begins two
 * levels below `start` and climbs back to it after short stretches of
 * headroom. A fast machine is there within a second, while the canvas is still
 * fading in; a slow one never pays for frames of the full room — on a software
 * renderer those take seconds each, and every one of them is a second the form
 * cannot answer a click.
 */
export function createGovernor(
    start: number,
    { top = 0, probe = false }: { top?: number; probe?: boolean } = {},
): Governor {
    let level = probe ? Math.min(start + 2, Math.max(start, LOWEST_MOVING)) : start;
    let probing = probe && level > start;
    let samples = 0;
    let time = 0;
    let interval = 0;
    let step = 0;
    let severeRun = 0;
    const failures = LEVELS.map(() => 0);

    const move = (to: number) => {
        if (to > level) probing = false;
        level = to;
        samples = 0;
        time = 0;
        severeRun = 0;
        return to;
    };

    return {
        get level() {
            return level;
        },
        sample(intervalMs, stepMs) {
            if (level === STILL) return null;
            samples++;
            // The first frame at a level pays for its shader compiles, and
            // says nothing about the level; it does not count, nor does its
            // time.
            if (samples === 1) return null;
            time += intervalMs;
            if (samples === 2) {
                interval = intervalMs;
                step = stepMs;
            } else {
                interval += (intervalMs - interval) * EMA;
                step += (stepMs - step) * EMA;
            }

            if (intervalMs > BUDGET.catastrophicMs) {
                failures[level]++;
                return move(STILL);
            }
            // A device that takes a large fraction of a second per frame is
            // not going to be rescued by a small step: move two at once, but
            // only ever into the still frame from the lowest moving level —
            // where one such frame is enough, there being nowhere else to go.
            severeRun = intervalMs > BUDGET.severeMs ? severeRun + 1 : 0;
            if (level === LOWEST_MOVING && severeRun > 0) {
                failures[level]++;
                return move(STILL);
            }
            if (intervalMs > BUDGET.severeMs * 4 || severeRun >= 2) {
                failures[level]++;
                const to = level < LOWEST_MOVING ? Math.min(level + 2, LOWEST_MOVING) : STILL;
                return move(to);
            }

            if (
                samples > BUDGET.minSamples &&
                (interval > BUDGET.downIntervalMs || step > BUDGET.downStepMs)
            ) {
                failures[level]++;
                return move(level + 1);
            }

            if (
                level > top &&
                time >= (probing ? BUDGET.probeUpMs : BUDGET.settleUpMs) &&
                interval < BUDGET.upIntervalMs &&
                step < BUDGET.upStepMs &&
                failures[level - 1] < BUDGET.maxFailures
            ) {
                const to = move(level - 1);
                if (to <= start) probing = false;
                return to;
            }
            return null;
        },
    };
}
