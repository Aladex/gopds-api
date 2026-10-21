/**
 * The two things that outlive a frame and so have to be taken down by hand:
 * the animation loop and the event listeners.
 *
 * The scene is mounted afresh by every screen in the frame — login,
 * registration, back again — so anything left behind by one mount is still
 * running under the next. Both live here, out of the WebGL glue, so their
 * clean-up can be checked without a GPU.
 */

interface FrameSource {
    raf: (cb: FrameRequestCallback) => number;
    caf: (id: number) => void;
}

const browserFrames: FrameSource = {
    raf: (cb) => requestAnimationFrame(cb),
    caf: (id) => cancelAnimationFrame(id),
};

export interface Loop {
    start(): void;
    /**
     * Draw nothing for the next `frames` refreshes. The room host asks for
     * this when the reader moves to another screen, so the new card paints
     * before the scene takes the main thread again.
     */
    hold(frames: number): void;
    /** Stop for good, leaving the last frame on screen. */
    stop(): void;
    dispose(): void;
}

/**
 * The shortest interval between two drawn frames: a 60 Hz frame, less a
 * little for timer jitter, so a 60 Hz display still draws every refresh.
 * Faster displays skip refreshes — 120 Hz draws every other one — because a
 * backdrop drawn at 144 frames a second is power spent on nothing anyone can
 * see, and main-thread time taken from the interface.
 */
export const MIN_FRAME_MS = 1000 / 60 - 2;

/**
 * A requestAnimationFrame loop that stops asking for frames while the tab is
 * hidden. Browsers already throttle a hidden tab, but throttled is not
 * stopped: the scene would keep simulating paper nobody can see.
 *
 * Each frame is told how long it has been since the last drawn one, which is
 * what the quality ladder judges the device by — or null when the gap was the
 * loop's own doing (the first frame, a hold, a hidden tab), since such a gap
 * says nothing about how long a frame takes.
 *
 * `still` is for a reader who asked for reduced motion: start() draws one
 * frame there and then, and nothing is ever scheduled after it.
 */
export function createLoop(
    frame: (now: number, interval: number | null) => void,
    frames: FrameSource = browserFrames,
    { still = false }: { still?: boolean } = {},
): Loop {
    let id = 0;
    let started = false;
    let stopped = false;
    let disposed = false;
    let held = 0;
    let lastDrawn: number | null = null;

    const tick = (now: number) => {
        id = 0;
        if (held > 0) {
            held--;
            lastDrawn = null;
        } else if (lastDrawn === null || now - lastDrawn >= MIN_FRAME_MS) {
            const interval = lastDrawn === null ? null : now - lastDrawn;
            lastDrawn = now;
            frame(now, interval);
        }
        schedule();
    };
    const schedule = () => {
        if (still || stopped || disposed || !started || id || document.hidden) return;
        id = frames.raf(tick);
    };
    const cancel = () => {
        if (id) frames.caf(id);
        id = 0;
        lastDrawn = null;
    };
    const onVisibility = () => (document.hidden ? cancel() : schedule());
    document.addEventListener('visibilitychange', onVisibility);

    return {
        start() {
            if (still) {
                if (!started && !disposed) frame(performance.now(), null);
                started = true;
                return;
            }
            started = true;
            schedule();
        },
        hold(count) {
            held = Math.max(held, count);
        },
        stop() {
            stopped = true;
            cancel();
        },
        dispose() {
            disposed = true;
            cancel();
            document.removeEventListener('visibilitychange', onVisibility);
        },
    };
}

export interface ListenerBag {
    on<E extends Event>(
        target: EventTarget,
        type: string,
        handler: (event: E) => void,
        options?: AddEventListenerOptions,
    ): void;
    clear(): void;
}

/** Every listener added through it, removed in one call. */
export function listenerBag(): ListenerBag {
    const added: (() => void)[] = [];
    return {
        on(target, type, handler, options) {
            const fn = handler as EventListener;
            target.addEventListener(type, fn, options);
            added.push(() => target.removeEventListener(type, fn, options));
        },
        clear() {
            added.splice(0).forEach((remove) => remove());
        },
    };
}

export interface Owner {
    /** Register a clean-up step. Steps run in reverse order of registration. */
    add(step: () => void): void;
    /** Register a resource to dispose; returns it, so it can wrap a constructor. */
    track<T extends { dispose(): void }>(resource: T): T;
    /** Run every step once. Safe to call any number of times. */
    dispose(): void;
    readonly disposed: boolean;
}

/**
 * The one owner of everything a scene allocates, created before the first
 * allocation.
 *
 * The scene is built in a long run of steps — a GPU context, then textures,
 * materials, geometry, listeners, post-processing — and any of them can fail
 * part-way: a 2D canvas the browser will not give under memory pressure, a
 * shader that does not compile. A disposer assembled at the end only covers a
 * scene that got to the end. This one is filled as the scene is built, so
 * whatever was made before a failure is released by the same call that
 * releases a finished scene.
 *
 * Steps run last-made-first: what was made later may depend on what was made
 * earlier — the renderer on its context — never the other way round. A step
 * that throws is reported and skipped; the rest still run, because a
 * half-released GPU context is worse than a noisy console.
 */
export function createOwner(): Owner {
    const steps: (() => void)[] = [];
    const tracked = new Set<object>();
    let disposed = false;

    const run = (step: () => void) => {
        try {
            step();
        } catch (error) {
            console.warn('Auth scene: a clean-up step failed:', error);
        }
    };

    return {
        add(step) {
            // Anything handed over after the end is released straight away:
            // an asynchronous step that finished late must not outlive us.
            if (disposed) run(step);
            else steps.push(step);
        },
        track(resource) {
            if (tracked.has(resource)) return resource;
            tracked.add(resource);
            this.add(() => resource.dispose());
            return resource;
        },
        dispose() {
            if (disposed) return;
            disposed = true;
            steps.splice(0).reverse().forEach(run);
        },
        get disposed() {
            return disposed;
        },
    };
}
