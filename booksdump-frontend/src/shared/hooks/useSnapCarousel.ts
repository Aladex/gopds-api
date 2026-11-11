import React, { useEffect, useLayoutEffect, useRef, useState } from 'react';

/**
 * useSnapCarousel is the mechanics of a snapping track of slides, shared by
 * the donate cards and the scan pipeline's phone chain.
 *
 * The scrolling is the browser's: a snapping track moves under a finger, a
 * trackpad and a sideways wheel with nothing here to arbitrate, and the slide
 * at the alignment point is whichever one is active — nearest the centre for a
 * centred track, nearest the left inset for one that snaps to the start. The
 * rest exists because a track can be scrolled past things that can be pressed.
 * A tap on a slide that is only peeking means that slide, not what is under
 * the finger, and the end of a swipe is not a tap at all, so both are stopped
 * before they reach the slide or its controls.
 *
 * Slides are the track's descendants marked `data-slide`. Nothing here listens
 * to the wheel: a vertical wheel, like a vertical pan, belongs to the page.
 *
 * `onActiveChange` hears only where the track comes to rest, not every slide a
 * swipe passes on its way, so a caller that opens something per slide opens
 * only the one the reader stopped on.
 */

/** How long after the track last moved a tap is taken for the end of a swipe. */
const SCROLL_SETTLE_MS = 160;
const DRAG_SETTLE_MS = 250;

/** Pixels a mouse has to travel before it means a drag. */
const DRAG_THRESHOLD = 6;

export interface SnapCarouselOptions {
    count: number;
    align: 'center' | 'start';
    onActiveChange?: (index: number) => void;
}

export interface SnapCarousel {
    trackRef: React.RefObject<HTMLDivElement | null>;
    active: number;
    dragging: boolean;
    go: (index: number, behavior?: ScrollBehavior) => void;
    trackProps: Pick<
        React.HTMLAttributes<HTMLDivElement>,
        | 'onScroll'
        | 'onClickCapture'
        | 'onKeyDown'
        | 'onPointerDown'
        | 'onPointerMove'
        | 'onPointerUp'
        | 'onPointerCancel'
        | 'onLostPointerCapture'
    >;
}

export function useSnapCarousel({
    count,
    align,
    onActiveChange,
}: SnapCarouselOptions): SnapCarousel {
    const trackRef = useRef<HTMLDivElement>(null);
    const [active, setActive] = useState(0);
    const [dragging, setDragging] = useState(false);
    const settledAt = useRef(0);
    const drag = useRef<{
        x: number;
        y: number;
        left: number;
        pointer: number;
        moving: boolean;
    } | null>(null);
    const restTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
    const reportRef = useRef(onActiveChange);
    useLayoutEffect(() => {
        reportRef.current = onActiveChange;
    });

    useEffect(
        () => () => {
            if (restTimer.current !== null) clearTimeout(restTimer.current);
        },
        [],
    );

    const slides = () =>
        Array.from(trackRef.current?.querySelectorAll<HTMLElement>('[data-slide]') ?? []);

    const nearest = () => {
        const track = trackRef.current;
        const box = track?.getBoundingClientRect();
        if (!track || !box) return 0;
        let distances: number[];
        if (align === 'start') {
            // The last slide cannot reach the snap line: at the far end it is
            // the one in view.
            const end = track.scrollWidth - track.clientWidth;
            if (end > 0 && track.scrollLeft >= end - 1) return slides().length - 1;
            // The snap line is the inset the track keeps on its left.
            const inset = parseFloat(getComputedStyle(track).paddingLeft) || 0;
            const line = box.left + inset;
            distances = slides().map((slide) =>
                Math.abs(slide.getBoundingClientRect().left - line),
            );
        } else {
            const centre = box.left + box.width / 2;
            distances = slides().map((slide) => {
                const rect = slide.getBoundingClientRect();
                return Math.abs(rect.left + rect.width / 2 - centre);
            });
        }
        return distances.indexOf(Math.min(...distances));
    };

    const go = (index: number, behavior: ScrollBehavior = 'smooth') => {
        const all = slides();
        const target = all[index];
        if (!target) return;
        trackRef.current?.scrollTo({
            left: target.offsetLeft - all[0].offsetLeft,
            behavior,
        });
    };

    const onScroll = () => {
        settledAt.current = performance.now() + SCROLL_SETTLE_MS;
        const next = nearest();
        if (reportRef.current) {
            if (restTimer.current !== null) clearTimeout(restTimer.current);
            restTimer.current = setTimeout(() => {
                restTimer.current = null;
                reportRef.current?.(nearest());
            }, SCROLL_SETTLE_MS);
        }
        if (next === active) return;
        // Focus left behind would sit on a slide that has just been hidden.
        const all = slides();
        if (all[active]?.contains(document.activeElement)) {
            all[next]?.querySelector('button')?.focus({ preventScroll: true });
        }
        setActive(next);
    };

    const onClickCapture = (event: React.MouseEvent) => {
        const slide = (event.target as Element).closest<HTMLElement>('[data-slide]');
        if (!slide) return;
        const index = Number(slide.dataset.slide);
        const moving = performance.now() < settledAt.current;
        if (!moving && index === active) return;
        // Prevented for the link, stopped for the slide.
        event.preventDefault();
        event.stopPropagation();
        if (!moving) go(index);
    };

    const onKeyDown = (event: React.KeyboardEvent) => {
        const target = {
            ArrowLeft: active - 1,
            ArrowRight: active + 1,
            Home: 0,
            End: count - 1,
        }[event.key];
        if (target === undefined) return;
        event.preventDefault();
        go(target);
    };

    // A mouse has no swipe of its own, so its drag is laid over the same track.
    const onPointerDown = (event: React.PointerEvent) => {
        if (event.pointerType !== 'mouse' || event.button !== 0) return;
        drag.current = {
            x: event.clientX,
            y: event.clientY,
            left: trackRef.current?.scrollLeft ?? 0,
            pointer: event.pointerId,
            moving: false,
        };
    };

    const onPointerMove = (event: React.PointerEvent) => {
        const state = drag.current;
        const element = trackRef.current;
        if (!state || !element) return;
        const dx = event.clientX - state.x;
        const dy = event.clientY - state.y;
        if (!state.moving) {
            if (Math.abs(dy) > Math.abs(dx) && Math.abs(dy) > DRAG_THRESHOLD) {
                drag.current = null;
                return;
            }
            if (Math.abs(dx) <= DRAG_THRESHOLD) return;
            state.moving = true;
            element.setPointerCapture(state.pointer);
            setDragging(true);
        }
        event.preventDefault();
        element.scrollLeft = state.left - dx;
    };

    const onPointerEnd = () => {
        const state = drag.current;
        drag.current = null;
        if (!state?.moving) return;
        setDragging(false);
        settledAt.current = performance.now() + DRAG_SETTLE_MS;
        go(nearest());
    };

    return {
        trackRef,
        active,
        dragging,
        go,
        trackProps: {
            onScroll,
            onClickCapture,
            onKeyDown,
            onPointerDown,
            onPointerMove,
            onPointerUp: onPointerEnd,
            onPointerCancel: onPointerEnd,
            onLostPointerCapture: onPointerEnd,
        },
    };
}
