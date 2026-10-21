/**
 * Below this share of the height, a height-only change is the URL bar sliding
 * away or back; the canvas is stretched by that much rather than every render
 * target of the post-processing being reallocated for it.
 */
const SLIDE_SHARE = 0.15;

/** Whether a viewport change is worth resizing the renderer and its targets for. */
export function needsResize(
    was: { width: number; height: number },
    now: { width: number; height: number },
): boolean {
    if (now.width !== was.width) return true;
    return Math.abs(now.height - was.height) > was.height * SLIDE_SHARE;
}
