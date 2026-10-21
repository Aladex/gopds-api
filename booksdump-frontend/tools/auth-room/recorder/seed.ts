/**
 * Seeded randomness for a recording, installed before the scene is imported:
 * the same seed makes the same video, frame for frame, so a re-render only
 * changes what was meant to change. mulberry32 — small, fast, good enough for
 * scattering paper.
 */
const seed = Number(new URLSearchParams(location.search).get('seed') ?? 1) >>> 0;
let s = seed;
Math.random = () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
