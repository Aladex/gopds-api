import { Matrix4, type BufferGeometry } from 'three';

import { blockHeight } from '../book';
import { LEVELS, LOWEST_MOVING, STILL, startLevel } from '../quality';

import createScene, { CONTEXT_ATTRIBUTES, type SceneOptions } from '../createScene';

/*
 * The real scene, with the GPU taken out from under it.
 *
 * jsdom has no WebGL, so the renderer, the post-processing passes and the HDR
 * loader are stand-ins; everything else — geometry, materials, textures, the
 * scene graph, the loop and the listeners — is the real code. Every geometry,
 * material, texture and pass is recorded when it is made, so the one question
 * that matters for a scene mounted afresh by every screen can be asked
 * directly: was everything that was made also released?
 */

const reg = vi.hoisted(() => ({
    created: [] as object[],
    released: new Set<object>(),
    renderers: [] as {
        params: Record<string, unknown>;
        dispose: () => void;
        pixelRatio: number;
        sizes: number;
        shadowMap: { autoUpdate: boolean; needsUpdate: boolean };
        compiled: number;
        uploaded: number;
    }[],
    /** The scale each page canvas was asked for. */
    pageScales: [] as number[],
    order: [] as string[],
    /** Runs on every composer render; a test can make it throw or look around. */
    onRender: null as null | (() => void),
    /** Runs on every page canvas drawn; a test can make it throw. */
    onPaper: null as null | (() => void),
    /** Every mesh made, to see what is on show. */
    meshes: [] as import('three').Mesh[],
    renders: 0,
}));

vi.mock('three', async (importOriginal) => {
    const three = await importOriginal<typeof import('three')>();
    // Record every instance, and when it says it was disposed.
    type Dispatcher = import('three').EventDispatcher<{ dispose: object }>;
    // A mixin's constructor has to take any[]; the instance is what is typed.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const tracked = <C extends new (...args: any[]) => Dispatcher>(Base: C) =>
        class extends Base {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            constructor(...args: any[]) {
                super(...args);
                reg.created.push(this);
                this.addEventListener('dispose', () => reg.released.add(this));
            }
        };
    class FakeRenderer {
        params: Record<string, unknown>;
        capabilities = { getMaxAnisotropy: () => 8 };
        shadowMap = { enabled: false, type: 0, autoUpdate: true, needsUpdate: false };
        pixelRatio = 1;
        sizes = 0;
        outputColorSpace = '';
        toneMapping = 0;
        toneMappingExposure = 1;
        constructor(params: Record<string, unknown>) {
            this.params = params;
            reg.renderers.push(this);
        }
        setPixelRatio(r: number) {
            this.pixelRatio = r;
        }
        getPixelRatio() {
            return this.pixelRatio;
        }
        setSize(w: number, h: number) {
            this.sizes++;
            reg.order.push('size');
            const canvas = this.params.canvas as HTMLCanvasElement;
            canvas.width = Math.floor(w * this.pixelRatio);
            canvas.height = Math.floor(h * this.pixelRatio);
        }
        compiled = 0;
        uploaded = 0;
        compileAsync() {
            this.compiled++;
            return Promise.resolve();
        }
        initTexture() {
            this.uploaded++;
            reg.order.push('upload');
        }
        dispose() {
            reg.order.push('renderer');
            reg.released.add(this);
        }
    }
    class FakePMREM {
        constructor() {
            reg.created.push(this);
        }
        fromEquirectangular() {
            const target = {
                texture: new three.Texture(),
                dispose: () => reg.released.add(target),
            };
            reg.created.push(target);
            return target;
        }
        dispose() {
            reg.released.add(this);
        }
    }
    return {
        ...three,
        WebGLRenderer: FakeRenderer,
        PMREMGenerator: FakePMREM,
        Texture: tracked(three.Texture),
        CanvasTexture: tracked(three.CanvasTexture),
        BufferGeometry: tracked(three.BufferGeometry),
        PlaneGeometry: tracked(three.PlaneGeometry),
        BoxGeometry: tracked(three.BoxGeometry),
        ConeGeometry: tracked(three.ConeGeometry),
        SphereGeometry: tracked(three.SphereGeometry),
        ExtrudeGeometry: tracked(three.ExtrudeGeometry),
        MeshStandardMaterial: tracked(three.MeshStandardMaterial),
        MeshPhysicalMaterial: tracked(three.MeshPhysicalMaterial),
        ShaderMaterial: tracked(three.ShaderMaterial),
        InstancedMesh: tracked(three.InstancedMesh),
        Mesh: class extends three.Mesh {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            constructor(...args: any[]) {
                super(...args);
                reg.meshes.push(this);
            }
        },
    };
});

// Post-processing: one stand-in for the composer and every pass.
const fakePass = vi.hoisted(
    () =>
        class FakePass {
            passes: unknown[] = [];
            uniforms: unknown;
            constructor(...args: { uniforms?: unknown }[]) {
                this.uniforms = args[0]?.uniforms;
                reg.created.push(this);
            }
            addPass(p: unknown) {
                this.passes.push(p);
            }
            render() {
                reg.renders++;
                reg.onRender?.();
            }
            setSize() {}
            setPixelRatio() {}
            updateGtaoMaterial() {}
            dispose() {
                reg.released.add(this);
            }
        },
);
vi.mock('three/examples/jsm/postprocessing/EffectComposer.js', () => ({
    EffectComposer: fakePass,
}));
vi.mock('three/examples/jsm/postprocessing/RenderPass.js', () => ({ RenderPass: fakePass }));
vi.mock('three/examples/jsm/postprocessing/ShaderPass.js', () => ({ ShaderPass: fakePass }));
vi.mock('three/examples/jsm/postprocessing/OutputPass.js', () => ({ OutputPass: fakePass }));
// The passes the quality ladder switches carry a name, to find them by.
vi.mock('three/examples/jsm/postprocessing/BokehPass.js', () => ({
    BokehPass: class extends fakePass {
        kind = 'bokeh';
    },
}));
vi.mock('three/examples/jsm/postprocessing/UnrealBloomPass.js', () => ({
    UnrealBloomPass: class extends fakePass {
        kind = 'bloom';
    },
}));
vi.mock('three/examples/jsm/postprocessing/GTAOPass.js', () => ({
    GTAOPass: class extends fakePass {
        kind = 'gtao';
    },
}));
vi.mock('three/examples/jsm/loaders/RGBELoader.js', async () => {
    const { DataTexture } = await vi.importActual<typeof import('three')>('three');
    return {
        RGBELoader: class {
            loadAsync() {
                return Promise.resolve(new DataTexture());
            }
        },
    };
});

// Page canvases need a 2D context, which jsdom has not got; blank canvases
// stand in, and a test can make drawing one fail.
vi.mock('../textures', async (importOriginal) => {
    const real = await importOriginal<typeof import('../textures')>();
    const blank = () => document.createElement('canvas');
    return {
        ...real,
        paperCanvas: (...args: unknown[]) => {
            reg.onPaper?.();
            reg.order.push('page');
            reg.pageScales.push((args[6] as number | undefined) ?? 1);
            return blank();
        },
        edgeCanvas: blank,
        giltMaps: () => ({ metal: blank(), rough: blank() }),
    };
});

const lose = vi.fn(() => reg.order.push('context'));
const gl = {
    getExtension: (name: string) =>
        name === 'WEBGL_lose_context'
            ? { loseContext: lose }
            : name === 'WEBGL_debug_renderer_info'
              ? { UNMASKED_RENDERER_WEBGL: 0x9246 }
              : null,
    getParameter: (p: number) => (p === 0x9246 ? 'Fake GPU 9000' : 'masked'),
    // Fences: the GPU is done with a frame when the test says so.
    SYNC_GPU_COMMANDS_COMPLETE: 0x9117,
    SYNC_STATUS: 0x9114,
    SIGNALED: 0x9119,
    UNSIGNALED: 0x9118,
    gpuBusy: false,
    fences: 0,
    /** The context is gone: fences are invalid and their status reads null. */
    lost: false,
    /** A context without fences: fenceSync gives nothing back. */
    noFences: false,
    isContextLost() {
        return gl.lost;
    },
    /** Fences made before this generation are no longer fences. */
    generation: 0,
    fenceSync() {
        if (gl.noFences || gl.lost) return null;
        gl.fences++;
        return { generation: gl.generation };
    },
    getSyncParameter(fence: { generation: number }) {
        if (gl.lost || fence.generation !== gl.generation) return null;
        return gl.gpuBusy ? gl.UNSIGNALED : gl.SIGNALED;
    },
    deleteSync() {},
    flush() {},
};

/** Frames requested and not yet run or cancelled. */
const frames = new Map<number, FrameRequestCallback>();
let frameId = 0;

/** DOM listeners on the window, the document and the canvas, still attached. */
let attached: Map<string, number>;

function options(over: Partial<SceneOptions> = {}): SceneOptions {
    return {
        canvas: document.createElement('canvas'),
        phone: false,
        still: false,
        onGolden: () => {},
        signal: new AbortController().signal,
        ...over,
    };
}

/** Everything recorded as made that was not released. */
const leaked = () => reg.created.filter((x) => !reg.released.has(x));

beforeEach(() => {
    reg.created.length = 0;
    reg.released.clear();
    reg.renderers.length = 0;
    reg.order.length = 0;
    reg.onRender = null;
    reg.onPaper = null;
    reg.meshes.length = 0;
    reg.pageScales.length = 0;
    reg.renders = 0;
    lose.mockClear();
    gl.gpuBusy = false;
    gl.lost = false;
    gl.noFences = false;
    gl.generation = 0;
    frames.clear();

    vi.stubGlobal(
        'Image',
        class {
            width = 4;
            height = 4;
            decoding = '';
            onload: (() => void) | null = null;
            onerror: (() => void) | null = null;
            set src(_: string) {
                queueMicrotask(() => this.onload?.());
            }
        },
    );
    vi.stubGlobal(
        'FontFace',
        class {
            load() {
                return Promise.resolve(this);
            }
        },
    );
    Object.defineProperty(document, 'fonts', { configurable: true, value: { add() {} } });
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
        frames.set(++frameId, cb);
        return frameId;
    });
    vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
    vi.spyOn(console, 'info').mockImplementation(() => {});

    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(((type: string) =>
        type === 'webgl2' ? gl : null) as never);

    attached = new Map();
    const key = (target: EventTarget, type: string) =>
        `${target === window ? 'window' : target === document ? 'document' : 'canvas'}:${type}`;
    const watched = (target: EventTarget) =>
        target === window || target === document || target instanceof HTMLCanvasElement;
    const add = EventTarget.prototype.addEventListener;
    const remove = EventTarget.prototype.removeEventListener;
    vi.spyOn(EventTarget.prototype, 'addEventListener').mockImplementation(function (
        this: EventTarget,
        type,
        fn,
        opts,
    ) {
        if (watched(this)) attached.set(key(this, type), (attached.get(key(this, type)) ?? 0) + 1);
        return add.call(this, type, fn, opts);
    });
    vi.spyOn(EventTarget.prototype, 'removeEventListener').mockImplementation(function (
        this: EventTarget,
        type,
        fn,
        opts,
    ) {
        if (watched(this)) attached.set(key(this, type), (attached.get(key(this, type)) ?? 0) - 1);
        return remove.call(this, type, fn, opts);
    });
});

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

/** Listeners the scene left behind: every add must have had its remove. */
const listenersLeft = () => [...attached].filter(([, n]) => n !== 0);

describe('createScene: a finished scene', () => {
    it('gives the renderer the context it acquired itself, with three’s own attributes', async () => {
        const o = options();
        const scene = await createScene(o);

        expect(HTMLCanvasElement.prototype.getContext).toHaveBeenCalledWith(
            'webgl2',
            CONTEXT_ATTRIBUTES,
        );
        expect(reg.renderers[0].params.context).toBe(gl);
        expect(reg.renderers[0].params.canvas).toBe(o.canvas);
        scene.dispose();
    });

    it('is animating once built', async () => {
        const scene = await createScene(options());

        expect(frames.size).toBe(1);
        scene.dispose();
    });

    it('releases everything it made on dispose, and gives the context back last', async () => {
        const scene = await createScene(options());
        expect(reg.created.length).toBeGreaterThan(50);

        scene.dispose();

        expect(leaked()).toEqual([]);
        expect(lose).toHaveBeenCalledTimes(1);
        expect(reg.order.filter((x) => x === 'renderer' || x === 'context')).toEqual([
            'renderer',
            'context',
        ]);
        expect(frames.size).toBe(0);
        expect(listenersLeft()).toEqual([]);
    });

    it('can be disposed twice without doing anything the second time', async () => {
        const scene = await createScene(options());
        scene.dispose();
        scene.dispose();

        expect(lose).toHaveBeenCalledTimes(1);
    });
});

describe('createScene: a scene that cannot be finished', () => {
    it('rejects quietly when the renderer’s context is refused, and makes nothing', async () => {
        vi.mocked(HTMLCanvasElement.prototype.getContext).mockReturnValue(null);
        const errors = vi.spyOn(console, 'error');

        await expect(createScene(options())).rejects.toThrow(/WebGL 2 context/);

        expect(reg.renderers).toHaveLength(0);
        expect(errors).not.toHaveBeenCalled();
        expect(reg.created).toEqual([]);
    });

    it('releases what was made when a page canvas fails half-way', async () => {
        let drawn = 0;
        reg.onPaper = () => {
            if (++drawn === 5) throw new Error('no 2D context under memory pressure');
        };

        await expect(createScene(options())).rejects.toThrow(/memory pressure/);

        expect(reg.created.length).toBeGreaterThan(5);
        expect(leaked()).toEqual([]);
        expect(lose).toHaveBeenCalledTimes(1);
        expect(listenersLeft()).toEqual([]);
        expect(frames.size).toBe(0);
    });

    it('releases a whole scene whose first still frame fails to render', async () => {
        reg.onRender = () => {
            throw new Error('shader did not compile');
        };

        await expect(createScene(options({ still: true }))).rejects.toThrow(/shader/);

        expect(leaked()).toEqual([]);
        expect(lose).toHaveBeenCalledTimes(1);
        expect(listenersLeft()).toEqual([]);
    });

    it('makes nothing for a screen that is already gone', async () => {
        const ctrl = new AbortController();
        const pending = createScene(options({ signal: ctrl.signal }));
        ctrl.abort();

        await expect(pending).rejects.toThrow();
        expect(reg.renderers).toHaveLength(0);
    });
});

describe('createScene: reduced motion', () => {
    it('draws one frame, asks for no other, and shows the far sheets where they belong', async () => {
        let farPlaced: boolean | null = null;
        reg.onRender = () => {
            const far = reg.created.find(
                (x): x is import('three').InstancedMesh =>
                    (x as { isInstancedMesh?: boolean }).isInstancedMesh === true,
            )!;
            const m = new Matrix4();
            let away = 0;
            for (let i = 0; i < far.count; i++) {
                far.getMatrixAt(i, m);
                if (!m.equals(new Matrix4())) away++;
            }
            farPlaced = away === far.count;
        };

        const scene = await createScene(options({ still: true }));

        expect(farPlaced).toBe(true);
        expect(frames.size).toBe(0);
        scene.dispose();
        expect(leaked()).toEqual([]);
    });
});

describe('createScene: the error page', () => {
    /**
     * Geometry lying on the right page by the gutter: the torn stub, its
     * fibre edge and its shadow, if any.
     */
    const tornParts = () =>
        reg.created.filter((x): x is BufferGeometry => {
            const g = x as BufferGeometry;
            if (!g.isBufferGeometry || !g.index) return false;
            const p = g.attributes.position;
            for (let i = 0; i < p.count; i++)
                if (p.getX(i) < -0.01 || p.getX(i) > 0.2 || p.getY(i) < 0.04) return false;
            return true;
        });

    it('leaves a torn stub at the gutter only on the error page', async () => {
        (await createScene(options())).dispose();
        expect(tornParts()).toHaveLength(0);

        const scene = await createScene(options({ torn: true }));
        // The stub itself, the paler fibre along its edge, the shadow it lays.
        expect(tornParts()).toHaveLength(3);
        scene.dispose();
        expect(leaked()).toEqual([]);
    });

    it('lays all of it on the page and under the turning sheet, so pages still turn over it', async () => {
        const scene = await createScene(options({ torn: true }));
        for (const part of tornParts()) {
            const p = part.attributes.position;
            for (let i = 0; i < p.count; i++) {
                const lift = p.getY(i) - blockHeight(p.getX(i));
                // Above the page it lies on (+0.003), below the turning sheet (+0.006).
                expect(lift).toBeGreaterThan(0.003);
                expect(lift).toBeLessThan(0.006);
            }
        }
        scene.dispose();
    });
});

describe('createScene: tearing and mending the live book', () => {
    /** Geometry lying on the right page by the gutter, as in the error page tests. */
    const tornParts = () =>
        reg.created.filter((x): x is BufferGeometry => {
            const g = x as BufferGeometry;
            if (!g.isBufferGeometry || !g.index) return false;
            const p = g.attributes.position;
            for (let i = 0; i < p.count; i++)
                if (p.getX(i) < -0.01 || p.getX(i) > 0.2 || p.getY(i) < 0.04) return false;
            return true;
        });
    /** Is the torn stub on show: every mesh of it and every ancestor visible? */
    const tornShown = () => {
        const parts = new Set<object>(tornParts());
        const meshes = reg.meshes.filter((m) => parts.has(m.geometry));
        if (meshes.length === 0) return false;
        return meshes.every((m) => {
            for (let o: import('three').Object3D | null = m; o; o = o.parent)
                if (!o.visible) return false;
            return true;
        });
    };

    it('tears a whole book on request, without building a new scene', async () => {
        const scene = await createScene(options());
        const renderers = reg.renderers.length;
        expect(tornShown()).toBe(false);

        scene.setTorn(true);

        expect(tornShown()).toBe(true);
        expect(reg.renderers).toHaveLength(renderers);
        scene.dispose();
        expect(leaked()).toEqual([]);
    });

    it('mends it again, and tears it again, reusing the same stub', async () => {
        const scene = await createScene(options({ torn: true }));
        const made = tornParts().length;

        scene.setTorn(false);
        expect(tornShown()).toBe(false);
        scene.setTorn(true);
        expect(tornShown()).toBe(true);

        expect(tornParts()).toHaveLength(made);
        scene.dispose();
        expect(leaked()).toEqual([]);
    });

    it('redraws the still frame when the book changes, since nothing else will', async () => {
        const scene = await createScene(options({ still: true }));
        const before = reg.renders;

        scene.setTorn(true);

        expect(reg.renders).toBe(before + 1);
        scene.dispose();
    });

    it('does nothing once disposed', async () => {
        const scene = await createScene(options());
        scene.dispose();
        const made = reg.created.length;

        scene.setTorn(true);

        expect(reg.created).toHaveLength(made);
    });
});

describe('createScene: fitting the device', () => {
    /** Run every frame scheduled now, at time `now`. */
    const tick = (now: number) => {
        const due = [...frames.values()];
        frames.clear();
        due.forEach((cb) => cb(now));
    };
    const pass = (kind: string) =>
        reg.created.find((x) => (x as { kind?: string }).kind === kind) as
            { enabled?: boolean } | undefined;

    it('keeps the full room on a device that holds sixty frames a second', async () => {
        const scene = await createScene(options());
        for (let i = 1; i <= 600; i++) tick(i * (1000 / 60));

        expect(scene.stats().level).toBe('full');
        expect(pass('gtao')?.enabled).not.toBe(false);
        expect(pass('bloom')?.enabled).not.toBe(false);
        scene.dispose();
    });

    it('steps down a slow device level by level and ends on a still frame', async () => {
        const levels: number[] = [];
        const scene = await createScene(options({ quality: { onChange: (l) => levels.push(l) } }));
        let now = 0;
        for (let i = 0; i < 20 && frames.size > 0; i++) tick((now += 3000));

        expect(scene.stats().level).toBe('still');
        // The room went through cheaper levels on the way down…
        expect(levels.at(-1)).toBe(STILL);
        expect(pass('gtao')?.enabled).toBe(false);
        expect(pass('bokeh')?.enabled).toBe(false);
        expect(pass('bloom')?.enabled).toBe(false);
        expect(reg.renderers[0].pixelRatio).toBeLessThanOrEqual(0.75);
        // …and stopped asking for frames: the last one stays on screen.
        expect(frames.size).toBe(0);
        scene.dispose();
        expect(leaked()).toEqual([]);
    });

    it('starts where the last room in this tab settled', async () => {
        const scene = await createScene(options({ quality: { start: STILL } }));

        expect(scene.stats().level).toBe('still');
        // A still room draws once and asks for nothing more.
        expect(reg.renders).toBe(1);
        expect(frames.size).toBe(0);
        scene.dispose();
    });

    it('never starts a phone above its own composition’s level', async () => {
        const scene = await createScene(options({ phone: true, quality: { start: 0 } }));

        expect(scene.stats().level).toBe(LEVELS[startLevel(true)].name);
        scene.dispose();
    });

    it('refreshes shadows less often on the lower levels', async () => {
        const full = await createScene(options({ quality: { start: 0 } }));
        expect(reg.renderers[0].shadowMap.autoUpdate).toBe(true);
        full.dispose();

        const low = await createScene(options({ quality: { start: LOWEST_MOVING } }));
        const shadowMap = reg.renderers[1].shadowMap;
        expect(shadowMap.autoUpdate).toBe(false);
        let updates = 0;
        for (let i = 1; i <= 30; i++) {
            shadowMap.needsUpdate = false;
            tick(i * (1000 / 60));
            if (shadowMap.needsUpdate) updates++;
        }
        expect(updates).toBeGreaterThan(5);
        expect(updates).toBeLessThan(20);
        low.dispose();
    });

    it('draws nothing for the frames after a hold, then carries on', async () => {
        const scene = await createScene(options());
        tick(16);
        const before = reg.renders;

        scene.hold();
        tick(33);
        tick(50);
        expect(reg.renders).toBe(before);

        tick(67);
        expect(reg.renders).toBe(before + 1);
        scene.dispose();
    });

    it('typesets smaller pages on a phone', async () => {
        (await createScene(options({ phone: true }))).dispose();
        expect(reg.pageScales.length).toBeGreaterThan(10);
        expect(new Set(reg.pageScales)).toEqual(new Set([0.5]));

        reg.pageScales.length = 0;
        (await createScene(options())).dispose();
        expect(new Set(reg.pageScales)).toEqual(new Set([1]));
    });

    it('lets the page breathe while it typesets: other tasks run before the room is ready', async () => {
        let ranBeforeReady: boolean | null = null;
        let ran = false;
        const pending = createScene(options());
        setTimeout(() => (ran = true), 0);
        const scene = await pending.then((s) => {
            ranBeforeReady = ran;
            return s;
        });

        expect(ranBeforeReady).toBe(true);
        scene.dispose();
    });

    it('reports what the debug overlay shows', async () => {
        const scene = await createScene(options());
        tick(16);
        tick(33);

        const stats = scene.stats();
        expect(LEVELS.map((l) => l.name)).toContain(stats.level);
        expect(stats.renderer).toBe('Fake GPU 9000');
        expect(stats.layout).toBe('desktop');
        expect(stats.buildMs).toBeGreaterThanOrEqual(0);
        expect(stats.pixelRatio).toBeGreaterThan(0);
        expect(stats.fps).toBeGreaterThan(0);
        scene.dispose();
    });
});

describe('createScene: the viewport changing under it', () => {
    const resize = (width: number, height: number) => {
        Object.defineProperty(window, 'innerWidth', { configurable: true, value: width });
        Object.defineProperty(window, 'innerHeight', { configurable: true, value: height });
        window.dispatchEvent(new Event('resize'));
    };
    const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

    it('does not reallocate for the URL bar sliding, and reallocates once for a real change', async () => {
        resize(390, 844);
        const scene = await createScene(options({ phone: true }));
        const renderer = reg.renderers[0];
        const before = renderer.sizes;

        // The URL bar, sliding in and out in a burst, and in the end gone.
        for (const h of [820, 800, 844, 800, 790]) resize(390, h);
        await wait(250);
        expect(renderer.sizes).toBe(before);

        // The keyboard: a large change, also in a burst — one resize for it.
        for (const h of [700, 560, 420]) resize(390, h);
        await wait(250);
        expect(renderer.sizes).toBe(before + 1);
        scene.dispose();
    });
});

describe('createScene: a device this tab has not seen', () => {
    const tick = (now: number) => {
        const due = [...frames.values()];
        frames.clear();
        due.forEach((cb) => cb(now));
    };

    it('starts below the full room and climbs to it while the canvas fades in', async () => {
        const scene = await createScene(options());
        expect(scene.stats().level).not.toBe('full');

        let now = 0;
        while (scene.stats().level !== 'full' && now < 3000) tick((now += 1000 / 60));

        expect(scene.stats().level).toBe('full');
        expect(now).toBeLessThan(1200);
        scene.dispose();
    });
});

describe('createScene: one frame in flight', () => {
    const tick = (now: number) => {
        const due = [...frames.values()];
        frames.clear();
        due.forEach((cb) => cb(now));
    };

    it('queues no new frame while the GPU is still busy with the last one', async () => {
        const scene = await createScene(options({ quality: { start: 0 } }));
        tick(16);
        const drawn = reg.renders;

        // The GPU falls behind: the main thread must not pile more work on
        // it — that backlog is what froze the page for seconds.
        gl.gpuBusy = true;
        for (let i = 2; i < 30; i++) tick(i * 16);
        expect(reg.renders).toBe(drawn);
        expect(frames.size).toBe(1);

        gl.gpuBusy = false;
        tick(30 * 16);
        expect(reg.renders).toBe(drawn + 1);
        scene.dispose();
    });

    it('judges the device by how long the GPU took, not by how fast frames were handed over', async () => {
        const levels: number[] = [];
        const scene = await createScene(
            options({ quality: { start: 0, onChange: (l) => levels.push(l) } }),
        );
        // Ticks every 16 ms, but the GPU finishes a frame only every 3 s:
        // the old interval measure saw sixty frames a second here.
        let now = 0;
        for (let frame = 0; frame < 6 && frames.size > 0; frame++) {
            gl.gpuBusy = false;
            tick((now += 16));
            gl.gpuBusy = true;
            for (let i = 0; i < 187; i++) tick((now += 16));
        }

        expect(levels.at(-1)).toBe(STILL);
        scene.dispose();
    });
});

describe('createScene: the first frame, prepared ahead', () => {
    it('compiles its programs and uploads every texture before drawing anything', async () => {
        let atFirstRender: { compiled: number; uploaded: number } | null = null;
        reg.onRender = () => {
            atFirstRender ??= { ...reg.renderers[0] };
        };
        const scene = await createScene(options({ still: true }));

        expect(atFirstRender!.compiled).toBe(1);
        const textures = reg.created.filter((x) => (x as { isTexture?: boolean }).isTexture);
        // Every texture the scene made, the environment's render target aside.
        expect(atFirstRender!.uploaded).toBeGreaterThanOrEqual(textures.length - 2);
        scene.dispose();
    });
});

describe('createScene: waiting for the GPU without blocking', () => {
    it('lets the GPU finish its heavy build steps before carrying on, without holding the page', async () => {
        // The GPU is busy (generating the environment map, say) for a while.
        gl.gpuBusy = true;
        let ready = false;
        const pending = createScene(options()).then((s) => {
            ready = true;
            return s;
        });

        // The page keeps getting turns meanwhile, and the scene is not ready.
        // Well past what the build itself needs to finish on its own.
        let turns = 0;
        for (let i = 0; i < 400; i++) {
            await new Promise((r) => setTimeout(r, 0));
            turns++;
        }
        expect(turns).toBe(400);
        expect(ready).toBe(false);

        gl.gpuBusy = false;
        const scene = await pending;
        expect(ready).toBe(true);
        scene.dispose();
        expect(leaked()).toEqual([]);
    });

    it('gives up, and releases everything, if the context is lost while it waits', async () => {
        gl.gpuBusy = true;
        const pending = createScene(options());
        await new Promise((r) => setTimeout(r, 30));

        // A lost context's fence never signals; its status reads null. Read
        // as "still busy", the build would wait for ever with the room half
        // made. It has to fail, so the screen keeps its static backdrop.
        gl.lost = true;

        await expect(pending).rejects.toThrow(/lost/);
        expect(leaked()).toEqual([]);
        expect(listenersLeft()).toEqual([]);
    });

    it('stops waiting, and releases everything, if the screen goes meanwhile', async () => {
        gl.gpuBusy = true;
        const ctrl = new AbortController();
        const pending = createScene(options({ signal: ctrl.signal }));
        await new Promise((r) => setTimeout(r, 30));

        ctrl.abort();

        await expect(pending).rejects.toThrow();
        expect(leaked()).toEqual([]);
        expect(lose).toHaveBeenCalledTimes(1);
    });
});

describe('createScene: no needless trips to the GPU', () => {
    it('sizes the drawing buffer before it typesets a single page', async () => {
        const scene = await createScene(options());

        // Resizing the canvas makes the browser finish all queued GPU work —
        // the page canvases' included, seconds of it on a slow GPU.
        expect(reg.order.indexOf('size')).toBeGreaterThanOrEqual(0);
        expect(reg.order.indexOf('size')).toBeLessThan(reg.order.indexOf('page'));
        scene.dispose();
    });

    it('never touches the drawing buffer again while nothing has changed', async () => {
        const scene = await createScene(options());

        expect(reg.renderers[0].sizes).toBe(1);
        scene.dispose();
    });
});

describe('createScene: page textures', () => {
    it('uploads each page as soon as it is drawn, so no upload waits on the pages after it', async () => {
        const scene = await createScene(options());

        // Between two pages there is always the first one's upload: the GPU
        // draws page canvases in order, and an upload made after all of them
        // would wait for every one.
        const pagesAndUploads = reg.order.filter((x) => x === 'page' || x === 'upload');
        const firstPage = pagesAndUploads.indexOf('page');
        const lastPage = pagesAndUploads.lastIndexOf('page');
        for (let i = firstPage; i < lastPage; i++)
            if (pagesAndUploads[i] === 'page') expect(pagesAndUploads[i + 1]).toBe('upload');
        scene.dispose();
    });
});

describe('createScene: typesetting without holding the page', () => {
    it('gives the page a turn between one page and the next', async () => {
        const turns = setInterval(() => reg.order.push('turn'), 0);
        const scene = await createScene(options());
        clearInterval(turns);

        const first = reg.order.indexOf('page');
        const last = reg.order.lastIndexOf('page');
        const between = reg.order.slice(first, last).filter((x) => x === 'turn').length;
        // Twenty-odd pages; drawn in one go there would be no turn at all.
        expect(between).toBeGreaterThan(5);
        scene.dispose();
    });
});

describe('createScene: the environment map first', () => {
    it('typesets no page while the GPU is still generating the environment map', async () => {
        gl.gpuBusy = true;
        const pending = createScene(options());
        for (let i = 0; i < 100; i++) await new Promise((r) => setTimeout(r, 0));

        // The pages wait: anything that syncs with the GPU meanwhile would
        // hold the page for as long as the GPU takes.
        expect(reg.order).not.toContain('page');

        gl.gpuBusy = false;
        (await pending).dispose();
        expect(reg.order).toContain('page');
    });
});

describe('createScene: a context that goes away, or has no fences', () => {
    const tick = (now: number) => {
        const due = [...frames.values()];
        frames.clear();
        due.forEach((cb) => cb(now));
    };

    it('drops the frame in flight when the context is lost, and draws again once it is restored', async () => {
        const o = options({ quality: { start: 0 } });
        const scene = await createScene(o);
        tick(16);
        gl.gpuBusy = true;
        tick(32);
        const drawn = reg.renders;

        gl.lost = true;
        for (let i = 3; i < 10; i++) tick(i * 16);
        expect(reg.renders).toBe(drawn);
        expect(frames.size).toBe(1);

        // Restored: the fence from before the loss means nothing now.
        gl.lost = false;
        o.canvas.dispatchEvent(new Event('webglcontextrestored'));
        tick(10 * 16);
        expect(reg.renders).toBe(drawn + 1);
        scene.dispose();
    });

    it('drops a fence that has stopped being one, rather than waiting on it for ever', async () => {
        const scene = await createScene(options({ quality: { start: 0 } }));
        tick(16);
        gl.gpuBusy = true;
        tick(32);
        const drawn = reg.renders;

        // The context went and came back between two frames, unseen: the
        // fence in flight now reads as nothing at all, not as busy.
        gl.generation++;
        tick(48);
        expect(reg.renders).toBe(drawn + 1);
        scene.dispose();
    });

    it('paces by animation frames and still judges the device when it cannot make fences', async () => {
        gl.noFences = true;
        const levels: number[] = [];
        const scene = await createScene(
            options({ quality: { start: 0, onChange: (l) => levels.push(l) } }),
        );

        // Every frame is drawn — nothing to wait on — and the governor reads
        // the frame interval instead: three seconds a frame is still too slow.
        let now = 0;
        for (let i = 0; i < 10 && frames.size > 0; i++) tick((now += 3000));

        expect(reg.renders).toBeGreaterThan(1);
        expect(levels.at(-1)).toBe(STILL);
        expect(scene.stats().buildMs).not.toBeNull();
        scene.dispose();
        expect(leaked()).toEqual([]);
    });
});
