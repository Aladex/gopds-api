import { act, render, waitFor } from '@testing-library/react';

import AuthScene from '@/features/auth/scene/AuthScene';
import createScene, { type SceneOptions } from '@/features/auth/scene/createScene';

/*
 * The scene itself needs a GPU, which jsdom has not got; what is checked here
 * is everything around it — when it is asked for, what it is asked for, and
 * that whatever was made is taken down again.
 */

vi.mock('@/features/auth/scene/createScene', () => ({ default: vi.fn() }));
const create = vi.mocked(createScene);

/** Scenes made and not yet disposed. */
let live = 0;
let disposals: ReturnType<typeof vi.fn>[] = [];

function sceneResolvesAtOnce() {
    create.mockImplementation(async () => {
        live++;
        const dispose = vi.fn(() => void live--);
        disposals.push(dispose);
        return { dispose };
    });
}

/** What the browser can draw with: WebGL 2 or nothing. */
function withWebGL(available: boolean) {
    if (available) {
        vi.stubGlobal('WebGL2RenderingContext', class {});
        vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
            () => ({ getExtension: () => ({ loseContext() {} }) }) as never,
        );
    } else {
        vi.stubGlobal('WebGL2RenderingContext', undefined);
    }
}

function mediaQueries(reducedMotion: boolean) {
    window.matchMedia = (query: string) =>
        ({
            matches: query.includes('prefers-reduced-motion') && reducedMotion,
            media: query,
            addEventListener: () => {},
            removeEventListener: () => {},
        }) as unknown as MediaQueryList;
}

function viewport(width: number, height: number) {
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: width });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: height });
    window.dispatchEvent(new Event('resize'));
}

const originalMatchMedia = window.matchMedia;

beforeEach(() => {
    live = 0;
    disposals = [];
    create.mockReset();
    sceneResolvesAtOnce();
    mediaQueries(false);
    viewport(1440, 900);
});

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    window.matchMedia = originalMatchMedia;
});

describe('AuthScene without WebGL', () => {
    it('draws no canvas and never loads the scene, leaving the static backdrop', () => {
        withWebGL(false);
        const errors = vi.spyOn(console, 'error');
        const { container } = render(<AuthScene onGolden={vi.fn()} />);

        expect(container.querySelector('canvas')).toBeNull();
        expect(container.querySelector('[data-auth-backdrop]')).not.toBeNull();
        expect(create).not.toHaveBeenCalled();
        expect(errors).not.toHaveBeenCalled();
    });
});

describe('AuthScene where WebGL 2 exists but no context is given', () => {
    it('draws no canvas and never loads the scene', () => {
        vi.stubGlobal('WebGL2RenderingContext', class {});
        vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null);
        const { container } = render(<AuthScene onGolden={vi.fn()} />);

        expect(container.querySelector('canvas')).toBeNull();
        expect(create).not.toHaveBeenCalled();
    });
});

describe('AuthScene with WebGL', () => {
    beforeEach(() => withWebGL(true));

    it('builds the scene on its canvas, animated, for the desktop composition', async () => {
        const { container } = render(<AuthScene onGolden={vi.fn()} />);

        await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
        const opts = create.mock.calls[0][0] as SceneOptions;
        expect(opts.canvas).toBe(container.querySelector('canvas'));
        expect(opts.still).toBe(false);
        expect(opts.phone).toBe(false);
    });

    it('asks for the torn book on the error page, and the whole one elsewhere', async () => {
        const { unmount } = render(<AuthScene onGolden={vi.fn()} />);
        await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
        expect((create.mock.calls[0][0] as SceneOptions).torn).toBe(false);
        unmount();

        render(<AuthScene onGolden={vi.fn()} torn />);
        await waitFor(() => expect(create).toHaveBeenCalledTimes(2));
        expect((create.mock.calls[1][0] as SceneOptions).torn).toBe(true);
    });

    it('asks for a still frame when the reader prefers reduced motion', async () => {
        mediaQueries(true);
        render(<AuthScene onGolden={vi.fn()} />);

        await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
        expect((create.mock.calls[0][0] as SceneOptions).still).toBe(true);
    });

    it('fades the canvas in only once the scene is there', async () => {
        const { container } = render(<AuthScene onGolden={vi.fn()} />);
        const canvas = container.querySelector('canvas')!;
        expect(canvas.dataset.ready).toBe('false');

        await waitFor(() => expect(canvas.dataset.ready).toBe('true'));
    });

    it('disposes the scene on unmount', async () => {
        const { unmount } = render(<AuthScene onGolden={vi.fn()} />);
        await waitFor(() => expect(live).toBe(1));

        unmount();

        expect(disposals[0]).toHaveBeenCalledTimes(1);
        expect(live).toBe(0);
    });

    it('disposes a scene that arrives after the screen has already gone', async () => {
        let resolve!: () => void;
        create.mockImplementation(
            (o) =>
                new Promise((done) => {
                    live++;
                    const dispose = vi.fn(() => void live--);
                    disposals.push(dispose);
                    resolve = () => done({ dispose });
                    void o;
                }),
        );
        const { unmount } = render(<AuthScene onGolden={vi.fn()} />);
        await waitFor(() => expect(create).toHaveBeenCalled());
        const signal = (create.mock.calls[0][0] as SceneOptions).signal;

        unmount();
        expect(signal.aborted).toBe(true);
        await act(async () => resolve());

        expect(live).toBe(0);
    });

    it('never stacks scenes going from screen to screen and back', async () => {
        for (let i = 0; i < 3; i++) {
            const { unmount } = render(<AuthScene onGolden={vi.fn()} />);
            await waitFor(() => expect(create).toHaveBeenCalledTimes(i + 1));
            await waitFor(() => expect(live).toBe(1));
            unmount();
            expect(live).toBe(0);
        }
    });

    it('rebuilds the composition when a phone turns, on a fresh canvas', async () => {
        const { container } = render(<AuthScene onGolden={vi.fn()} />);
        await waitFor(() => expect(live).toBe(1));
        const first = container.querySelector('canvas');

        act(() => viewport(390, 844));

        await waitFor(() => expect(create).toHaveBeenCalledTimes(2));
        expect(disposals[0]).toHaveBeenCalledTimes(1);
        const opts = create.mock.calls[1][0] as SceneOptions;
        expect(opts.phone).toBe(true);
        // A canvas whose context was given back cannot be drawn on again.
        expect(opts.canvas).not.toBe(first);
        await waitFor(() => expect(live).toBe(1));
    });

    it('does not rebuild for a resize that keeps the orientation', async () => {
        render(<AuthScene onGolden={vi.fn()} />);
        await waitFor(() => expect(live).toBe(1));

        act(() => viewport(1280, 800));

        expect(create).toHaveBeenCalledTimes(1);
    });

    it('falls back to the static backdrop when the scene cannot be built', async () => {
        const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
        create.mockRejectedValue(new Error('no context'));
        const { container } = render(<AuthScene onGolden={vi.fn()} />);

        await waitFor(() => expect(container.querySelector('canvas')).toBeNull());
        expect(container.querySelector('[data-auth-backdrop]')).not.toBeNull();
        expect(warn).toHaveBeenCalled();
    });

    it('passes the golden post through to the screen', async () => {
        const onGolden = vi.fn();
        render(<AuthScene onGolden={onGolden} />);
        await waitFor(() => expect(create).toHaveBeenCalled());

        (create.mock.calls[0][0] as SceneOptions).onGolden();

        expect(onGolden).toHaveBeenCalledTimes(1);
    });
});
