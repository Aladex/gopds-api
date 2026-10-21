import './seed';
import createScene, { type RecordGust, type SceneMarkers } from '../scene/createScene';

/**
 * The page the recorder drives: the real scene, in record mode, on a canvas
 * the size of the video. Each frame is advanced by a fixed step and handed
 * back to the recorder as a PNG of the drawing buffer — exactly what the GPU
 * produced, no screen capture in between.
 */

declare global {
    interface Window {
        recorder: {
            markers(): SceneMarkers;
            frame(
                index: number,
                now: number,
                gust: RecordGust | null,
                capture: boolean,
            ): string | null;
        };
    }
}

const params = new URLSearchParams(location.search);
const canvas = document.querySelector('canvas')!;
const scene = await createScene({
    canvas,
    phone: params.get('phone') === '1',
    wide: params.get('wide') === '1',
    still: false,
    torn: params.get('torn') === '1',
    onGolden: () => {},
    signal: new AbortController().signal,
    record: { level: Number(params.get('level') ?? 0) },
});

window.recorder = {
    markers: () => scene.markers(),
    frame(_index, now, gust, capture) {
        scene.advance(now, gust);
        if (!capture) return null;
        // The drawing buffer, preserved, as a PNG: exactly what the GPU made.
        return canvas.toDataURL('image/png');
    },
};
document.title = 'ready';
