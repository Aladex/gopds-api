// The verbs a scene is written in: caption, move, click, zoom, hold. Each one
// acts on the page and logs itself to the recorder, so the timeline the
// encoder rebuilds is the one that was actually played.
import { XFADE } from './encode.mjs';
import { VIEW } from './recorder.mjs';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const ease = (p) => (p < 0.5 ? 2 * p * p : 1 - (-2 * p + 2) ** 2 / 2);

/** Minimum time a caption stays up: 2.5 s a line, slower for long lines. */
export function captionMinSeconds(text) {
    const lines = text.split('\n');
    return lines.reduce((sum, l) => sum + Math.max(2.6, l.length / 14), 0);
}

export class Shot {
    constructor({ page, recorder, captions, step, cursorStart }) {
        this.page = page;
        this.rec = recorder;
        this.captions = captions;
        this.step = step;
        this.cursor = cursorStart ?? { x: VIEW.width * 0.62, y: VIEW.height * 0.55 };
        this.current = null;
    }

    async begin() {
        await this.page.mouse.move(this.cursor.x, this.cursor.y);
        await this.page.evaluate(({ x, y }) => window.__vc?.set(x, y), this.cursor);
    }

    /**
     * Shows caption `id`. The one before it is held until it has been readable
     * for its minimum time, so a fast scene can never flash a caption past.
     */
    async caption(id, { position = 'bottom' } = {}) {
        const text = this.captions[id];
        if (!text) throw new Error(`no caption "${id}"`);
        await this.settleCaption();
        this.rec.log('caption', { id, text, step: this.step, position });
        this.current = { at: this.rec.time(), min: captionMinSeconds(text) };
    }

    /** Waits until the current caption has had its time on screen. */
    async settleCaption() {
        if (!this.current) return;
        const left = this.current.at + this.current.min - this.rec.time();
        if (left > 0) await sleep(left * 1000);
    }

    async hold(ms) {
        await sleep(ms);
    }

    async point(target) {
        if (typeof target.x === 'number' && typeof target.y === 'number' && !target.boundingBox) {
            return target;
        }
        await target.scrollIntoViewIfNeeded();
        const box = await target.boundingBox();
        if (!box) throw new Error('target has no box');
        return { x: box.x + box.width / 2, y: box.y + box.height / 2, box };
    }

    /** Glides the pointer to `target` along an eased path. */
    async move(target, { duration = 750, dx = 0, dy = 0 } = {}) {
        const p = await this.point(target);
        const to = { x: p.x + dx, y: p.y + dy };
        const from = { ...this.cursor };
        const dist = Math.hypot(to.x - from.x, to.y - from.y);
        const ms = dist < 4 ? 0 : Math.max(320, Math.min(duration, 260 + dist * 0.9));
        const t0 = Date.now();
        while (ms > 0) {
            const k = Math.min(1, (Date.now() - t0) / ms);
            const e = ease(k);
            await this.page.mouse.move(from.x + (to.x - from.x) * e, from.y + (to.y - from.y) * e);
            if (k >= 1) break;
            await sleep(14);
        }
        this.cursor = to;
        return p;
    }

    async click(target, { pause = 220, after = 350, ...moveOpts } = {}) {
        const p = await this.move(target, moveOpts);
        await sleep(pause);
        await this.page.mouse.down();
        await sleep(90);
        await this.page.mouse.up();
        await sleep(after);
        return p;
    }

    /**
     * Scrolls `target` to `block` (0 = top, 1 = bottom of the viewport) along
     * an eased path, in whichever container scrolls it — the window or a
     * dialog body.
     */
    async scrollTo(target, { block = 0.3, duration = 900 } = {}) {
        await target.evaluate(
            (el, o) =>
                new Promise((done) => {
                    let box = el.parentElement;
                    while (box && box !== document.body) {
                        const oy = getComputedStyle(box).overflowY;
                        if ((oy === 'auto' || oy === 'scroll') && box.scrollHeight > box.clientHeight) break;
                        box = box.parentElement;
                    }
                    const win = !box || box === document.body;
                    const top = win ? 0 : box.getBoundingClientRect().top;
                    const height = win ? innerHeight : box.clientHeight;
                    const max = win
                        ? document.documentElement.scrollHeight - innerHeight
                        : box.scrollHeight - box.clientHeight;
                    const from = win ? scrollY : box.scrollTop;
                    const goal = Math.max(
                        0,
                        Math.min(max, from + el.getBoundingClientRect().top - top - height * o.block),
                    );
                    const t0 = performance.now();
                    const step = (t) => {
                        const k = Math.min(1, (t - t0) / o.duration);
                        const e = k < 0.5 ? 2 * k * k : 1 - (-2 * k + 2) ** 2 / 2;
                        const y = from + (goal - from) * e;
                        if (win) scrollTo(0, y);
                        else box.scrollTop = y;
                        if (k < 1) requestAnimationFrame(step);
                        else done();
                    };
                    requestAnimationFrame(step);
                }),
            { block, duration },
        );
    }

    /** Shows a key-cap badge beside the cursor. */
    async keys(...names) {
        await this.page.evaluate((n) => window.__vc?.keys(...n), names);
    }

    /**
     * Zooms the frame in on `target` (a locator or {x, y}) in post. The scale
     * is clamped so the zoomed frame never leaves the page.
     */
    async zoom(target, scale = 1.6, { duration = 0.9 } = {}) {
        const p = await this.point(target);
        this.rec.log('zoom', { cx: p.x, cy: p.y, scale, dur: duration });
        await sleep(duration * 1000);
    }

    async unzoom({ duration = 0.8 } = {}) {
        this.rec.log('zoom', { cx: VIEW.width / 2, cy: VIEW.height / 2, scale: 1, dur: duration });
        await sleep(duration * 1000);
    }

    mark(name) {
        this.rec.log('mark', { name });
    }

    /**
     * Ends the scene: lets the last caption finish, plus the cross-fade into
     * the next scene (which eats the scene's last half second), then a tail.
     */
    async end(tail = 300) {
        await this.settleCaption();
        await sleep((XFADE + 0.1) * 1000 + tail);
    }
}
