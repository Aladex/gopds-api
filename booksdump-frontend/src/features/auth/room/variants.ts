import markers from '@/features/auth/room/markers.json';
import type { Point } from '@/features/auth/room/cover';

/**
 * The four recordings of the reading room (see tools/auth-room): the desktop
 * and the phone composition, each with the book whole and with its page torn
 * out for the error page.
 */

export type Variant = 'desktop' | 'desktop-torn' | 'phone' | 'phone-torn';

export const variantFor = (phone: boolean, torn: boolean): Variant =>
    `${phone ? 'phone' : 'desktop'}${torn ? '-torn' : ''}` as Variant;

export interface Sources {
    webm: string;
    mp4: string;
    poster: string;
}

const BASE = '/auth-room/';

export const sourcesFor = (v: Variant): Sources => ({
    webm: `${BASE}${v}.webm`,
    mp4: `${BASE}${v}.mp4`,
    poster: `${BASE}${v}.webp`,
});

/**
 * WebM (VP9) where it plays, MP4 (H.264) elsewhere — Safari and iOS. One src
 * on one element rather than <source> children: swapping a variant is then a
 * single assignment.
 */
export const pickSource = (s: Sources, canPlayType: (type: string) => string): string =>
    canPlayType('video/webm; codecs="vp9"') ? s.webm : s.mp4;

/** Where the Easter eggs are in a recording, in its pixels. */
export interface Markers {
    width: number;
    height: number;
    goldenPost: Point[];
    pupils: Point[];
    mask: { x: number; y: number; w: number; h: number };
}

// Both compositions, always: one missing from the file fails the type check
// here instead of silently losing that screen's Easter eggs.
const byComposition: Record<'desktop' | 'phone', unknown> = markers;

// The torn book is the same camera on the same room: one set per composition.
export const markersFor = (v: Variant): Markers =>
    (byComposition as Record<'desktop' | 'phone', Markers>)[
        v.startsWith('phone') ? 'phone' : 'desktop'
    ];
