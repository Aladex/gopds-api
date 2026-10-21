import React, { useEffect, useRef, useState, useSyncExternalStore } from 'react';

import { coverFit, toBox } from '@/features/auth/room/cover';
import {
    markersFor,
    pickSource,
    sourcesFor,
    variantFor,
    type Markers,
} from '@/features/auth/room/variants';
import {
    createCompositionTracker,
    keyboardMayBeOpen,
    touchFirst,
    type Composition,
} from '@/features/auth/room/viewport';

/**
 * The reading room behind the screens a reader meets before signing in: a
 * recording of it, playing on a loop.
 *
 * It used to be the scene itself, live in WebGL. On a fast machine that was
 * the nicer thing; on a phone it fought the form for the main thread and, on
 * the owner's own phone, settled into a frozen still. A video decodes in
 * hardware on anything that can show a web page, costs the main thread
 * nothing, and looks the same everywhere. The recordings are made from the
 * real scene by tools/auth-room; this component only chooses one and shows it.
 *
 * One element for the room's whole life. Moving between screens keeps it
 * playing; the error page's torn book and a phone turned on its side change
 * its source on the same element, with the last frame held over the swap so
 * there is no flash.
 *
 * Nothing moves for a reader who asked for reduced motion or whose connection
 * asks to save data: they get the poster, the recording's first frame.
 *
 * What the live scene did under the hand is kept as a light overlay, placed
 * with the same arithmetic as object-fit: cover from positions the recorder
 * exported: the golden post on the table can still be caught, and the mask's
 * pupils still glow, brighter when the hand comes near. On a mouse the whole
 * picture shifts a few pixels after the pointer, which is all the depth the
 * live scene's cursor ever gave it.
 */

// The composition is decided once per resize, when it happens, and kept: the
// keyboard check depends on focus, which can change between a resize and the
// next render, and a render in that gap must not flip the room. The keyboard
// and the URL bar change a phone's height in place; see createCompositionTracker.
const composition = createCompositionTracker();
let compositionNow: Composition | null = null;
let listening = 0;
const readComposition = () =>
    (compositionNow = composition.composition(
        window.innerWidth,
        window.innerHeight,
        keyboardMayBeOpen(),
    ));
const subscribeResize = (onChange: () => void) => {
    const onResize = () => {
        readComposition();
        onChange();
    };
    listening++;
    window.addEventListener('resize', onResize);
    return () => {
        window.removeEventListener('resize', onResize);
        // Nobody is following resizes any more, so the kept answer would go
        // stale: the next room reads the viewport afresh.
        if (--listening === 0) compositionNow = null;
    };
};
const compositionSnapshot = () => compositionNow ?? readComposition();

const REDUCED_MOTION = '(prefers-reduced-motion: reduce)';
const subscribeMotion = (onChange: () => void) => {
    const mq = window.matchMedia(REDUCED_MOTION);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
};
const motionSnapshot = () => window.matchMedia(REDUCED_MOTION).matches;

const sizeSnapshot = () => `${window.innerWidth}x${window.innerHeight}`;

/** Data saver: navigator.connection is not everywhere, and not in the DOM types. */
const savesData = () =>
    (navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData ===
    true;

/** How far the picture follows the mouse, in pixels, and the zoom that hides its edges. */
const PARALLAX_PX = 10;
const PARALLAX_ZOOM = 1.03;

let announced = false;
/** The blog's own boast, as its pages printed it: once, when the room first shows. */
const announce = () => {
    if (announced) return;
    announced = true;
    // eslint-disable-next-line no-console -- this line is the feature
    console.info(
        `Витенька, я сгенерировала эту страницу за ${(performance.now() / 1000).toFixed(2)} секунды!`,
    );
};

type RoomVideoProps = {
    /** The golden post was caught. */
    onGolden: () => void;
    /** The error page's room: the book with a page torn out. */
    torn?: boolean;
};

const RoomVideo: React.FC<RoomVideoProps> = ({ onGolden, torn = false }) => {
    const framing = useSyncExternalStore(subscribeResize, compositionSnapshot);
    const reduced = useSyncExternalStore(subscribeMotion, motionSnapshot);
    const size = useSyncExternalStore(subscribeResize, sizeSnapshot);
    // Reduced motion follows the setting live; data saver is read once.
    const [saveData] = useState(savesData);
    const still = reduced || saveData;
    const [fine] = useState(() => window.matchMedia('(pointer: fine)').matches && !touchFirst());
    const variant = variantFor(framing, torn);
    const sources = sourcesFor(variant);
    const videoRef = useRef<HTMLVideoElement>(null);
    const freezeRef = useRef<HTMLCanvasElement>(null);
    const layerRef = useRef<HTMLDivElement>(null);

    // The source, set here rather than in the markup, so that a change of
    // variant can hold the frame on screen first. It also runs when the
    // poster gives way to the video: that is a new element, with no source.
    useEffect(() => {
        const video = videoRef.current;
        const freeze = freezeRef.current;
        if (!video) {
            // Poster only: nothing will play to lift a frame held over a swap.
            if (freeze) freeze.style.opacity = '0';
            return;
        }
        const sources = sourcesFor(variant);
        const url = pickSource(sources, (type) => video.canPlayType(type));
        if (video.getAttribute('src') === url) return;
        if (freeze && video.getAttribute('src') && video.readyState >= 2 && video.videoWidth) {
            freeze.width = video.videoWidth;
            freeze.height = video.videoHeight;
            freeze.getContext('2d')?.drawImage(video, 0, 0);
            freeze.style.opacity = '1';
            video.addEventListener('playing', () => (freeze.style.opacity = '0'), { once: true });
        }
        video.poster = sources.poster;
        video.muted = true;
        video.src = url;
        video.play()?.catch(() => {
            // Autoplay refused or the source unplayable: the poster stays,
            // which is a perfectly good room.
        });
    }, [variant, still]);

    useEffect(() => {
        const video = videoRef.current;
        if (!video) {
            announce();
            return;
        }
        video.addEventListener('playing', announce, { once: true });
        return () => video.removeEventListener('playing', announce);
    }, [still]);

    // Parallax, on a mouse only and never under reduced motion: eased toward
    // the pointer, a transform and nothing else, and no frames asked for once
    // it has caught up.
    const follows = fine && !reduced;
    useEffect(() => {
        const layer = layerRef.current;
        if (!follows || !layer) return;
        let target = [0, 0];
        let at = [0, 0];
        let id = 0;
        const tick = () => {
            at = [at[0] + (target[0] - at[0]) * 0.08, at[1] + (target[1] - at[1]) * 0.08];
            layer.style.transform = `translate3d(${at[0].toFixed(2)}px, ${at[1].toFixed(2)}px, 0) scale(${PARALLAX_ZOOM})`;
            const moving = Math.abs(target[0] - at[0]) + Math.abs(target[1] - at[1]) > 0.05;
            id = moving ? requestAnimationFrame(tick) : 0;
        };
        const onMove = (e: PointerEvent) => {
            target = [
                -((e.clientX / window.innerWidth) * 2 - 1) * PARALLAX_PX,
                -((e.clientY / window.innerHeight) * 2 - 1) * PARALLAX_PX,
            ];
            if (!id) id = requestAnimationFrame(tick);
        };
        window.addEventListener('pointermove', onMove, { passive: true });
        return () => {
            window.removeEventListener('pointermove', onMove);
            if (id) cancelAnimationFrame(id);
            // Turned off mid-movement: the picture goes back where it was.
            layer.style.transform = '';
        };
    }, [follows]);

    const [w, h] = size.split('x').map(Number);
    const markers = markersFor(variant);

    return (
        <div aria-hidden className="pointer-events-none fixed inset-0 overflow-hidden bg-[#120e0b]">
            <div ref={layerRef} data-room-layer className="absolute inset-0">
                {still ? (
                    <img
                        src={sources.poster}
                        alt=""
                        onLoad={announce}
                        className="absolute inset-0 h-full w-full object-cover"
                    />
                ) : (
                    <video
                        ref={videoRef}
                        poster={sources.poster}
                        autoPlay
                        muted
                        loop
                        playsInline
                        preload="auto"
                        className="absolute inset-0 h-full w-full object-cover"
                    />
                )}
                <canvas
                    ref={freezeRef}
                    className="absolute inset-0 h-full w-full object-cover opacity-0 transition-opacity duration-300"
                />
                {markers && (
                    <Eggs markers={markers} box={{ width: w, height: h }} onGolden={onGolden} />
                )}
            </div>
            {/* Film grain, laid over the video instead of baked into it: noise
                is what a video encoder can least afford, and this also breaks
                up any banding in the dark. */}
            <div className="room-grain" />
        </div>
    );
};

type EggsProps = {
    markers: Markers;
    box: { width: number; height: number };
    onGolden: () => void;
};

/** The golden post's hotspot and the mask's glowing pupils, over the video. */
const Eggs: React.FC<EggsProps> = ({ markers, box, onGolden }) => {
    const fit = coverFit({ width: markers.width, height: markers.height }, box);
    const [near, setNear] = useState(false);
    const m = markers.mask;
    const [mx0, my0] = toBox([m.x, m.y], fit);
    const [mx1, my1] = toBox([m.x + m.w, m.y + m.h], fit);
    // The pupils' glow, sized to the mask as it is drawn.
    const glow = Math.max(12, (mx1 - mx0) * 0.3);

    useEffect(() => {
        // Near the mask: inside its box grown by half its size on each side.
        const padX = (mx1 - mx0) / 2;
        const padY = (my1 - my0) / 2;
        let timer: ReturnType<typeof setTimeout> | undefined;
        const at = (e: PointerEvent) =>
            e.clientX > mx0 - padX &&
            e.clientX < mx1 + padX &&
            e.clientY > my0 - padY &&
            e.clientY < my1 + padY;
        const onMove = (e: PointerEvent) => {
            if (e.pointerType === 'mouse' || e.pointerType === '') setNear(at(e));
        };
        // A tap has no hover: it lights the eyes for a moment instead.
        const onDown = (e: PointerEvent) => {
            if (e.pointerType === 'mouse' || !at(e)) return;
            setNear(true);
            clearTimeout(timer);
            timer = setTimeout(() => setNear(false), 1500);
        };
        window.addEventListener('pointermove', onMove, { passive: true });
        window.addEventListener('pointerdown', onDown, { passive: true });
        return () => {
            window.removeEventListener('pointermove', onMove);
            window.removeEventListener('pointerdown', onDown);
            clearTimeout(timer);
        };
    }, [mx0, my0, mx1, my1]);

    return (
        <>
            <svg className="absolute inset-0 h-full w-full" width={box.width} height={box.height}>
                <polygon
                    data-golden-post
                    points={markers.goldenPost
                        .map((p) =>
                            toBox(p, fit)
                                .map((n) => n.toFixed(1))
                                .join(','),
                        )
                        .join(' ')}
                    fill="transparent"
                    className="pointer-events-auto cursor-pointer"
                    onClick={onGolden}
                />
            </svg>
            <div data-eyes data-near={near} className="room-eyes">
                {markers.pupils.map((p, i) => {
                    const [x, y] = toBox(p, fit);
                    return (
                        <div
                            key={i}
                            data-eye
                            className="room-eye"
                            style={{ left: x, top: y, width: glow, height: glow }}
                        />
                    );
                })}
            </div>
        </>
    );
};

// The room host re-renders on every navigation; the room must not.
export default React.memo(RoomVideo);
