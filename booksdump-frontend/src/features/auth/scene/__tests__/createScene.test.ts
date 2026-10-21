import { Matrix4, type BufferGeometry } from 'three';

import { blockHeight } from '@/features/auth/scene/book';

import createScene, {
    CONTEXT_ATTRIBUTES,
    type SceneOptions,
} from '@/features/auth/scene/createScene';

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
    renderers: [] as { params: Record<string, unknown>; dispose: () => void }[],
    order: [] as string[],
    /** Runs on every composer render; a test can make it throw or look around. */
    onRender: null as null | (() => void),
    /** Runs on every page canvas drawn; a test can make it throw. */
    onPaper: null as null | (() => void),
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
        shadowMap = { enabled: false, type: 0 };
        outputColorSpace = '';
        toneMapping = 0;
        toneMappingExposure = 1;
        constructor(params: Record<string, unknown>) {
            this.params = params;
            reg.renderers.push(this);
        }
        setPixelRatio() {}
        getPixelRatio() {
            return 1;
        }
        setSize() {}
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
vi.mock('three/examples/jsm/postprocessing/BokehPass.js', () => ({ BokehPass: fakePass }));
vi.mock('three/examples/jsm/postprocessing/UnrealBloomPass.js', () => ({
    UnrealBloomPass: fakePass,
}));
vi.mock('three/examples/jsm/postprocessing/GTAOPass.js', () => ({ GTAOPass: fakePass }));
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
vi.mock('@/features/auth/scene/textures', async (importOriginal) => {
    const real = await importOriginal<typeof import('@/features/auth/scene/textures')>();
    const blank = () => document.createElement('canvas');
    return {
        ...real,
        paperCanvas: () => {
            reg.onPaper?.();
            return blank();
        },
        edgeCanvas: blank,
        giltMaps: () => ({ metal: blank(), rough: blank() }),
    };
});

const lose = vi.fn(() => reg.order.push('context'));
const gl = {
    getExtension: (name: string) => (name === 'WEBGL_lose_context' ? { loseContext: lose } : null),
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
    lose.mockClear();
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
        expect(reg.order).toEqual(['renderer', 'context']);
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
