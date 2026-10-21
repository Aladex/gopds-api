/**
 * object-fit: cover, as arithmetic: how a picture of one size fills a box of
 * another — scaled until it covers both sides, centred, the overflow cut off.
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

export function coverFit(media: Size, box: Size): Cover {
    const scale = Math.max(box.width / media.width, box.height / media.height);
    return {
        scale,
        dx: (box.width - media.width * scale) / 2,
        dy: (box.height - media.height * scale) / 2,
    };
}

/** A point in the picture's pixels, in the box's. */
export const toBox = ([x, y]: Point, c: Cover): Point => [x * c.scale + c.dx, y * c.scale + c.dy];
