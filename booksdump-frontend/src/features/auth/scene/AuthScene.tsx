import React, { useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react';

import { createCompositionTracker, keyboardMayBeOpen } from '@/features/auth/scene/viewport';
import type { SceneHandle } from '@/features/auth/scene/createScene';
import type { RoomControl } from '@/features/auth/scene/roomControl';

/**
 * The reading room behind the screens a reader meets before signing in.
 *
 * The screen never waits for it. What paints first is a flat dark backdrop in
 * the room's own colours; the scene — three.js, its textures, a typeset book —
 * is a separate chunk fetched after that, and its canvas fades in over the
 * backdrop once the first frame is drawn. Where it cannot be had at all (no
 * WebGL 2, a context the browser refuses, an asset that does not arrive) the
 * backdrop simply stays, and nothing about the form changes.
 *
 * Every screen in the frame mounts this afresh, so a scene is built per mount
 * and taken down on unmount: renderer, GPU memory, listeners and the loop. A
 * phone turned on its side gets a new composition rather than a stretched one,
 * which means a new scene on a new canvas — a canvas whose context has been
 * handed back cannot be drawn on again.
 *
 * It is decoration: aria-hidden, and the golden post it hides is an easter egg
 * with a plain link to the same place on the card.
 */

/** three.js r170 renders with WebGL 2 only; asking for less would fail later and louder. */
function hasWebGL2(): boolean {
    // jsdom and very old browsers have no WebGL at all; asking a canvas there
    // only produces an error message on the console.
    if (typeof WebGL2RenderingContext === 'undefined') return false;
    try {
        const gl = document.createElement('canvas').getContext('webgl2');
        if (!gl) return false;
        // The probe would otherwise hold one of the few contexts a page gets.
        gl.getExtension('WEBGL_lose_context')?.loseContext();
        return true;
    } catch {
        return false;
    }
}

// The composition is decided once per resize, when it happens, and kept: the
// keyboard check depends on focus, which can change between a resize and the
// next render, and a render in that gap must not flip the room. The keyboard
// and the URL bar change a phone's height in place; see createCompositionTracker.
const composition = createCompositionTracker();
let phoneNow: boolean | null = null;
let listening = 0;
const readComposition = () =>
    (phoneNow = composition.phone(window.innerWidth, window.innerHeight, keyboardMayBeOpen()));
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
        // stale: the next scene reads the viewport afresh.
        if (--listening === 0) phoneNow = null;
    };
};
const phoneSnapshot = () => phoneNow ?? readComposition();

const REDUCED_MOTION = '(prefers-reduced-motion: reduce)';
const subscribeMotion = (onChange: () => void) => {
    const mq = window.matchMedia(REDUCED_MOTION);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
};
const motionSnapshot = () => window.matchMedia(REDUCED_MOTION).matches;

type AuthSceneProps = {
    /** The golden post was caught. */
    onGolden: () => void;
    /** The error page's book, with a page torn out. */
    torn?: boolean;
    /** The room host's line to the live scene; see roomControl.ts. */
    control?: RoomControl;
};

const AuthScene: React.FC<AuthSceneProps> = ({ onGolden, torn = false, control }) => {
    const [supported] = useState(hasWebGL2);
    const phone = useSyncExternalStore(subscribeResize, phoneSnapshot);
    const still = useSyncExternalStore(subscribeMotion, motionSnapshot);
    // One scene per composition: the key names it, and a new key is a new
    // canvas as well as a new scene.
    const key = `${phone ? 'phone' : 'desk'}-${still ? 'still' : 'moving'}`;
    const [readyKey, setReadyKey] = useState<string | null>(null);
    const [failed, setFailed] = useState(false);
    // The canvas as state, through a callback ref, not an object ref: the
    // effect has to run again when the node arrives. Under React's development
    // double-mount the object ref was still empty when the effect re-ran, so
    // the second scene was never built and the room stayed a flat backdrop.
    // It carries the composition it was made for: when the phone turns, the
    // effect runs once before the new canvas is attached, and must not build
    // on the old one, whose context is about to be given back.
    const [canvas, setCanvas] = useState<{ node: HTMLCanvasElement; key: string } | null>(null);
    const attach = useCallback(
        (node: HTMLCanvasElement | null) => setCanvas(node ? { node, key } : null),
        [key],
    );
    const goldenRef = useRef(onGolden);
    const handleRef = useRef<SceneHandle | null>(null);
    // The book a new scene starts with. Later changes go to the live scene:
    // tearing a page out is not a reason to rebuild the room.
    const tornRef = useRef(torn);

    useEffect(() => {
        goldenRef.current = onGolden;
    }, [onGolden]);

    useEffect(() => {
        tornRef.current = torn;
        handleRef.current?.setTorn(torn);
    }, [torn]);

    useEffect(() => {
        if (!canvas || canvas.key !== key) return;
        const ctrl = new AbortController();
        let handle: SceneHandle | null = null;

        import('@/features/auth/scene/createScene')
            .then(({ default: createScene }) =>
                createScene({
                    canvas: canvas.node,
                    phone,
                    still,
                    torn: tornRef.current,
                    onGolden: () => goldenRef.current(),
                    signal: ctrl.signal,
                    quality: control?.quality,
                }),
            )
            .then((scene) => {
                // The screen went while the scene was being built.
                if (ctrl.signal.aborted) {
                    scene.dispose();
                    return;
                }
                handle = scene;
                handleRef.current = scene;
                control?.attach(scene);
                // The book may have changed while the scene was being built.
                scene.setTorn(tornRef.current);
                setReadyKey(key);
            })
            .catch((error: unknown) => {
                if (ctrl.signal.aborted) return;
                // Not an error for the reader — the backdrop is what they see
                // either way — but worth a line for whoever is looking.
                console.warn('Auth scene unavailable, keeping the static backdrop:', error);
                setFailed(true);
            });

        return () => {
            ctrl.abort();
            if (handleRef.current === handle) {
                handleRef.current = null;
                control?.attach(null);
            }
            handle?.dispose();
        };
    }, [canvas, key, phone, still, supported, failed, control]);

    return (
        <>
            {/*
              The room before the room: its darkness, with the warmth of the
              shaft where the book will be. It stays under the canvas, so the
              fade-in has something to fade in over.
            */}
            <div
                aria-hidden
                data-auth-backdrop
                className="pointer-events-none fixed inset-0 bg-[#120e0b] bg-[radial-gradient(ellipse_at_28%_72%,rgba(255,196,130,0.10),transparent_55%)]"
            />
            {supported && !failed && (
                <canvas
                    key={key}
                    ref={attach}
                    aria-hidden
                    data-ready={readyKey === key}
                    // touch-none: on a phone a finger on the scene is wind, not a
                    // scroll or a pinch.
                    className="fixed inset-0 block h-full w-full touch-none opacity-0 transition-opacity duration-1000 data-[ready=true]:opacity-100"
                />
            )}
        </>
    );
};

// The room host re-renders on every navigation; the scene must not.
export default React.memo(AuthScene);
