/**
 * The Easter eggs' positions the app bundles (src/features/auth/room/
 * markers.json): one entry per composition: desktop, phone, ultrawide. The torn book
 * is the same camera on the same room, so its eggs are where the whole book's
 * are — checked here, not assumed.
 *
 * A run may render only some variants. What it did not render keeps the entry
 * it had: the app needs both compositions, always.
 */

type Point = [number, number];

export interface Markers {
    width: number;
    height: number;
    goldenPost: Point[];
    pupils: Point[];
    mask: { x: number; y: number; w: number; h: number };
}

export const COMPOSITIONS = ['desktop', 'phone', 'ultrawide'] as const;
export type Composition = (typeof COMPOSITIONS)[number];

/** How far, in pixels, two recordings of one composition may disagree. */
const TOLERANCE = 1;

const compositionOf = (variant: string): Composition => {
    const key = variant.replace(/-torn$/, '');
    if (!(COMPOSITIONS as readonly string[]).includes(key)) {
        throw new Error(`unknown variant: ${variant}`);
    }
    return key as Composition;
};

const isTorn = (variant: string) => variant.endsWith('-torn');

const drift = (a: Markers, b: Markers): number => {
    const flat = (m: Markers) => [...m.goldenPost.flat(), ...m.pupils.flat()];
    const fb = flat(b);
    return Math.max(...flat(a).map((v, i) => Math.abs(v - fb[i])));
};

/**
 * The markers file after a run: the compositions this run recorded, the rest
 * as they were.
 *
 * Throws when the two variants recorded in this run disagree, when a variant
 * recorded alone disagrees with the kept entry for its composition (the
 * other variant's video would then be out of step with the eggs: re-render
 * both), or when a composition would end up missing.
 */
export function mergeMarkers(
    existing: Partial<Record<Composition, Markers>>,
    recorded: Record<string, Markers>,
): Record<Composition, Markers> {
    const fresh: Partial<Record<Composition, { variant: string; markers: Markers }[]>> = {};
    for (const [variant, markers] of Object.entries(recorded)) {
        (fresh[compositionOf(variant)] ??= []).push({ variant, markers });
    }
    const out: Partial<Record<Composition, Markers>> = {};
    for (const key of COMPOSITIONS) {
        const runs = fresh[key];
        if (!runs) {
            if (existing[key]) out[key] = existing[key];
            continue;
        }
        // The whole book's recording is the reference; the torn one is held
        // to it.
        const ordered = [...runs].sort(
            (a, b) => Number(isTorn(a.variant)) - Number(isTorn(b.variant)),
        );
        const [first, ...rest] = ordered;
        for (const r of rest) {
            const d = drift(r.markers, first.markers);
            if (d > TOLERANCE) {
                throw new Error(`${r.variant}: Easter eggs moved by ${d}px from ${first.variant}`);
            }
        }
        // Only one of the two variants re-rendered: it must still agree with
        // the recording it did not replace.
        const kept = existing[key];
        if (runs.length === 1 && kept) {
            const d = drift(first.markers, kept);
            if (d > TOLERANCE) {
                throw new Error(
                    `${first.variant}: Easter eggs moved by ${d}px from the kept ${key} entry; ` +
                        `re-render both ${key} variants`,
                );
            }
            // The torn book alone: the kept entry came from the whole one.
            if (isTorn(first.variant)) {
                out[key] = kept;
                continue;
            }
        }
        out[key] = first.markers;
    }
    const missing = COMPOSITIONS.filter((k) => !out[k]);
    if (missing.length) {
        throw new Error(`markers for ${missing.join(', ')} missing: render those variants too`);
    }
    return out as Record<Composition, Markers>;
}
