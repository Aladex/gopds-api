import {
    AdditiveBlending,
    AgXToneMapping,
    BackSide,
    Box3,
    BoxGeometry,
    BufferAttribute,
    BufferGeometry,
    CanvasTexture,
    ClampToEdgeWrapping,
    Color,
    ConeGeometry,
    DataTexture,
    DirectionalLight,
    DoubleSide,
    Euler,
    ExtrudeGeometry,
    Fog,
    FrontSide,
    Group,
    HemisphereLight,
    InstancedMesh,
    Material,
    Matrix4,
    Mesh,
    MeshBasicMaterial,
    MeshPhysicalMaterial,
    MeshStandardMaterial,
    NoColorSpace,
    PerspectiveCamera,
    PlaneGeometry,
    PMREMGenerator,
    PointLight,
    Points,
    Quaternion,
    Raycaster,
    RepeatWrapping,
    Scene,
    ShaderMaterial,
    Shape,
    SphereGeometry,
    SpotLight,
    SRGBColorSpace,
    Texture,
    Vector2,
    Vector3,
    VSMShadowMap,
    WebGLRenderer,
    type Object3D,
    type Side,
} from 'three';
import { RGBELoader } from 'three/examples/jsm/loaders/RGBELoader.js';
import { EffectComposer } from 'three/examples/jsm/postprocessing/EffectComposer.js';
import { RenderPass } from 'three/examples/jsm/postprocessing/RenderPass.js';
import { ShaderPass } from 'three/examples/jsm/postprocessing/ShaderPass.js';
import { OutputPass } from 'three/examples/jsm/postprocessing/OutputPass.js';
import { BokehPass } from 'three/examples/jsm/postprocessing/BokehPass.js';
import { UnrealBloomPass } from 'three/examples/jsm/postprocessing/UnrealBloomPass.js';
import { GTAOPass } from 'three/examples/jsm/postprocessing/GTAOPass.js';
import type { Pass } from 'three/examples/jsm/postprocessing/Pass.js';

import {
    FLIP_S,
    FLIP_Z,
    TEAR_VELOCITY,
    blockHeight,
    easeTurn,
    finishTurn,
    layoutFlip,
    newBookState,
    pickSpare,
    startTurn,
    tearHandOff,
    tornStub,
} from './book';
import {
    CAMERA_Y,
    CAMERA_Z,
    PAGE_H,
    PAGE_W,
    SOFT_DIST,
    TABLE_Y,
    halfExtentAt,
    sceneLayout,
} from './layout';
import { createLoop, createOwner, listenerBag } from './lifecycle';
import { LEVELS, createGovernor, startLevel } from './quality';
import { needsResize } from './resize';
import {
    deform,
    makeGust,
    placeSheets,
    separate,
    stepSwarm,
    type Gust,
    type Sheet,
    type View,
} from './physics';
import { GOLDEN_FOLIO, GOLDEN_POST, PASSAGES, folioOf, type Passage } from './text';
import { edgeCanvas, giltMaps, paperCanvas, type PrintSources, type SideOptions } from './textures';
import logoMeta from './logo_meta.json';
import ptSerifRegular from './fonts/PTSerif-Regular-subset.woff2?url';
import ptSerifBold from './fonts/PTSerif-Bold-subset.woff2?url';
import ptSerifItalic from './fonts/PTSerif-Italic-subset.woff2?url';

/**
 * The reading room behind the sign-in screens: a dark table, an open book
 * turning its pages in a shaft of light, loose sheets drifting in the air the
 * cursor moves, and the Leprosorium emblem propped on two closed books.
 *
 * This module is the only one that talks to WebGL, and it is loaded on its own
 * after the screen is already usable: the form never waits for a GPU. What it
 * owns it disposes — renderer, post-processing, geometry, materials, textures,
 * listeners and the loop — because every screen in the frame mounts it afresh.
 *
 * The behaviour lives in the pure modules beside it (layout, book, physics,
 * typeset), which are tested without a GPU; this file places, draws and wires.
 */

export interface SceneOptions {
    canvas: HTMLCanvasElement;
    /** Portrait-phone composition and the lighter pipeline. */
    phone: boolean;
    /** The ultrawide composition (desktop only; see sceneLayout). */
    wide?: boolean;
    /** prefers-reduced-motion: render one frame and no loop. */
    still: boolean;
    /**
     * The error page: the open book has its right-hand page torn out, a
     * ragged stub left at the gutter. Only the starting state; the live
     * scene changes it with setTorn().
     */
    torn?: boolean;
    /** The golden post was caught. */
    onGolden: () => void;
    signal: AbortSignal;
    /**
     * Where on the quality ladder to start — the level the last room in this
     * tab settled on, or none for a device not seen yet, which is then probed
     * from below — and who to tell when it moves. A phone never starts above
     * its own composition's level.
     */
    quality?: { start?: number; onChange?: (level: number) => void };
    /**
     * Recording for the video the app plays (see tools/auth-room): a fixed
     * quality level, no loop, no governor, no fence pacing — the recorder
     * advances the scene a fixed step at a time with advance() and reads the
     * drawing buffer back, so it is preserved. Pages at full size on a phone
     * too, no lamp following a cursor, and the film grain only if asked for:
     * per-frame noise is the most expensive thing a video encoder can be
     * given, and the app lays a grain of its own over the video instead.
     */
    record?: { level: number; grain?: boolean };
}

/** A scripted breath of air for a recording: where the "hand" is and how fast it moves. */
export interface RecordGust {
    ndc: [number, number];
    vel: [number, number];
}

/** Where the overlay's Easter eggs sit in the recorded frame, in its pixels. */
export interface SceneMarkers {
    width: number;
    height: number;
    /** The golden post lying on the table: its four corners. */
    goldenPost: [number, number][];
    /** The mask's two pupils, left then right. */
    pupils: [number, number][];
    /** The mask's bounding box. */
    mask: { x: number; y: number; w: number; h: number };
}

/** What the opt-in diagnostics overlay (?roomdebug=1) shows. */
export interface SceneStats {
    fps: number;
    /** Name of the quality level. */
    level: string;
    /** From asking for the assets to the first frame drawn; null until then. */
    buildMs: number | null;
    /**
     * Where the build went, in milliseconds: fetching and decoding the
     * assets, typesetting the pages, and the rest up to the first frame —
     * geometry, post-processing, shader compiles and uploads.
     */
    phases: { assets: number; pages: number; gpu: number | null };
    renderer: string;
    pixelRatio: number;
    canvas: string;
    /** Which composition the scene was built for. */
    layout: 'phone' | 'desktop' | 'ultrawide';
}

export interface SceneHandle {
    dispose(): void;
    /**
     * Tear the right-hand page out of the open book, or put it back, on the
     * live scene: the room is shared by every public screen and only the
     * error page shows the torn book.
     */
    setTorn(torn: boolean): void;
    /**
     * Draw nothing for the next couple of frames, so a screen the reader has
     * just opened paints before the scene takes the main thread again.
     */
    hold(): void;
    stats(): SceneStats;
    /** Recording only: one step of the scene at time `now` (ms), drawn. */
    advance(now: number, gust: RecordGust | null): void;
    /** Recording only: where the Easter eggs are in the frame. */
    markers(): SceneMarkers;
}

/** Give the rest of the page a turn: input, layout and paint run between our tasks. */
const nextTask = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

/** Resize events arrive in bursts — the URL bar, the keyboard sliding in. */
const RESIZE_DEBOUNCE_MS = 150;

const ASSETS = '/auth-scene/';

/**
 * The context attributes three.js r170 asks for when it makes the context
 * itself. The scene makes it instead, so that a refusal is a quiet rejection
 * here rather than an error three prints on the console before throwing.
 */
export const CONTEXT_ATTRIBUTES: WebGLContextAttributes = {
    alpha: true,
    depth: true,
    stencil: false,
    antialias: true,
    premultipliedAlpha: true,
    preserveDrawingBuffer: false,
    powerPreference: 'default',
    failIfMajorPerformanceCaveat: false,
};

// Decoded sources survive a remount: going from login to registration should
// not decode the same pictures again. Textures, which belong to one renderer,
// are made fresh from them every time.
const images = new Map<string, Promise<HTMLImageElement>>();
function loadImage(name: string): Promise<HTMLImageElement> {
    let p = images.get(name);
    if (!p) {
        p = new Promise<HTMLImageElement>((resolve, reject) => {
            const img = new Image();
            img.decoding = 'async';
            img.onload = () => resolve(img);
            img.onerror = () => reject(new Error(`auth scene: ${name} did not load`));
            img.src = ASSETS + name;
        });
        p.catch(() => images.delete(name));
        images.set(name, p);
    }
    return p;
}

let environment: Promise<DataTexture> | null = null;
function loadEnvironment(): Promise<DataTexture> {
    if (!environment) {
        environment = new RGBELoader().loadAsync(ASSETS + 'room.hdr');
        environment.catch(() => (environment = null));
    }
    return environment;
}

// PT Serif is drawn into canvases, which only see a face the document has
// loaded. Added once per page; the faces are small and every mount wants them.
let fonts: Promise<void> | null = null;
function loadFonts(): Promise<void> {
    if (!fonts) {
        const faces = [
            new FontFace('PT Serif', `url(${ptSerifRegular})`, { weight: '400' }),
            new FontFace('PT Serif', `url(${ptSerifBold})`, { weight: '700' }),
            new FontFace('PT Serif', `url(${ptSerifItalic})`, { style: 'italic' }),
        ];
        fonts = Promise.all(faces.map((f) => f.load())).then((loaded) => {
            loaded.forEach((f) => document.fonts.add(f));
        });
        fonts.catch(() => (fonts = null));
    }
    return fonts;
}

let announced = false;

/** Throws if WebGL cannot be had; resolves once the first frame is on screen. */
export default async function createScene(o: SceneOptions): Promise<SceneHandle> {
    const buildStart = performance.now();
    const [
        tableColorImg,
        tableNormalImg,
        tableRoughImg,
        leatherColorImg,
        leatherNormalImg,
        leatherRoughImg,
        paperNormalImg,
        paperRoughImg,
        paperImg,
        devitoImg,
        logoColorImg,
        logoMaskImg,
        logoMaskAlphaImg,
        logoMaskGlowImg,
        logoStampImg,
        envHdr,
    ] = await Promise.all([
        loadImage('table_color.webp'),
        loadImage('table_normal.webp'),
        loadImage('table_rough.webp'),
        loadImage('leather_color.webp'),
        loadImage('leather_normal.webp'),
        loadImage('leather_rough.webp'),
        loadImage('paper_normal.webp'),
        loadImage('paper_rough.webp'),
        loadImage('paper_color.webp'),
        loadImage('devito_plate.webp'),
        // The Leprosorium logo, prepared from the real artwork: the cut-out,
        // the mask crop, its silhouette, the glowing pupils, the one-colour mark.
        loadImage('logo_color.webp'),
        loadImage('logo_mask.webp'),
        loadImage('logo_mask_alpha.png'),
        loadImage('logo_mask_glow.webp'),
        loadImage('logo_stamp.webp'),
        loadEnvironment(),
        loadFonts(),
    ]);
    if (o.signal.aborted) throw new DOMException('unmounted', 'AbortError');
    const assetsAt = performance.now();

    // Everything from here on belongs to this owner, which exists before the
    // first allocation: whether construction finishes or fails half-way, one
    // call releases exactly what was made.
    const owner = createOwner();
    const own = <T extends { dispose(): void }>(resource: T): T => owner.track(resource);
    try {
        const layout = sceneLayout(o.phone, Boolean(o.wide));
        const { canvas } = o;
        // The probe in AuthScene only says a context could be had a moment ago;
        // this is the one the renderer will use. Asked for here, a refusal is an
        // ordinary rejection — the backdrop stays — instead of three's own
        // console error.
        const gl = canvas.getContext('webgl2', {
            ...CONTEXT_ATTRIBUTES,
            // A recording reads every frame back.
            preserveDrawingBuffer: Boolean(o.record),
        });
        if (!gl) throw new Error('auth scene: no WebGL 2 context for the renderer');
        // Registered first, so it runs last: the context goes back to the browser
        // after everything made with it is released. By then renderer.dispose()
        // has removed three's own context-lost handler, so nothing is logged.
        owner.add(() => gl.getExtension('WEBGL_lose_context')?.loseContext());
        const renderer = own(new WebGLRenderer({ canvas, context: gl, antialias: true }));

        // Wait until the GPU has finished everything handed to it so far,
        // without blocking: a fence, checked between tasks. Anything that
        // would otherwise sync with the GPU — a resize, a read — then finds
        // it idle instead of holding the main thread until it is.
        //
        // A fence has three answers, not two. Signalled, unsignalled, and —
        // once the context is lost — nothing at all: read as "still busy",
        // that waited for ever with the room half built. A lost context fails
        // the build instead, through the owner, and the screen keeps its
        // static backdrop.
        //
        // Some contexts cannot make fences at all. Then there is nothing to
        // wait on here, and the frame loop paces itself by animation frames
        // and judges the device by their intervals (see step).
        let fenceless = false;
        const lostError = () => new Error('auth scene: the WebGL context was lost');
        const gpuDone = async () => {
            if (gl.isContextLost()) throw lostError();
            const sync = gl.fenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0);
            if (!sync) {
                fenceless = true;
                return;
            }
            gl.flush();
            try {
                for (;;) {
                    const status = gl.getSyncParameter(sync, gl.SYNC_STATUS);
                    if (status === gl.SIGNALED) return;
                    if (status !== gl.UNSIGNALED || gl.isContextLost()) throw lostError();
                    await new Promise((resolve) => setTimeout(resolve, 16));
                    if (o.signal.aborted) throw new DOMException('unmounted', 'AbortError');
                }
            } finally {
                if (!gl.isContextLost()) gl.deleteSync(sync);
            }
        };
        const bag = listenerBag();
        owner.add(() => bag.clear());

        // A device this tab has not seen yet is probed from below; one it has
        // starts where the last room settled. Decided before anything is sized,
        // so the canvas starts at its level's pixel ratio.
        const top = startLevel(o.phone);
        const remembered = o.quality?.start;
        const governor = o.record
            ? createGovernor(o.record.level, { top: o.record.level })
            : createGovernor(Math.max(top, remembered ?? top), {
                  top,
                  probe: remembered === undefined,
              });
        renderer.setPixelRatio(
            Math.min(
                devicePixelRatio,
                layout.pixelRatioCap,
                LEVELS[governor.level].pixelRatio ?? Infinity,
            ),
        );
        // Sized now, before a single page is typeset. Resizing the canvas makes
        // the browser finish all GPU work queued so far — the page canvases'
        // drawing included, which a slow GPU takes seconds over — and holds the
        // main thread until it has.
        renderer.setSize(innerWidth, innerHeight, false);
        renderer.outputColorSpace = SRGBColorSpace;
        renderer.toneMapping = AgXToneMapping;
        renderer.toneMappingExposure = 1.15;
        renderer.shadowMap.enabled = true;
        renderer.shadowMap.type = VSMShadowMap;
        const maxAniso = renderer.capabilities.getMaxAnisotropy();

        // Every texture this scene makes, to upload ahead of the first frame.
        const uploads: Texture[] = [];
        const texture = (
            img: HTMLImageElement,
            srgb: boolean,
            repeat?: [number, number],
        ): Texture => {
            const t = new Texture(img);
            if (srgb) t.colorSpace = SRGBColorSpace;
            t.wrapS = t.wrapT = repeat ? RepeatWrapping : ClampToEdgeWrapping;
            if (repeat) t.repeat.set(repeat[0], repeat[1]);
            t.needsUpdate = true;
            own(t);
            uploads.push(t);
            return t;
        };
        const canvasTexture = (c: HTMLCanvasElement, srgb = true): CanvasTexture => {
            const t = new CanvasTexture(c);
            t.colorSpace = srgb ? SRGBColorSpace : NoColorSpace;
            t.anisotropy = maxAniso;
            // Every canvas here is opaque, and a 2D canvas keeps its pixels
            // premultiplied; saying so spares the upload a conversion of each.
            t.premultiplyAlpha = true;
            own(t);
            uploads.push(t);
            // Uploaded straight away. The browser draws page canvases on the
            // GPU, in order, and uploading one waits for its drawing to finish;
            // done now, that is this page's drawing alone, and the next page
            // starts in a task of its own. Left to the first frame, the first
            // upload would wait for every page at once.
            renderer.initTexture(t);
            return t;
        };

        const tableColor = texture(tableColorImg, true, [9, 6]);
        const tableNormal = texture(tableNormalImg, false, [9, 6]);
        const tableRough = texture(tableRoughImg, false, [9, 6]);
        const leatherColor = texture(leatherColorImg, true, [1.5, 1]);
        const leatherNormal = texture(leatherNormalImg, false, [1.5, 1]);
        const leatherRough = texture(leatherRoughImg, false, [1.5, 1]);
        const paperNormal = texture(paperNormalImg, false, [1, 1]);
        const paperRough = texture(paperRoughImg, false, [1, 1]);
        // The paper scan is landscape; turn it to run along the portrait page.
        for (const t of [paperNormal, paperRough]) {
            t.center.set(0.5, 0.5);
            t.rotation = Math.PI / 2;
        }
        const logoColor = texture(logoColorImg, true);
        const logoMask = texture(logoMaskImg, true);
        const logoMaskAlpha = texture(logoMaskAlphaImg, false);
        const logoMaskGlow = texture(logoMaskGlowImg, true);
        const print: PrintSources = { paper: paperImg, devito: devitoImg, stamp: logoStampImg };

        const BG = 0x120e0b;
        const scene = new Scene();
        // A last sweep over whatever is in the scene graph at the end, for
        // anything made without passing through own().
        owner.add(() => disposeGraph(scene));
        scene.background = new Color(BG);
        scene.fog = new Fog(BG, 10, 34);

        // Bounce light from a real room, so surfaces pick up soft warm reflections
        // instead of flat fill.
        {
            // The decoded HDR stays cached for the next mount; only its upload to
            // this renderer goes.
            owner.add(() => envHdr.dispose());
            const pmrem = own(new PMREMGenerator(renderer));
            const env = own(pmrem.fromEquirectangular(envHdr));
            scene.environment = env.texture;
            scene.environmentIntensity = 0.22;
            envHdr.dispose();
            pmrem.dispose();
        }
        // The environment map is a few dozen render passes of blur. Let the
        // GPU get through them before anything forces a sync: the next canvas
        // resize would otherwise wait for all of it with the page frozen —
        // seven seconds on a software renderer.
        await gpuDone();

        const camera = new PerspectiveCamera(layout.fov, 1, 0.1, 100);
        camera.position.set(0, CAMERA_Y, CAMERA_Z);
        camera.lookAt(0, layout.lookAtY, 0);
        const view: View = {
            halfExtent: (z) => halfExtentAt(z, camera.fov, camera.aspect),
            cameraY: CAMERA_Y,
        };

        // --- Lights -------------------------------------------------------------
        scene.add(new HemisphereLight(0xffe2bd, 0x1a110a, 0.2));

        const key = new DirectionalLight(0xffd6a0, 0.55);
        key.position.set(5, 10, 7);
        key.target.position.set(0, TABLE_Y, 0);
        key.castShadow = true;
        key.shadow.mapSize.setScalar(layout.shadowMapSize);
        Object.assign(key.shadow.camera, {
            left: -11,
            right: 11,
            top: 9,
            bottom: -9,
            near: 1,
            far: 40,
        });
        key.shadow.radius = 10;
        key.shadow.blurSamples = 16;
        key.shadow.bias = -0.0005;
        scene.add(key, key.target);

        // The shaft: a spot coming from high up on the left onto the open book.
        const BEAM_FROM = new Vector3(-6.8, 8.6, -1.8);
        const BEAM_ANGLE = 0.34;
        const beamLight = new SpotLight(0xffd29a, 5.5, 0, BEAM_ANGLE, 0.65, 0);
        beamLight.position.copy(BEAM_FROM);
        beamLight.target.position.copy(layout.bookPos);
        beamLight.castShadow = true;
        beamLight.shadow.mapSize.setScalar(layout.shadowMapSize);
        beamLight.shadow.radius = 5;
        beamLight.shadow.blurSamples = 16;
        beamLight.shadow.bias = -0.0005;
        scene.add(beamLight, beamLight.target);

        // A warm glow that follows the hand.
        const lamp = new PointLight(0xffc27a, 0, 0, 1.6);
        scene.add(lamp);

        const beamAxis = new Vector3().subVectors(layout.bookPos, BEAM_FROM).normalize();
        const beamLen = BEAM_FROM.distanceTo(layout.bookPos) + 1.5;

        // Visible shaft: an open cone, brighter where we look across it, fading
        // toward the floor.
        let beamCone: Mesh;
        {
            const r = Math.tan(BEAM_ANGLE * 0.9) * beamLen;
            const geo = own(new ConeGeometry(r, beamLen, 64, 1, true));
            const mat = own(
                new ShaderMaterial({
                    transparent: true,
                    depthWrite: false,
                    blending: AdditiveBlending,
                    side: DoubleSide,
                    uniforms: { uFloor: { value: TABLE_Y }, uStrength: { value: 0.075 } },
                    vertexShader: `
                varying vec3 vW; varying vec3 vN; varying float vT;
                void main() {
                    vT = uv.y;
                    vW = (modelMatrix * vec4(position, 1.0)).xyz;
                    vN = normalize(mat3(modelMatrix) * normal);
                    gl_Position = projectionMatrix * viewMatrix * vec4(vW, 1.0);
                }`,
                    fragmentShader: `
                uniform float uFloor; uniform float uStrength;
                varying vec3 vW; varying vec3 vN; varying float vT;
                void main() {
                    vec3 V = normalize(cameraPosition - vW);
                    float f = pow(abs(dot(normalize(vN), V)), 2.5);
                    float a = f * mix(0.2, 1.0, vT) * smoothstep(uFloor, uFloor + 1.4, vW.y) * uStrength;
                    gl_FragColor = vec4(1.0, 0.78, 0.5, a);
                }`,
                }),
            );
            beamCone = new Mesh(geo, mat);
            beamCone.position.copy(BEAM_FROM).addScaledVector(beamAxis, beamLen / 2);
            beamCone.quaternion.setFromUnitVectors(new Vector3(0, 1, 0), beamAxis.clone().negate());
            beamCone.renderOrder = 10;
            scene.add(beamCone);
        }

        // --- Table --------------------------------------------------------------
        {
            for (const t of [tableColor, tableNormal, tableRough]) t.anisotropy = maxAniso;
            const table = new Mesh(
                own(new PlaneGeometry(60, 40)),
                own(
                    new MeshStandardMaterial({
                        map: tableColor,
                        normalMap: tableNormal,
                        roughnessMap: tableRough,
                        roughness: 1,
                        metalness: 0,
                        color: 0x8f8070,
                    }),
                ),
            );
            table.rotation.x = -Math.PI / 2;
            table.position.set(0, TABLE_Y, -8);
            table.receiveShadow = true;
            scene.add(table);
        }

        // --- Paper materials ----------------------------------------------------
        const paperMat = (map: Texture, side: Side) =>
            own(
                new MeshStandardMaterial({
                    map,
                    normalMap: paperNormal,
                    normalScale: new Vector2(0.45, 0.45),
                    roughnessMap: paperRough,
                    roughness: 1,
                    metalness: 0,
                    side,
                    shadowSide: DoubleSide,
                }),
            );
        interface Leaf {
            front: Material;
            back: Material;
            /** The back's content lying face up, for the left page of the open book. */
            verso: Material;
        }
        // Typesetting the pages is the costliest thing the build does on the
        // main thread — two dozen full pages of text — so they are drawn one
        // at a time with a turn for the rest of the page in between: the form
        // stays responsive while the room is being built. A phone draws them at
        // half size; at the size a page is shown there, nobody can tell.
        const pageScale = o.phone && !o.record ? 0.5 : 1;
        const pageCanvas = async (
            passage: Passage,
            folio: number,
            isBack: boolean,
            mirror: boolean,
            sideOpts: SideOptions = {},
        ) => {
            await nextTask();
            if (o.signal.aborted) throw new DOMException('unmounted', 'AbortError');
            return paperCanvas(print, passage, folio, isBack, mirror, sideOpts, pageScale);
        };
        const materials: Leaf[] = [];
        for (const [i, passage] of PASSAGES.entries()) {
            // The library stamp sits on the verso of the first passage, which is
            // the left page of the open book.
            const stamp = i === 0;
            const folio = folioOf(i);
            materials.push({
                front: paperMat(
                    canvasTexture(await pageCanvas(passage, folio, false, false)),
                    FrontSide,
                ),
                back: paperMat(
                    canvasTexture(await pageCanvas(passage, folio, true, true, { stamp })),
                    BackSide,
                ),
                verso: paperMat(
                    canvasTexture(await pageCanvas(passage, folio, true, false, { stamp })),
                    FrontSide,
                ),
            });
        }
        // The golden post: the one sheet worth an invite.
        const gilt = giltMaps(pageScale);
        const metal = canvasTexture(gilt.metal, false);
        const rough = canvasTexture(gilt.rough, false);
        const giltMat = (c: HTMLCanvasElement, side: Side) =>
            own(
                new MeshStandardMaterial({
                    map: canvasTexture(c),
                    metalnessMap: metal,
                    roughnessMap: rough,
                    metalness: 1,
                    roughness: 1,
                    envMapIntensity: 4,
                    normalMap: paperNormal,
                    normalScale: new Vector2(0.45, 0.45),
                    side,
                    shadowSide: DoubleSide,
                }),
            );
        const gold = {
            front: giltMat(
                await pageCanvas(GOLDEN_POST, GOLDEN_FOLIO, false, false, { gilt: true }),
                FrontSide,
            ),
            back: giltMat(
                await pageCanvas(GOLDEN_POST, GOLDEN_FOLIO, true, true, { gilt: true }),
                BackSide,
            ),
        };
        // Plates were printed on one side only: a blank verso, nothing showing through.
        const plateSide = async (isBack: boolean, mirror: boolean, side: Side) =>
            paperMat(
                canvasTexture(
                    await pageCanvas(PASSAGES[2], folioOf(2), isBack, mirror, {
                        plate: true,
                        blank: isBack,
                    }),
                ),
                side,
            );
        const plate: Leaf = {
            front: await plateSide(false, false, FrontSide),
            back: await plateSide(true, true, BackSide),
            verso: await plateSide(true, false, FrontSide),
        };
        const pagesAt = performance.now();

        // --- Open book ----------------------------------------------------------
        const book = new Group();
        book.position.copy(layout.bookPos);
        book.rotation.y = layout.bookYaw;
        scene.add(book);

        const cover = new Mesh(
            own(new BoxGeometry(2.14, 0.04, PAGE_H + 0.1)),
            own(
                new MeshStandardMaterial({
                    map: leatherColor,
                    normalMap: leatherNormal,
                    roughnessMap: leatherRough,
                    roughness: 1,
                    color: 0x9a7a66,
                }),
            ),
        );
        cover.position.y = 0.02;
        cover.castShadow = cover.receiveShadow = true;
        book.add(cover);

        // Page blocks: the arched profile extruded along the spine.
        {
            const edgeTex = canvasTexture(edgeCanvas());
            edgeTex.wrapS = edgeTex.wrapT = RepeatWrapping;
            // Side UVs are in world units: a block ~0.15 tall is a few hundred sheet edges.
            edgeTex.repeat.set(3, 1);
            const edgeMat = own(
                new MeshStandardMaterial({ map: edgeTex, roughness: 1, side: DoubleSide }),
            );
            for (const side of [1, -1]) {
                const shape = new Shape();
                shape.moveTo(0, 0.04);
                for (let i = 0; i <= 24; i++) {
                    const s = i / 24;
                    shape.lineTo(s * side, blockHeight(s));
                }
                shape.lineTo(side, 0.04);
                shape.closePath();
                const block = new Mesh(
                    own(new ExtrudeGeometry(shape, { depth: PAGE_H, bevelEnabled: false })),
                    edgeMat,
                );
                block.position.z = -PAGE_H / 2;
                block.castShadow = block.receiveShadow = true;
                book.add(block);
            }
        }

        // Top pages lying on the blocks.
        const surface = (side: number) => {
            const geo = own(new PlaneGeometry(PAGE_W, PAGE_H, FLIP_S, 4));
            const pos = geo.attributes.position.array as Float32Array;
            for (let i = 0; i < pos.length; i += 3) {
                const x = pos[i] + 0.5 * side;
                const y = pos[i + 1];
                pos[i] = x;
                pos[i + 1] = blockHeight(Math.abs(x)) + 0.003;
                pos[i + 2] = -y;
            }
            geo.computeVertexNormals();
            const m = new Mesh(geo);
            m.receiveShadow = true;
            book.add(m);
            return m;
        };
        const rightTop = surface(1);
        const leftTop = surface(-1);

        // The error page's book: what is left of a page torn out, a ragged
        // strip at the gutter on the right. It is laid row by row along the
        // block's curve — a flat triangulation would cut under the arched
        // page — and kept below the turning sheet, which lifts off at +0.006,
        // so pages still turn over it.
        // Built the first time it is wanted, then only shown or hidden: the
        // reader can go from the error page to the sign-in screen and back
        // without the scene being rebuilt.
        let tornPage: Group | null = null;
        const buildTornPage = (): Group => {
            const page = new Group();
            book.add(page);
            const edge = tornStub().slice(1, -1);
            const K = 6;
            const pos: number[] = [];
            const uv: number[] = [];
            const index: number[] = [];
            edge.forEach(([xe, z], i) => {
                for (let k = 0; k <= K; k++) {
                    const x = (k / K) * xe;
                    // The free edge lifts a hair, as a torn edge does.
                    pos.push(x, blockHeight(x) + 0.0042 + 0.0012 * (k / K) ** 2, z);
                    // Same mapping as the right page: the stub keeps the
                    // margin and the starts of the lines it was torn from.
                    uv.push(x / PAGE_W, 0.5 - z / PAGE_H);
                }
                if (i === 0) return;
                for (let k = 0; k < K; k++) {
                    const a = (i - 1) * (K + 1) + k;
                    const b = a + K + 1;
                    index.push(a, b, a + 1, a + 1, b, b + 1);
                }
            });
            const stubGeo = own(new BufferGeometry());
            stubGeo.setAttribute('position', new BufferAttribute(new Float32Array(pos), 3));
            stubGeo.setAttribute('uv', new BufferAttribute(new Float32Array(uv), 2));
            stubGeo.setIndex(index);
            stubGeo.computeVertexNormals();
            const stub = new Mesh(stubGeo, materials[3].front);
            stub.castShadow = stub.receiveShadow = true;
            page.add(stub);

            // Cream on cream, a stub alone does not read as torn at the size the
            // book is drawn. What the eye takes a tear by is its edge: the paler
            // fibre a tear exposes, and the soft shadow the lifted edge lays on
            // the page beneath. Both are thin bands following the ragged line,
            // and both stay under the turning sheet.
            const band = (from: number, to: number, lift: number, alpha: [number, number]) => {
                const pos: number[] = [];
                const rgba: number[] = [];
                const index: number[] = [];
                edge.forEach(([xe, z], i) => {
                    for (const [k, x] of [from, to].map((d, k) => [k, xe + d] as const)) {
                        pos.push(x, blockHeight(x) + lift, z);
                        rgba.push(1, 1, 1, alpha[k]);
                    }
                    if (i > 0) {
                        const a = (i - 1) * 2;
                        index.push(a, a + 2, a + 1, a + 1, a + 2, a + 3);
                    }
                });
                const geo = own(new BufferGeometry());
                geo.setAttribute('position', new BufferAttribute(new Float32Array(pos), 3));
                geo.setAttribute('color', new BufferAttribute(new Float32Array(rgba), 4));
                geo.setIndex(index);
                geo.computeVertexNormals();
                return geo;
            };
            const shadow = new Mesh(
                band(-0.002, 0.014, 0.0035, [0.5, 0]),
                own(
                    new MeshBasicMaterial({
                        color: 0x000000,
                        vertexColors: true,
                        transparent: true,
                        depthWrite: false,
                    }),
                ),
            );
            const fibre = new Mesh(
                band(-0.0035, 0.0005, 0.0046, [1, 1]),
                own(
                    new MeshStandardMaterial({ color: 0xfbf3e2, vertexColors: true, roughness: 1 }),
                ),
            );
            page.add(shadow, fibre);
            return page;
        };
        if (o.torn) tornPage = buildTornPage();

        const flipGeo = own(new PlaneGeometry(PAGE_W, PAGE_H, FLIP_S, FLIP_Z));
        const flipPos = flipGeo.attributes.position.array as Float32Array;
        const flipMesh = new Group();
        {
            const f = new Mesh(flipGeo);
            const b = new Mesh(flipGeo);
            f.castShadow = true;
            f.receiveShadow = b.receiveShadow = true;
            flipMesh.add(f, b);
        }
        flipMesh.visible = false;
        book.add(flipMesh);
        book.updateMatrixWorld(true);

        // Pages of the open book, in order. It opens on the stamped verso (left)
        // facing the plate (right), so both are in plain sight from the first frame.
        const bookSeq: Leaf[] = [materials[0], plate, ...materials.slice(1)];
        const bookState = newBookState();
        const leafAt = (k: number) => bookSeq[k % bookSeq.length];
        const setBookPages = () => {
            rightTop.material = leafAt(bookState.page).front;
            leftTop.material = leafAt(bookState.page - 1 + bookSeq.length).verso;
        };
        setBookPages();

        const setFlip = (p: number) => {
            layoutFlip(flipPos, p);
            flipGeo.attributes.position.needsUpdate = true;
            flipGeo.computeVertexNormals();
        };

        const startFlip = (fast: boolean) => {
            const leaf = leafAt(bookState.page);
            // The plate stays bound in: it already flies as a sheet of its own.
            startTurn(bookState, fast, leaf !== plate);
            (flipMesh.children[0] as Mesh).material = leaf.front;
            (flipMesh.children[1] as Mesh).material = leaf.back;
            rightTop.material = leafAt(bookState.page + 1).front;
            setFlip(0);
            flipMesh.visible = true;
        };

        // --- Loose pages on the table; the one nearest the book is the golden post.
        // What a click can catch: the golden post on the table, and the one in the air.
        const goldenTargets: Object3D[] = [];
        layout.lyingSpots.forEach(([x, z, rot], i) => {
            const geo = own(new PlaneGeometry(PAGE_W, PAGE_H, 10, 10));
            const pos = geo.attributes.position.array as Float32Array;
            for (let k = 0; k < pos.length; k += 3) {
                const u = pos[k] / (PAGE_W / 2);
                const v = pos[k + 1] / (PAGE_H / 2);
                const y = pos[k + 1];
                // Edges curl up a little, as paper does when it has lain a while.
                pos[k + 1] = 0.004 + 0.05 * u * u + 0.03 * Math.max(0, u * v);
                pos[k + 2] = -y;
            }
            geo.computeVertexNormals();
            const m = new Mesh(
                geo,
                i === 0 ? gold.front : materials[(i + 2) % materials.length].front,
            );
            if (i === 0) goldenTargets.push(m);
            m.position.set(x, TABLE_Y, z);
            m.rotation.y = rot;
            m.castShadow = m.receiveShadow = true;
            scene.add(m);
        });

        // --- The emblem: Leprosorium's logo as an object on the desk --------------
        // The real logo, die-cut along its outline into an enamelled plate; the
        // hockey mask is a curved shell raised off the plate and wears the logo's
        // own mask artwork. The red pupils glow, as on the logo, and flare when the
        // hand comes close.
        let maskMat: MeshPhysicalMaterial;
        const maskFocus = new Vector3();
        let maskMesh!: Mesh;
        {
            const { outline, texture: T, mask: M } = logoMeta;
            const emblem = new Group();
            const add = (mesh: Mesh) => {
                mesh.castShadow = mesh.receiveShadow = true;
                emblem.add(mesh);
                return mesh;
            };

            // Plate: the outline extruded, the logo mapped flat onto its faces.
            const shape = new Shape(outline.map(([x, y]) => new Vector2(x, y)));
            const depth = 0.06;
            const plateGeo = own(
                new ExtrudeGeometry(shape, {
                    depth,
                    bevelEnabled: true,
                    bevelThickness: 0.012,
                    bevelSize: 0.008,
                    bevelSegments: 2,
                    curveSegments: 1,
                }),
            );
            const puv = plateGeo.attributes.uv;
            const ppos = plateGeo.attributes.position;
            for (let i = 0; i < ppos.count; i++) {
                puv.setXY(
                    i,
                    (ppos.getX(i) - T.x0) / T.size,
                    (ppos.getY(i) - (T.y0 - T.size)) / T.size,
                );
            }
            logoColor.anisotropy = maxAniso;
            const face = own(
                new MeshPhysicalMaterial({
                    map: logoColor,
                    roughness: 0.3,
                    clearcoat: 1,
                    clearcoatRoughness: 0.1,
                }),
            );
            const rim = own(
                new MeshStandardMaterial({ color: 0x2a2624, roughness: 0.4, metalness: 0.6 }),
            );
            add(new Mesh(plateGeo, [face, rim]));

            // Mask shell over the mask area of the logo.
            const W = M.w / 2;
            const H = M.h / 2;
            const D = W * 0.62;
            const geo = own(
                new SphereGeometry(1, 128, 128, Math.PI / 2 - 1.45, 2.9, 0.1, Math.PI - 0.2),
            );
            const pos = geo.attributes.position;
            const uv = geo.attributes.uv;
            let minZ = Infinity;
            for (let i = 0; i < pos.count; i++) {
                const x = pos.getX(i) * W;
                const y = pos.getY(i) * H;
                let z = pos.getZ(i) * D;
                // Nose ridge.
                z +=
                    0.035 *
                    W *
                    Math.exp(-((x / (0.12 * W)) ** 2)) *
                    Math.exp(-(((y + 0.1 * H) / (0.25 * H)) ** 2));
                pos.setXYZ(i, x, y, z);
                uv.setXY(i, 0.5 + x / (2 * W), 0.5 + y / (2 * H));
                minZ = Math.min(minZ, z);
            }
            geo.computeVertexNormals();
            logoMask.anisotropy = maxAniso;
            maskMat = own(
                new MeshPhysicalMaterial({
                    map: logoMask,
                    alphaMap: logoMaskAlpha,
                    alphaTest: 0.5,
                    side: DoubleSide,
                    emissive: 0xffffff,
                    emissiveMap: logoMaskGlow,
                    emissiveIntensity: 0.8,
                    roughness: 0.35,
                    clearcoat: 0.7,
                    clearcoatRoughness: 0.2,
                }),
            );
            const mask = add(new Mesh(geo, maskMat));
            maskMesh = mask;
            mask.position.set(M.cx, M.cy, depth + 0.012 - minZ + 0.004);

            // Lean it back against the book stack, facing the room.
            emblem.scale.setScalar(layout.emblemScale);
            emblem.rotation.set(-0.3, layout.emblemYaw, 0, 'YXZ');
            emblem.position.copy(layout.maskPos);
            scene.add(emblem);
            emblem.updateMatrixWorld(true);
            // Drop it until its lowest point touches the table.
            const box = new Box3().setFromObject(emblem, true);
            emblem.position.y += TABLE_Y + 0.003 - box.min.y;
            emblem.updateMatrixWorld(true);
            new Box3().setFromObject(mask, true).getCenter(maskFocus);

            // Two closed books, the emblem's support: leather boards, a spine, the
            // page block between them. Right behind the emblem's foot, turned the
            // same way, so the top edge props the star.
            const edgeMat = (() => {
                const t = canvasTexture(edgeCanvas());
                t.wrapS = t.wrapT = RepeatWrapping;
                t.repeat.set(4, 1);
                t.rotation = Math.PI / 2;
                return own(new MeshStandardMaterial({ map: t, roughness: 1, color: 0xe6dcc6 }));
            })();
            const closedBook = (w: number, h: number, d: number, tint: number) => {
                const g = new Group();
                const leather = own(
                    new MeshStandardMaterial({
                        map: leatherColor,
                        normalMap: leatherNormal,
                        roughnessMap: leatherRough,
                        roughness: 1,
                        color: tint,
                    }),
                );
                const board = 0.035;
                const part = (
                    geo: BufferGeometry,
                    mat: Material,
                    x: number,
                    y: number,
                    z: number,
                ) => {
                    const m = new Mesh(geo, mat);
                    m.position.set(x, y, z);
                    m.castShadow = m.receiveShadow = true;
                    g.add(m);
                };
                part(own(new BoxGeometry(w, board, d)), leather, 0, board / 2, 0);
                part(own(new BoxGeometry(w, board, d)), leather, 0, h - board / 2, 0);
                part(own(new BoxGeometry(0.05, h, d)), leather, -w / 2 + 0.025, h / 2, 0);
                part(
                    own(new BoxGeometry(w - 0.09, h - board * 2, d - 0.07)),
                    edgeMat,
                    0.01,
                    h / 2,
                    0,
                );
                return g;
            };
            const k = layout.still;
            const a = closedBook(1.9 * k, 0.3 * k, 2.1 * k, 0x8a6a58);
            const b = closedBook(1.7 * k, 0.26 * k, 1.85 * k, 0x5c4a5a);
            a.position.copy(layout.stackPos);
            a.rotation.y = layout.emblemYaw + Math.PI / 2 + 0.06;
            b.position.copy(layout.stackPos).add(new Vector3(0.04, 0.3, -0.12).multiplyScalar(k));
            b.rotation.y = layout.emblemYaw + Math.PI / 2 - 0.1;
            scene.add(a, b);
        }

        // --- Mid layer: free sheets ------------------------------------------------
        // The camera's aspect has to be right before the sheets are placed: where
        // the frame ends is where they are scattered.
        const resize = () => {
            const w = innerWidth;
            const h = innerHeight;
            // Only when the drawing buffer really changes: setting it, even to
            // the same size, reallocates it and waits for the GPU.
            const ratio = renderer.getPixelRatio();
            if (canvas.width !== Math.floor(w * ratio) || canvas.height !== Math.floor(h * ratio))
                renderer.setSize(w, h, false);
            camera.aspect = w / h;
            camera.updateProjectionMatrix();
            if (composer && film) {
                composer.setPixelRatio(renderer.getPixelRatio());
                composer.setSize(w, h);
                film.uniforms.uRes.value.set(
                    w * renderer.getPixelRatio(),
                    h * renderer.getPixelRatio(),
                );
            }
        };
        let composer: EffectComposer | null = null;
        let film: ShaderPass | null = null;
        resize();

        const sheets = placeSheets(layout.sheetCount, view, layout.obstacles);
        // The sheets in flight; the quality ladder flies fewer.
        let active: Sheet[] = sheets;
        const sheetMeshes = sheets.map((s, i) => {
            const leaf =
                s.kind === 'gold'
                    ? gold
                    : s.kind === 'plate'
                      ? plate
                      : materials[i % materials.length];
            // Same grid as the turning sheet, so a tear maps vertices one for one.
            const geo = own(new PlaneGeometry(PAGE_W, PAGE_H, FLIP_S, FLIP_Z));
            const group = new Group();
            const f = new Mesh(geo, leaf.front);
            const b = new Mesh(geo, leaf.back);
            f.castShadow = true;
            f.receiveShadow = b.receiveShadow = true;
            group.add(f, b);
            // The sheet and its mesh share one position and one orientation.
            group.position.copy(s.position);
            group.quaternion.copy(s.quaternion);
            s.position = group.position;
            s.quaternion = group.quaternion;
            scene.add(group);
            return { group, geo, pos: geo.attributes.position.array as Float32Array };
        });
        separate(sheets, 1 / 60, layout.obstacles);
        const goldSheet = sheetMeshes[sheets.findIndex((s) => s.kind === 'gold')];
        if (goldSheet) goldenTargets.push(goldSheet.group);

        const projV = new Vector3();
        const offscreen = (p: Vector3) => {
            projV.copy(p).project(camera);
            return Math.abs(projV.x) > 1.25 || Math.abs(projV.y) > 1.25;
        };

        const redraw = (i: number, t: number) => {
            const m = sheetMeshes[i];
            deform(sheets[i], m.pos, t);
            m.geo.attributes.position.needsUpdate = true;
            m.geo.computeVertexNormals();
        };

        // Hand the turning sheet over to the free swarm, using a sheet that is
        // currently out of sight.
        const tearOff = (t: number): boolean => {
            const spare = pickSpare(active, offscreen);
            if (!spare) return false;
            const handOff = tearHandOff(flipPos, book.matrixWorld);
            // Tearing next to another sheet would make the separation shove it;
            // let the turn finish instead.
            if (sheets.some((q) => q.position.distanceTo(handOff.position) < SOFT_DIST + 0.4))
                return false;
            const i = sheets.indexOf(spare);
            spare.position.copy(handOff.position);
            spare.quaternion.copy(handOff.quaternion);
            spare.morph = handOff.morph;
            spare.morphT = 1;
            spare.vel.set(...TEAR_VELOCITY);
            spare.spin.set(0, 0, 0);
            spare.bend = 0;
            spare.twist = 0;
            spare.flutter = 0.02;
            spare.lifting = true;
            // This frame's swarm pass already drew it with its old shape.
            redraw(i, t);
            const leaf = leafAt(bookState.page);
            (sheetMeshes[i].group.children[0] as Mesh).material = leaf.front;
            (sheetMeshes[i].group.children[1] as Mesh).material = leaf.back;
            return true;
        };

        const updateBook = (dt: number, t: number, nearBook: boolean) => {
            const st = bookState;
            if (!st.flipping) {
                st.next -= dt * (nearBook ? 4 : 1);
                if (st.next <= 0) startFlip(nearBook);
                return;
            }
            st.p += dt / st.dur;
            if (st.tearAt > 0 && st.p >= st.tearAt) {
                st.tearAt = -1;
                if (tearOff(t)) {
                    flipMesh.visible = false;
                    finishTurn(st);
                    return;
                }
            }
            if (st.p >= 1) {
                flipMesh.visible = false;
                finishTurn(st);
                setBookPages();
                return;
            }
            setFlip(easeTurn(st.p));
        };

        // --- Far layer: hundreds of tiny sheets fading into the dark ---------------
        const rand = (a: number, b: number) => a + Math.random() * (b - a);
        const farMesh = own(
            new InstancedMesh(
                new PlaneGeometry(0.17, 0.24),
                new MeshStandardMaterial({ color: 0xb8ab92, roughness: 0.95, side: DoubleSide }),
                layout.farCount,
            ),
        );
        scene.add(farMesh);
        const farItems = Array.from({ length: layout.farCount }, () => ({
            pos: new Vector3(
                rand(-layout.farHalfX, layout.farHalfX),
                rand(TABLE_Y + 0.6, 10),
                rand(-30, -13),
            ),
            rot: new Euler(rand(0, 6.3), rand(0, 6.3), rand(0, 6.3)),
            w: new Vector3(rand(-0.6, 0.6), rand(-0.9, 0.9), rand(-0.4, 0.4)),
            phase: rand(0, 6.3),
        }));
        const tmpM = new Matrix4();
        const farQ = new Quaternion();
        const farS = new Vector3(1, 1, 1);
        const updateFar = (dt: number, t: number) => {
            farItems.forEach((it, i) => {
                it.pos.x += (0.25 + 0.12 * Math.sin(t * 0.21 + it.phase)) * dt;
                it.pos.y += 0.08 * Math.cos(t * 0.3 + it.phase) * dt;
                if (it.pos.x > layout.farHalfX) it.pos.x = -layout.farHalfX;
                it.rot.x += it.w.x * dt;
                it.rot.y += it.w.y * dt;
                it.rot.z += it.w.z * dt;
                tmpM.compose(it.pos, farQ.setFromEuler(it.rot), farS);
                farMesh.setMatrixAt(i, tmpM);
            });
            farMesh.instanceMatrix.needsUpdate = true;
        };
        // Placed once now, so a still frame — which never steps the far layer —
        // shows it scattered into the dark rather than piled up at the origin.
        updateFar(0, 0);

        // --- Dust in the shaft -------------------------------------------------------
        const dustN = layout.dustCount;
        const dustLo = new Vector3(-9, TABLE_Y, -5);
        const dustHi = new Vector3(1.5, 8.5, 3.5);
        const dustPos = new Float32Array(dustN * 3);
        const dustSeed = new Float32Array(dustN);
        const dustVel = new Float32Array(dustN * 3);
        for (let i = 0; i < dustN; i++) {
            dustPos[i * 3] = rand(dustLo.x, dustHi.x);
            dustPos[i * 3 + 1] = rand(dustLo.y, dustHi.y);
            dustPos[i * 3 + 2] = rand(dustLo.z, dustHi.z);
            dustSeed[i] = Math.random();
        }
        const dustGeo = own(new BufferGeometry());
        dustGeo.setAttribute('position', new BufferAttribute(dustPos, 3));
        dustGeo.setAttribute('aSeed', new BufferAttribute(dustSeed, 1));
        const dustMat = own(
            new ShaderMaterial({
                transparent: true,
                depthWrite: false,
                blending: AdditiveBlending,
                uniforms: {
                    uApex: { value: BEAM_FROM },
                    uAxis: { value: beamAxis },
                    uTan: { value: Math.tan(BEAM_ANGLE) },
                    uPR: { value: renderer.getPixelRatio() },
                    uTime: { value: 0 },
                },
                vertexShader: `
            uniform vec3 uApex; uniform vec3 uAxis; uniform float uTan; uniform float uPR; uniform float uTime;
            attribute float aSeed; varying float vA;
            void main() {
                vec3 v = position - uApex;
                float along = dot(v, uAxis), r = along * uTan, radial = length(v - uAxis * along);
                float inside = along > 0.0 ? 1.0 - smoothstep(r * 0.5, r, radial) : 0.0;
                vec4 mv = viewMatrix * vec4(position, 1.0);
                vA = inside * (0.55 + 0.45 * sin(uTime * 1.3 + aSeed * 40.0)) * smoothstep(28.0, 6.0, -mv.z);
                gl_PointSize = (0.7 + aSeed * 1.6) * uPR * (10.0 / -mv.z) * 1.7;
                gl_Position = projectionMatrix * mv;
            }`,
                fragmentShader: `
            varying float vA;
            void main() {
                float d = length(gl_PointCoord - 0.5);
                gl_FragColor = vec4(1.0, 0.87, 0.64, smoothstep(0.5, 0.0, d) * vA);
            }`,
            }),
        );
        const dust = new Points(dustGeo, dustMat);
        dust.frustumCulled = false;
        scene.add(dust);
        const updateDust = (dt: number, t: number, gust: Gust | null) => {
            for (let i = 0; i < dustN; i++) {
                const k = i * 3;
                const s = dustSeed[i] * 50;
                // Brownian-ish drift: slow, mostly sideways, very light.
                const ax = 0.04 * Math.sin(t * 0.3 + s) + 0.02;
                const ay = 0.03 * Math.cos(t * 0.23 + s * 1.3) - 0.008;
                const az = 0.03 * Math.sin(t * 0.19 + s * 0.7);
                dustVel[k] += (ax - dustVel[k]) * dt * 0.8;
                dustVel[k + 1] += (ay - dustVel[k + 1]) * dt * 0.8;
                dustVel[k + 2] += (az - dustVel[k + 2]) * dt * 0.8;
                if (gust) {
                    const c = gust.at(dustPos[k + 2]);
                    const dx = dustPos[k] - c.x;
                    const dy = dustPos[k + 1] - c.y;
                    const d = Math.hypot(dx, dy);
                    const R = 1.6;
                    if (d < R) {
                        const f = (1 - d / R) ** 2;
                        dustVel[k] +=
                            (gust.vx(dustPos[k + 2]) * 0.35 + (dx / (d || 1)) * 0.1) * f * dt * 3;
                        dustVel[k + 1] +=
                            (gust.vy(dustPos[k + 2]) * 0.35 + (dy / (d || 1)) * 0.1) * f * dt * 3;
                    }
                }
                dustPos[k] += dustVel[k] * dt;
                dustPos[k + 1] += dustVel[k + 1] * dt;
                dustPos[k + 2] += dustVel[k + 2] * dt;
                for (let a = 0; a < 3; a++) {
                    const lo = dustLo.getComponent(a);
                    const hi = dustHi.getComponent(a);
                    if (dustPos[k + a] < lo) dustPos[k + a] = hi;
                    else if (dustPos[k + a] > hi) dustPos[k + a] = lo;
                }
            }
            dustGeo.attributes.position.needsUpdate = true;
            dustMat.uniforms.uTime.value = t;
        };

        // --- Pointer -----------------------------------------------------------------
        // The hand is moving air everywhere on the page, the card included: the
        // listeners are on the window. Only catching the golden post is the
        // canvas's own, so a click on the form never lands on a sheet behind it.
        const pointer = {
            ndc: new Vector2(),
            vel: new Vector2(),
            t: performance.now(),
            active: false,
        };
        const toNdc = (e: PointerEvent | MouseEvent, out: Vector2) =>
            out.set((e.clientX / innerWidth) * 2 - 1, -(e.clientY / innerHeight) * 2 + 1);
        const ndcTmp = new Vector2();
        bag.on<PointerEvent>(window, 'pointermove', (e) => {
            const now = performance.now();
            const dt = Math.max(8, now - pointer.t) / 1000;
            toNdc(e, ndcTmp);
            pointer.vel.x = pointer.vel.x * 0.7 + ((ndcTmp.x - pointer.ndc.x) / dt) * 0.3;
            pointer.vel.y = pointer.vel.y * 0.7 + ((ndcTmp.y - pointer.ndc.y) / dt) * 0.3;
            pointer.ndc.copy(ndcTmp);
            pointer.t = now;
            pointer.active = true;
        });
        bag.on<PointerEvent>(window, 'pointerdown', (e) => {
            // A finger lands somewhere new: no velocity carried over from the last one.
            if (e.pointerType !== 'touch') return;
            toNdc(e, pointer.ndc);
            pointer.vel.set(0, 0);
            pointer.t = performance.now();
        });
        bag.on<PointerEvent>(window, 'pointerup', (e) => {
            if (e.pointerType === 'touch') pointer.active = false;
        });
        bag.on<PointerEvent>(window, 'pointerout', (e) => {
            // Leaving the window, not just one element for another.
            if (!e.relatedTarget) pointer.active = false;
        });

        const ray = new Raycaster();
        const goldenHit = (e: PointerEvent | MouseEvent) => {
            ray.setFromCamera(toNdc(e, ndcTmp), camera);
            return ray.intersectObjects(goldenTargets, true).length > 0;
        };
        bag.on<PointerEvent>(canvas, 'pointermove', (e) => {
            canvas.style.cursor = goldenHit(e) ? 'pointer' : '';
        });
        bag.on<MouseEvent>(canvas, 'click', (e) => {
            if (goldenHit(e)) o.onGolden();
        });

        // --- Camera optics and film ----------------------------------------------------
        const FilmShader = {
            uniforms: {
                tDiffuse: { value: null },
                uTime: { value: 0 },
                uRes: { value: new Vector2(1, 1) },
                uGrain: { value: !o.record || o.record.grain ? 1 : 0 },
            },
            vertexShader: `varying vec2 vUv; void main() { vUv = uv; gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0); }`,
            fragmentShader: `
            uniform sampler2D tDiffuse; uniform float uTime; uniform vec2 uRes; uniform float uGrain; varying vec2 vUv;
            float hash(vec2 p) { return fract(sin(dot(p, vec2(12.9898, 78.233))) * 43758.5453); }
            void main() {
                vec2 d = vUv - 0.5; float r2 = dot(d, d);
                // A touch of lateral chromatic aberration toward the corners.
                vec2 ca = d * 0.006 * r2;
                vec3 col = vec3(texture2D(tDiffuse, vUv + ca).r, texture2D(tDiffuse, vUv).g, texture2D(tDiffuse, vUv - ca).b);
                // Lens vignette.
                col *= mix(1.0, 0.45, smoothstep(0.04, 0.5, r2));
                // Warm, slightly lifted blacks, like print film.
                col = col * 0.97 + vec3(0.016, 0.010, 0.004);
                // Grain, stronger in the shadows.
                float n = hash(vUv * uRes + fract(uTime * 7.13) * 91.7) - 0.5;
                col += uGrain * n * mix(0.045, 0.018, clamp(dot(col, vec3(0.333)), 0.0, 1.0));
                gl_FragColor = vec4(col, 1.0);
            }`,
        };

        // Passes that re-render the scene (normals for AO, depth for focus) must
        // not see the light shaft or the dust: they are see-through and would
        // leave solid silhouettes.
        const hideDuring = (pass: Pass, objects: Object3D[]) => {
            const render = pass.render.bind(pass);
            pass.render = (...args: Parameters<Pass['render']>) => {
                const vis = objects.map((x) => x.visible);
                objects.forEach((x) => (x.visible = false));
                render(...args);
                objects.forEach((x, i) => (x.visible = vis[i]));
            };
        };
        composer = own(new EffectComposer(renderer));
        composer.addPass(own(new RenderPass(scene, camera)));
        // The passes the quality ladder switches off and on again.
        const heavy: Pass[] = [];
        if (layout.heavyPost) {
            const gtao = own(new GTAOPass(scene, camera, innerWidth, innerHeight));
            gtao.updateGtaoMaterial({
                radius: 0.45,
                distanceExponent: 1.5,
                thickness: 1.2,
                scale: 1.0,
                samples: 16,
            });
            gtao.blendIntensity = 0.85;
            hideDuring(gtao, [beamCone, dust]);
            composer.addPass(gtao);
            heavy.push(gtao);
            const bokeh = own(
                new BokehPass(scene, camera, {
                    focus: 9.8,
                    aperture: 0.0003,
                    maxblur: 0.005,
                }),
            );
            hideDuring(bokeh, [beamCone, dust]);
            composer.addPass(bokeh);
            heavy.push(bokeh);
        }
        const bloom = own(
            new UnrealBloomPass(new Vector2(innerWidth, innerHeight), 0.2, 0.7, 0.92),
        );
        composer.addPass(bloom);
        composer.addPass(own(new OutputPass()));
        film = own(new ShaderPass(FilmShader));
        composer.addPass(film);
        resize();
        // Resized once a burst of events has settled, and not at all for the
        // URL bar sliding: every resize reallocates every render target of the
        // post-processing, and a phone fires these by the dozen.
        let applied = { width: innerWidth, height: innerHeight };
        let resizeTimer: ReturnType<typeof setTimeout> | undefined;
        owner.add(() => clearTimeout(resizeTimer));
        bag.on(window, 'resize', () => {
            clearTimeout(resizeTimer);
            resizeTimer = setTimeout(() => {
                const now = { width: innerWidth, height: innerHeight };
                if (!needsResize(applied, now)) return;
                applied = now;
                resize();
                // A scene that is not moving has no loop to pick the new size up.
                if (stopped) still();
            }, RESIZE_DEBOUNCE_MS);
        });

        // --- The frame -------------------------------------------------------------------
        let last = performance.now();
        let maskGlow = 0;
        const step = (now: number, interval: number | null) => {
            // One frame in flight. While the GPU has not finished the last
            // frame, draw nothing and queue nothing: commands handed over
            // faster than the GPU runs them pile up, and once the queue is
            // full the main thread waits on it — seconds on a slow device,
            // every one of them a second the form cannot answer a click.
            let cost: number | null = null;
            // A lost context draws nothing until three restores it, and a
            // fence from before the loss means nothing after it.
            if (gl.isContextLost()) {
                fence = null;
                return;
            }
            // No fences on this context: pace by animation frames, and judge
            // the device by their intervals — coarser than the GPU's own
            // time, but a measure, which silence is not.
            if (fenceless && interval !== null) cost = interval;
            if (fence) {
                const status = gl.getSyncParameter(fence, gl.SYNC_STATUS);
                if (status === gl.UNSIGNALED) return;
                if (status !== gl.SIGNALED) {
                    // Not a fence any more: drop it and draw.
                    fence = null;
                }
            }
            if (fence) {
                gl.deleteSync(fence);
                fence = null;
                // The build is over when the GPU has finished the first frame.
                firstFrameAt ??= performance.now();
                // How long the last frame really took, GPU included — unless
                // the loop itself paused in between (a hold, a hidden tab).
                // Measured on the animation-frame clock, from the frame that
                // issued it to the first one that finds it done.
                if (interval !== null) cost = now - issuedAt;
            }
            const began = performance.now();
            const dt = Math.min(0.05, (now - last) / 1000);
            last = now;
            const t = now / 1000;
            let gust: Gust | null = null;
            if (pointer.active) {
                ray.setFromCamera(pointer.ndc, camera);
                gust = makeGust(ray.ray.origin, ray.ray.direction, pointer.vel, 1.6, view);
            }

            if (pointer.active && !o.record) {
                lamp.position.copy(gust ? gust.at(3) : new Vector3(0, 0, 3));
                lamp.intensity += (4 - lamp.intensity) * Math.min(1, dt * 3);
            } else {
                lamp.intensity += (0 - lamp.intensity) * Math.min(1, dt * 3);
            }
            pointer.vel.multiplyScalar(Math.exp(-dt * 4));

            stepSwarm(active, dt, t, gust, view, layout.obstacles);
            for (let i = 0; i < active.length; i++) redraw(i, t);

            // Is the hand hovering over the book? Then pages turn faster.
            projV.copy(layout.bookCenter).project(camera);
            const nearBook =
                pointer.active &&
                Math.hypot(projV.x - pointer.ndc.x, (projV.y - pointer.ndc.y) / camera.aspect) <
                    0.22;
            updateBook(dt, t, nearBook);
            projV.copy(maskFocus).project(camera);
            // A recording keeps the pupils at their resting glow: the app's
            // overlay brightens them under the reader's own hand.
            const nearMask =
                pointer.active &&
                !o.record &&
                Math.hypot(projV.x - pointer.ndc.x, (projV.y - pointer.ndc.y) / camera.aspect) <
                    0.12;
            maskGlow += ((nearMask ? 1 : 0) - maskGlow) * Math.min(1, dt * (nearMask ? 2.5 : 1.2));
            // Always lit, as on the logo; brighter when the hand is near.
            maskMat.emissiveIntensity = 0.8 + maskGlow * 3.2;
            updateFar(dt, t);
            updateDust(dt, t, gust);

            film!.uniforms.uTime.value = t;
            if (shadowEvery > 1 && frameNo % shadowEvery === 0)
                renderer.shadowMap.needsUpdate = true;
            frameNo++;
            composer!.render(dt);
            if (!fenceless) {
                fence = gl.fenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0);
                if (fence) gl.flush();
                else fenceless = !gl.isContextLost();
            }
            // Without a fence to say when the GPU finished it, the first frame
            // handed over is the end of the build.
            if (fenceless) firstFrameAt ??= performance.now();
            const spent = performance.now() - began;
            if (issuedAt > 0 && now > issuedAt) {
                const sinceLast = now - issuedAt;
                fps = fps === 0 ? 1000 / sinceLast : fps + (1000 / sinceLast - fps) * 0.1;
            }
            issuedAt = now;
            // How the device is coping: the previous frame's whole cost, from
            // handing it over to the GPU being done with it, and this frame's
            // script time, which is ours alone.
            if (cost === null) return;
            const next = governor.sample(cost, spent);
            if (next !== null) {
                applyLevel(next);
                o.quality?.onChange?.(next);
            }
        };

        // No motion: the book open at its first spread, the swarm where it was
        // scattered, drawn once.
        const still = () => {
            for (let i = 0; i < active.length; i++) redraw(i, 0);
            composer!.render(0);
            firstFrameAt ??= performance.now();
        };
        const loop = createLoop(o.still ? still : step, undefined, { still: o.still });
        owner.add(() => loop.dispose());

        // --- Fitting the device -----------------------------------------------------------
        // The quality ladder: see quality.ts. Each level is applied in place,
        // without rebuilding anything.
        let stopped = o.still;
        let shadowEvery = 1;
        let frameNo = 0;
        let fps = 0;
        let firstFrameAt: number | null = null;
        let fence: WebGLSync | null = null;
        let issuedAt = 0;
        owner.add(() => {
            if (fence && !gl.isContextLost()) gl.deleteSync(fence);
        });
        // Whatever was in flight is gone with the context, lost or restored.
        bag.on(canvas, 'webglcontextlost', () => (fence = null));
        bag.on(canvas, 'webglcontextrestored', () => (fence = null));
        const applyLevel = (index: number) => {
            const level = LEVELS[index];
            for (const pass of heavy) pass.enabled = level.heavyPost;
            bloom.enabled = level.bloom;
            const ratio = Math.min(
                devicePixelRatio,
                layout.pixelRatioCap,
                level.pixelRatio ?? Infinity,
            );
            if (ratio !== renderer.getPixelRatio()) {
                renderer.setPixelRatio(ratio);
                dustMat.uniforms.uPR.value = ratio;
                resize();
            }
            dustGeo.setDrawRange(0, Math.round(layout.dustCount * level.share));
            farMesh.count = Math.round(layout.farCount * level.share);
            // Fewer free sheets: the first ones — the golden post and the plate
            // among them — keep flying, the rest are put away.
            active = sheets.slice(0, Math.max(2, Math.round(sheets.length * level.share)));
            sheets.forEach((_, i) => (sheetMeshes[i].group.visible = i < active.length));
            shadowEvery = level.shadowEvery;
            renderer.shadowMap.autoUpdate = shadowEvery === 1;
            renderer.shadowMap.needsUpdate = true;
            if (!level.moving && !stopped) {
                // The last resort: no more frames. The one drawn now, at the
                // cheaper settings, stays on screen.
                stopped = true;
                loop.stop();
                if (firstFrameAt !== null) still();
            }
        };
        applyLevel(governor.level);

        // The first frame's cost is mostly the GPU's: compiling a few dozen
        // shader programs and uploading two dozen page textures. Both are done
        // ahead of it, so the main thread is not held for all of it in one go:
        // the programs through the browser's parallel compile where it has one,
        // the textures one per task, with the page given a turn in between.
        await renderer.compileAsync(scene, camera);
        for (const t of uploads) {
            if (o.signal.aborted) throw new DOMException('unmounted', 'AbortError');
            renderer.initTexture(t);
            await nextTask();
        }
        await gpuDone();

        if (o.signal.aborted) throw new DOMException('unmounted', 'AbortError');

        if (o.record) fenceless = true;
        else if (stopped) still();
        else loop.start();

        if (!announced && !o.record) {
            announced = true;
            // The blog's own boast, as its pages printed it. The page has been
            // generated by now, so the number is true.
            // eslint-disable-next-line no-console -- this line is the feature
            console.info(
                `Витенька, я сгенерировала эту страницу за ${(performance.now() / 1000).toFixed(2)} секунды!`,
            );
        }

        const setTorn = (torn: boolean) => {
            if (owner.disposed) return;
            if (torn && !tornPage) tornPage = buildTornPage();
            if (tornPage) tornPage.visible = torn;
            // A scene that is not moving draws only when asked to.
            if (stopped) still();
        };
        const stats = (): SceneStats => ({
            // A room that has stopped draws nothing; its last rate would mislead.
            fps: stopped ? 0 : Math.round(fps * 10) / 10,
            level: LEVELS[governor.level].name,
            buildMs: firstFrameAt === null ? null : Math.round(firstFrameAt - buildStart),
            phases: {
                assets: Math.round(assetsAt - buildStart),
                pages: Math.round(pagesAt - assetsAt),
                gpu: firstFrameAt === null ? null : Math.round(firstFrameAt - pagesAt),
            },
            renderer: rendererName(gl),
            pixelRatio: renderer.getPixelRatio(),
            canvas: `${canvas.width}×${canvas.height}`,
            layout: o.phone ? 'phone' : o.wide ? 'ultrawide' : 'desktop',
        });
        // --- Recording -----------------------------------------------------------------
        let recordStarted = false;
        const advance = (now: number, input: RecordGust | null) => {
            if (!o.record) throw new Error('auth scene: advance() is for recordings');
            if (!recordStarted) {
                recordStarted = true;
                last = now - 1000 / 30;
            }
            pointer.active = input !== null;
            if (input) {
                pointer.ndc.set(input.ndc[0], input.ndc[1]);
                pointer.vel.set(input.vel[0], input.vel[1]);
            }
            step(now, null);
        };
        const markers = (): SceneMarkers => {
            const width = canvas.width;
            const height = canvas.height;
            const px = (v: Vector3): [number, number] => {
                const p = v.clone().project(camera);
                // Whole pixels: finer than any hand, and the app bundles these.
                return [Math.round(((p.x + 1) / 2) * width), Math.round(((1 - p.y) / 2) * height)];
            };
            // The renderer updates these as it draws; markers may be asked for
            // before the first frame.
            scene.updateMatrixWorld(true);
            camera.updateMatrixWorld(true);
            // The golden post on the table: the corners of its 11 × 11 grid.
            const lying = goldenTargets[0] as Mesh;
            const lp = lying.geometry.attributes.position;
            const goldenPost = [0, 10, 120, 110].map((i) =>
                px(lying.localToWorld(new Vector3().fromBufferAttribute(lp, i))),
            );
            // The pupils: the two bright blobs of the mask's own glow texture,
            // found on the shell by their texture coordinates.
            const mp = maskMesh.geometry.attributes.position;
            const muv = maskMesh.geometry.attributes.uv;
            const pupils = pupilUVs(logoMaskGlowImg).map(([u, v]) => {
                let best = 0;
                let bestD = Infinity;
                for (let i = 0; i < muv.count; i++) {
                    const d = (muv.getX(i) - u) ** 2 + (muv.getY(i) - v) ** 2;
                    if (d < bestD) {
                        bestD = d;
                        best = i;
                    }
                }
                return px(maskMesh.localToWorld(new Vector3().fromBufferAttribute(mp, best)));
            });
            let x0 = Infinity;
            let y0 = Infinity;
            let x1 = -Infinity;
            let y1 = -Infinity;
            for (let i = 0; i < mp.count; i += 7) {
                const [x, y] = px(maskMesh.localToWorld(new Vector3().fromBufferAttribute(mp, i)));
                x0 = Math.min(x0, x);
                y0 = Math.min(y0, y);
                x1 = Math.max(x1, x);
                y1 = Math.max(y1, y);
            }
            return {
                width,
                height,
                goldenPost,
                pupils,
                mask: { x: x0, y: y0, w: x1 - x0, h: y1 - y0 },
            };
        };
        return {
            dispose: () => owner.dispose(),
            setTorn,
            hold: () => loop.hold(2),
            stats,
            advance,
            markers,
        };
    } catch (error) {
        owner.dispose();
        throw error;
    }
}

/**
 * Release every geometry, material, material texture and instanced mesh in a
 * scene graph. Shared ones are met more than once and released once.
 */
function disposeGraph(root: Object3D): void {
    const seen = new Set<object>();
    const drop = (x: { dispose(): void } | null | undefined) => {
        if (!x || seen.has(x)) return;
        seen.add(x);
        x.dispose();
    };
    root.traverse((obj) => {
        const mesh = obj as Mesh;
        drop(mesh.geometry);
        const mats = mesh.material
            ? Array.isArray(mesh.material)
                ? mesh.material
                : [mesh.material]
            : [];
        for (const m of mats) {
            for (const v of Object.values(m)) if (v instanceof Texture) drop(v);
            drop(m);
        }
        if (obj instanceof InstancedMesh) drop(obj);
    });
}

/**
 * The GPU's name, for the diagnostics overlay. Asked for only when the overlay
 * is open: Firefox warns on the console about the extension that unmasks it,
 * and Chromium needs that extension to say anything useful.
 */
function rendererName(gl: WebGL2RenderingContext): string {
    const plain = String(gl.getParameter(gl.RENDERER));
    if (!/^(WebKit WebGL|Mozilla|undefined|masked)$/.test(plain)) return plain;
    const info = gl.getExtension('WEBGL_debug_renderer_info');
    return info ? String(gl.getParameter(info.UNMASKED_RENDERER_WEBGL)) : plain;
}

/**
 * The texture coordinates of the two pupils: the bright blobs of the mask's
 * glow image, split down the middle, each at its centroid. Recording only.
 */
function pupilUVs(img: HTMLImageElement): [number, number][] {
    const c = document.createElement('canvas');
    c.width = img.width;
    c.height = img.height;
    const g = c.getContext('2d', { willReadFrequently: true })!;
    g.drawImage(img, 0, 0);
    const { data } = g.getImageData(0, 0, c.width, c.height);
    const sums = [
        [0, 0, 0],
        [0, 0, 0],
    ];
    for (let y = 0; y < c.height; y++)
        for (let x = 0; x < c.width; x++) {
            const w = data[(y * c.width + x) * 4];
            if (w < 40) continue;
            const side = x < c.width / 2 ? 0 : 1;
            sums[side][0] += x * w;
            sums[side][1] += y * w;
            sums[side][2] += w;
        }
    // Texture v runs up, image rows run down.
    return sums.map(([sx, sy, sw]) => [sx / sw / c.width, 1 - sy / sw / c.height]);
}
