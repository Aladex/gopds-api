/**
 * object-fit: cover, as arithmetic: how a picture of one size fills a box of
 * another — scaled until it covers both sides, placed by object-position (the
 * anchor, centred unless said otherwise), the overflow cut off.
 * The reading-room video is shown that way, and the Easter eggs laid over it
 * use the same numbers to land on the things they belong to.
 */

export interface Size {
    width: number;
    height: number;
}

export type Point = [number, number];

export interface Cover {
    scale: number;
    /** Where the picture's top-left corner lands in the box. */
    dx: number;
    dy: number;
}

/**
 * object-position in fractions: 0 puts the picture's left (top) edge on the
 * box's, 1 its right (bottom) edge, 0.5 centres it — the same rule CSS uses
 * for percentages, so the two always agree.
 */
export interface Anchor {
    x: number;
    y: number;
}

export const CENTRED: Anchor = { x: 0.5, y: 0.5 };

export function coverFit(media: Size, box: Size, anchor: Anchor = CENTRED): Cover {
    const scale = Math.max(box.width / media.width, box.height / media.height);
    return {
        scale,
        dx: (box.width - media.width * scale) * anchor.x,
        dy: (box.height - media.height * scale) * anchor.y,
    };
}

/** The anchor as a CSS object-position value. */
export const objectPosition = (a: Anchor): string => `${a.x * 100}% ${a.y * 100}%`;

/** A point in the picture's pixels, in the box's. */
export const toBox = ([x, y]: Point, c: Cover): Point => [x * c.scale + c.dx, y * c.scale + c.dy];
