import { Vector3 } from 'three';

export { isPhoneViewport } from '../../../src/features/auth/room/viewport';

/**
 * Where everything stands in the reading room, and how much of it there is.
 *
 * Kept apart from the three.js glue so the physics and its tests read the same
 * numbers the renderer draws with: an obstacle that moved in one place and not
 * the other would let sheets fly through the book while every test stayed
 * green.
 *
 * Units are scene units, roughly a sheet's width each. The table is the plane
 * y = TABLE_Y; the camera looks at it from z = 10.
 */

export const TABLE_Y = -2.4;

/** A sheet: A-series proportions, one unit wide. */
export const PAGE_W = 1.0;
export const PAGE_H = 1.414;

/**
 * A bent sheet always fits in a sphere of this radius (half-diagonal 0.87,
 * deformation depth at most 0.5), so two sheets whose centres stay HARD_DIST
 * apart can never cut through each other. SOFT_DIST is where they start to
 * feel each other and get pushed apart by forces before the hard limit has to
 * step in.
 */
export const BOUND_R = 1.0;
export const HARD_DIST = BOUND_R * 2;
export const SOFT_DIST = HARD_DIST + 0.8;
/** The lowest a sheet's centre may go: one bounding radius above the table. */
export const FLOOR = TABLE_Y + BOUND_R;

/** The depth band the free sheets fly in. */
export const MID_Z: readonly [number, number] = [-8, 2.5];

export const CAMERA_Y = 1.3;
export const CAMERA_Z = 10;

/** A solid thing on the table, as one sphere a sheet's centre must stay out of. */
export interface Obstacle {
    c: Vector3;
    r: number;
}

export interface Layout {
    phone: boolean;
    /** The ultrawide composition: the desktop one, with life across the extra width. */
    wide: boolean;
    fov: number;
    /** Where the camera aims; phones aim higher so the desk sits below the card. */
    lookAtY: number;
    pixelRatioCap: number;
    shadowMapSize: number;
    /** Ambient occlusion and depth of field: desktop only. */
    heavyPost: boolean;
    sheetCount: number;
    farCount: number;
    /** The far layer spans x in ±farHalfX, wrapping at the edges. */
    farHalfX: number;
    dustCount: number;
    bookPos: Vector3;
    bookYaw: number;
    bookCenter: Vector3;
    bookR: number;
    maskPos: Vector3;
    emblemYaw: number;
    emblemScale: number;
    /** Scale of the still life (emblem and its stack) relative to the desktop one. */
    still: number;
    stackPos: Vector3;
    obstacles: Obstacle[];
    /** Loose pages on the table: x, z, yaw. The first is the golden post. */
    lyingSpots: [number, number, number][];
}

/**
 * Pages lying on the table out at the sides, seen only in the ultrawide
 * frame: the desktop frame ends at about x = ±5 at the front of the table.
 */
const WIDE_LYING_SPOTS: [number, number, number][] = [
    [-8.6, -0.4, 0.9],
    [8.9, 0.6, 2.0],
    [-12.5, -6.5, 0.4],
    [13.5, -7.5, 1.6],
];

/**
 * The ultrawide frame keeps the desktop camera — the same lens, aim and
 * height — so its middle is the desktop picture exactly and nothing on the
 * desk moves; the width beyond it is filled, not stretched.
 */
export function sceneLayout(phone: boolean, wide = false): Layout {
    if (wide && phone) throw new Error('a phone is never ultrawide');
    const bookPos = phone ? new Vector3(-1.15, TABLE_Y, 2.1) : new Vector3(-3.0, TABLE_Y, 0.9);
    const bookCenter = bookPos.clone().add(new Vector3(0, 0.3, 0));
    const bookR = 1.3;

    // The emblem (radius ~1.05, standing) leans against a stack of two closed
    // books (~2 x 2, 0.56 high). One sphere covers the emblem; one sunk into
    // the table covers the stack.
    const maskPos = phone ? new Vector3(1.25, TABLE_Y, 1.7) : new Vector3(2.75, TABLE_Y, 1.15);
    const emblemYaw = phone ? -0.45 : -0.3;
    const emblemScale = phone ? 0.72 : 1.05;
    const still = emblemScale / 1.05;
    const stackPos = maskPos
        .clone()
        .add(
            new Vector3(Math.sin(emblemYaw), 0, Math.cos(emblemYaw)).multiplyScalar(
                -(0.32 + 1.05) * still,
            ),
        );

    return {
        phone,
        wide,
        fov: phone ? 52 : 35,
        lookAtY: phone ? 0.2 : -0.2,
        pixelRatioCap: phone ? 1.25 : 1.5,
        shadowMapSize: phone ? 1024 : 2048,
        heavyPost: !phone,
        // Free sheets are scattered across the visible width already; the
        // wider frame gets more of them to keep the same density.
        sheetCount: phone ? 10 : wide ? 30 : 16,
        farCount: phone ? 90 : wide ? 380 : 170,
        farHalfX: wide ? 44 : 20,
        dustCount: phone ? 350 : 700,
        bookPos,
        bookYaw: phone ? 0.5 : 0.32,
        bookCenter,
        bookR,
        maskPos,
        emblemYaw,
        emblemScale,
        still,
        stackPos,
        obstacles: [
            { c: bookCenter, r: bookR },
            {
                c: maskPos.clone().add(new Vector3(0.1, 0.8, -0.35).multiplyScalar(still)),
                r: 1.6 * still,
            },
            { c: stackPos.clone().add(new Vector3(0, -0.6 * still, 0)), r: 1.6 * still },
        ],
        lyingSpots: phone
            ? [
                  [0.15, 3.4, 0.3],
                  [1.7, 4.0, 0.6],
                  [-2.4, -1.5, 1.1],
                  [2.0, -3.5, 2.3],
              ]
            : [
                  [-0.75, 1.55, 0.35],
                  [0.6, 2.4, 0.6],
                  [-5.6, -2.5, 1.1],
                  [3.4, -5.5, 2.3],
                  ...(wide ? WIDE_LYING_SPOTS : []),
              ],
    };
}

/** Half the visible width and height of the frame at depth z. */
export function halfExtentAt(z: number, fov: number, aspect: number): { x: number; y: number } {
    const d = CAMERA_Z - z;
    const y = Math.tan(((fov / 2) * Math.PI) / 180) * d;
    return { x: y * aspect, y };
}
