import { act, fireEvent, render } from '@testing-library/react';

import RoomVideo from '@/features/auth/room/RoomVideo';
import { coverFit, toBox } from '@/features/auth/room/cover';
import { markersFor } from '@/features/auth/room/variants';

/*
 * The reading room is a recorded video now (see tools/auth-room): one element
 * that plays on a loop behind every public screen. What is checked here is
 * which recording it plays, when it plays nothing but its poster, the
 * parallax, and that the Easter eggs land on the things they belong to.
 */

function viewport(width: number, height: number) {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: width });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: height });
    window.dispatchEvent(new Event('resize'));
}

/** Media queries: reduced motion, and what kind of pointer this is. */
const prefs = { reduced: false, fine: true };
let motionListeners: (() => void)[] = [];
function media({ reduced = false, fine = true }: { reduced?: boolean; fine?: boolean } = {}) {
    Object.assign(prefs, { reduced, fine });
    window.matchMedia = (query: string) =>
        ({
            matches:
                (query.includes('prefers-reduced-motion') && prefs.reduced) ||
                (query === '(pointer: fine)' && prefs.fine) ||
                (query === '(pointer: coarse)' && !prefs.fine) ||
                (query === '(hover: none)' && !prefs.fine),
            media: query,
            addEventListener: (_: string, cb: () => void) => {
                if (query.includes('prefers-reduced-motion')) motionListeners.push(cb);
            },
            removeEventListener: (_: string, cb: () => void) => {
                motionListeners = motionListeners.filter((l) => l !== cb);
            },
        }) as unknown as MediaQueryList;
}
/** The reader changes the reduced-motion setting while the page is open. */
const setReduced = (reduced: boolean) =>
    act(() => {
        prefs.reduced = reduced;
        motionListeners.forEach((cb) => cb());
    });

const originalMatchMedia = window.matchMedia;
let frames: { id: number; cb: FrameRequestCallback }[] = [];
let nextFrame = 1;
const runFrames = (n: number) => {
    for (let i = 0; i < n; i++) {
        const due = frames;
        frames = [];
        due.forEach(({ cb }) => cb(i * 16));
    }
};

beforeEach(() => {
    media();
    viewport(1440, 900);
    frames = [];
    motionListeners = [];
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
        frames.push({ id: nextFrame, cb });
        return nextFrame++;
    });
    vi.stubGlobal('cancelAnimationFrame', (id: number) => {
        frames = frames.filter((f) => f.id !== id);
    });
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('');
    vi.spyOn(console, 'info').mockImplementation(() => {});
});

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    window.matchMedia = originalMatchMedia;
    Object.defineProperty(navigator, 'connection', { configurable: true, value: undefined });
});

const video = (c: HTMLElement) => c.querySelector('video') as HTMLVideoElement;

describe('RoomVideo: the recording it plays', () => {
    it('plays the desktop room, muted, looped and inline, with its poster until it starts', () => {
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        const v = video(container);

        expect(v).not.toBeNull();
        expect(v.muted).toBe(true);
        expect(v.loop).toBe(true);
        expect(v.autoplay).toBe(true);
        expect(v.hasAttribute('playsinline')).toBe(true);
        expect(v.preload).toBe('auto');
        expect(v.getAttribute('poster')).toBe('/auth-room/desktop.webp');
        // No VP9 here: the MP4 fallback.
        expect(v.getAttribute('src')).toBe('/auth-room/desktop.mp4');
    });

    it('prefers WebM where the browser plays VP9', () => {
        vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue('probably');
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);

        expect(video(container).getAttribute('src')).toBe('/auth-room/desktop.webm');
    });

    it('plays the phone composition on a phone', () => {
        viewport(390, 844);
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);

        expect(video(container).getAttribute('src')).toBe('/auth-room/phone.mp4');
    });

    it('plays the ultrawide room on a 21:9 screen, and its torn book on the error page', () => {
        viewport(3440, 1440);
        const { container, rerender } = render(<RoomVideo onGolden={vi.fn()} />);
        const v = video(container);
        expect(v.getAttribute('src')).toBe('/auth-room/ultrawide.mp4');
        expect(v.getAttribute('poster')).toBe('/auth-room/ultrawide.webp');

        rerender(<RoomVideo onGolden={vi.fn()} torn />);
        expect(v.getAttribute('src')).toBe('/auth-room/ultrawide-torn.mp4');
    });

    it('moves to the ultrawide room on the same element when the window is made wide', () => {
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        const first = video(container);
        expect(first.getAttribute('src')).toBe('/auth-room/desktop.mp4');

        act(() => viewport(3440, 1300));

        expect(video(container)).toBe(first);
        expect(first.getAttribute('src')).toBe('/auth-room/ultrawide.mp4');
    });

    it('keeps a landscape tablet on its recording while the keyboard opens and closes', () => {
        media({ fine: false });
        viewport(1024, 768);
        const field = document.body.appendChild(document.createElement('input'));
        field.focus();
        try {
            const { container } = render(<RoomVideo onGolden={vi.fn()} />);
            const v = video(container);
            expect(v.getAttribute('src')).toBe('/auth-room/desktop.mp4');
            const play = vi.mocked(HTMLMediaElement.prototype.play);
            const plays = play.mock.calls.length;

            act(() => viewport(1024, 400));
            expect(v.getAttribute('src')).toBe('/auth-room/desktop.mp4');
            act(() => viewport(1024, 768));
            expect(v.getAttribute('src')).toBe('/auth-room/desktop.mp4');
            // No source swap, so nothing restarted either.
            expect(play.mock.calls.length).toBe(plays);
        } finally {
            field.remove();
        }
    });

    it('tears the book on the same element, without making a new one', () => {
        const { container, rerender } = render(<RoomVideo onGolden={vi.fn()} />);
        const first = video(container);

        rerender(<RoomVideo onGolden={vi.fn()} torn />);

        expect(video(container)).toBe(first);
        expect(first.getAttribute('src')).toBe('/auth-room/desktop-torn.mp4');
        expect(first.getAttribute('poster')).toBe('/auth-room/desktop-torn.webp');
    });

    it('holds the last frame over the swap until the new recording plays: no flash', () => {
        const { container, rerender } = render(<RoomVideo onGolden={vi.fn()} />);
        const v = video(container);
        Object.defineProperty(v, 'readyState', { configurable: true, value: 4 });
        Object.defineProperty(v, 'videoWidth', { configurable: true, value: 1920 });
        Object.defineProperty(v, 'videoHeight', { configurable: true, value: 1080 });
        const drawImage = vi.fn();
        vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
            drawImage,
        } as never);

        rerender(<RoomVideo onGolden={vi.fn()} torn />);
        const freeze = container.querySelector('canvas') as HTMLCanvasElement;
        expect(drawImage).toHaveBeenCalledWith(v, 0, 0);
        expect(freeze.style.opacity).toBe('1');

        fireEvent(v, new Event('playing'));
        expect(freeze.style.opacity).toBe('0');
    });
});

describe('RoomVideo: when only the poster is shown', () => {
    it('shows the poster and plays nothing when the reader prefers reduced motion', () => {
        media({ reduced: true });
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);

        expect(video(container)).toBeNull();
        expect(container.querySelector('img')).toHaveAttribute('src', '/auth-room/desktop.webp');
    });

    it('shows the poster and plays nothing when the connection asks to save data', () => {
        Object.defineProperty(navigator, 'connection', {
            configurable: true,
            value: { saveData: true },
        });
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);

        expect(video(container)).toBeNull();
        expect(container.querySelector('img')).toHaveAttribute('src', '/auth-room/desktop.webp');
    });
});

describe('RoomVideo: between the poster and the video', () => {
    const play = () => vi.mocked(HTMLMediaElement.prototype.play);

    it('starts the recording when reduced motion is turned off on a room that opened as a poster', () => {
        media({ reduced: true });
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        expect(video(container)).toBeNull();

        setReduced(false);

        const v = video(container);
        expect(v.getAttribute('src')).toBe('/auth-room/desktop.mp4');
        expect(play().mock.contexts).toContain(v);
    });

    it('starts the recording again after reduced motion is turned on and off while it plays', () => {
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        const first = video(container);
        expect(first.getAttribute('src')).toBe('/auth-room/desktop.mp4');

        setReduced(true);
        expect(video(container)).toBeNull();
        expect(container.querySelector('img')).toHaveAttribute('src', '/auth-room/desktop.webp');

        setReduced(false);

        const v = video(container);
        expect(v).not.toBe(first);
        expect(v.getAttribute('src')).toBe('/auth-room/desktop.mp4');
        expect(play().mock.contexts).toContain(v);
    });

    it('lifts a frame held over a swap when the room turns into its poster', () => {
        const { container, rerender } = render(<RoomVideo onGolden={vi.fn()} />);
        const v = video(container);
        Object.defineProperty(v, 'readyState', { configurable: true, value: 4 });
        Object.defineProperty(v, 'videoWidth', { configurable: true, value: 1920 });
        Object.defineProperty(v, 'videoHeight', { configurable: true, value: 1080 });
        vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
            drawImage: vi.fn(),
        } as never);
        rerender(<RoomVideo onGolden={vi.fn()} torn />);
        const freeze = container.querySelector('canvas') as HTMLCanvasElement;
        expect(freeze.style.opacity).toBe('1');

        // The video that would have lifted it is gone before it played.
        setReduced(true);

        expect(freeze.style.opacity).toBe('0');
    });
});

describe('RoomVideo: parallax', () => {
    const layer = (c: HTMLElement) => c.querySelector('[data-room-layer]') as HTMLElement;

    it('follows a mouse with a small shift and a slight zoom', () => {
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);

        fireEvent.pointerMove(window, { clientX: 1440, clientY: 0 });
        runFrames(30);

        expect(layer(container).style.transform).toMatch(/translate3d\(-\d/);
        expect(layer(container).style.transform).toContain('scale(1.03)');
    });

    it('stays put on a touch device: a finger is not a hand hovering', () => {
        media({ fine: false });
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);

        fireEvent.pointerMove(window, { clientX: 300, clientY: 0 });
        runFrames(30);

        expect(layer(container).style.transform).toBe('');
        expect(frames).toHaveLength(0);
    });

    it('stays put under reduced motion, poster and all', () => {
        media({ reduced: true });
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);

        fireEvent.pointerMove(window, { clientX: 1440, clientY: 0 });
        runFrames(30);

        expect(layer(container).style.transform).toBe('');
        expect(frames).toHaveLength(0);
    });

    it('lets go of the picture when reduced motion is turned on mid-movement', () => {
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        fireEvent.pointerMove(window, { clientX: 1440, clientY: 0 });
        runFrames(3);
        expect(layer(container).style.transform).not.toBe('');
        expect(frames).toHaveLength(1);

        setReduced(true);

        // Back in place, the frame in flight cancelled, the pointer ignored.
        expect(layer(container).style.transform).toBe('');
        expect(frames).toHaveLength(0);
        fireEvent.pointerMove(window, { clientX: 0, clientY: 900 });
        runFrames(30);
        expect(layer(container).style.transform).toBe('');
        expect(frames).toHaveLength(0);
        // Turned off again: the picture follows the mouse once more.
        setReduced(false);
        fireEvent.pointerMove(window, { clientX: 0, clientY: 900 });
        runFrames(30);
        expect(layer(container).style.transform).toMatch(/translate3d\(\d/);
    });
});

describe('RoomVideo: framing', () => {
    it('anchors the desktop recording at the bottom, the desk, and its overlay with it', () => {
        viewport(1920, 945);
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        expect(video(container).style.objectPosition).toBe('50% 100%');

        const fit = coverFit(
            { width: 1920, height: 1080 },
            { width: 1920, height: 945 },
            { x: 0.5, y: 1 },
        );
        const expected = markersFor('desktop')
            .goldenPost.map((p) =>
                toBox(p, fit)
                    .map((n) => n.toFixed(1))
                    .join(','),
            )
            .join(' ');
        expect(container.querySelector('[data-golden-post]')!.getAttribute('points')).toBe(
            expected,
        );
    });

    it('keeps the ultrawide recording centred', () => {
        viewport(3440, 1440);
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        expect(video(container).style.objectPosition).toBe('50% 50%');
    });

    it('holds the old frame where it was when the window crosses into the ultrawide band', () => {
        viewport(1920, 945);
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        const v = video(container);
        Object.defineProperty(v, 'readyState', { configurable: true, value: 4 });
        Object.defineProperty(v, 'videoWidth', { configurable: true, value: 1920 });
        Object.defineProperty(v, 'videoHeight', { configurable: true, value: 1080 });
        vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({
            drawImage: vi.fn(),
        } as never);

        act(() => viewport(3440, 1440));

        const freeze = container.querySelector('canvas') as HTMLCanvasElement;
        expect(freeze.style.opacity).toBe('1');
        // The held frame is the desktop one, anchored as it was shown…
        expect(freeze.style.objectPosition).toBe('50% 100%');
        // …while the new recording comes in centred.
        expect(v.style.objectPosition).toBe('50% 50%');
    });

    it('anchors the poster the same way as the video', () => {
        media({ reduced: true });
        viewport(1920, 945);
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        expect((container.querySelector('img') as HTMLElement).style.objectPosition).toBe(
            '50% 100%',
        );
    });
});

describe('RoomVideo: the Easter eggs', () => {
    const fit = () => coverFit({ width: 1920, height: 1080 }, { width: 1440, height: 900 });

    it('puts the golden post’s hotspot over the gold page on the table, and it opens registration', () => {
        const onGolden = vi.fn();
        const { container } = render(<RoomVideo onGolden={onGolden} />);
        const polygon = container.querySelector('[data-golden-post]') as SVGPolygonElement;

        const expected = markersFor('desktop')
            .goldenPost.map((p) =>
                toBox(p, fit())
                    .map((n) => n.toFixed(1))
                    .join(','),
            )
            .join(' ');
        expect(polygon.getAttribute('points')).toBe(expected);

        fireEvent.click(polygon);
        expect(onGolden).toHaveBeenCalledTimes(1);
    });

    it('puts a glow over each pupil', () => {
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        const eyes = [...container.querySelectorAll('[data-eye]')] as HTMLElement[];

        expect(eyes).toHaveLength(2);
        markersFor('desktop').pupils.forEach((p, i) => {
            const [x, y] = toBox(p, fit());
            expect(parseFloat(eyes[i].style.left)).toBeCloseTo(x, 0);
            expect(parseFloat(eyes[i].style.top)).toBeCloseTo(y, 0);
        });
    });

    it('brightens the eyes when the hand comes near the mask', () => {
        const { container } = render(<RoomVideo onGolden={vi.fn()} />);
        const eyes = container.querySelector('[data-eyes]') as HTMLElement;
        expect(eyes.dataset.near).toBe('false');

        const m = markersFor('desktop').mask;
        const [x, y] = toBox([m.x + m.w / 2, m.y + m.h / 2], fit());
        act(() => fireEvent.pointerMove(window, { clientX: x, clientY: y }));

        expect(eyes.dataset.near).toBe('true');
    });
});

describe('RoomVideo as the room host’s child', () => {
    it('is memoised, so the host re-rendering on every navigation leaves it alone', () => {
        expect((RoomVideo as unknown as { $$typeof: symbol }).$$typeof).toBe(
            Symbol.for('react.memo'),
        );
    });
});
