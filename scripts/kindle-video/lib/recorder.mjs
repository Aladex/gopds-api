// Records one scene: Chrome's screencast frames (with their wall-clock
// timestamps) plus a log of what the scene did and when — captions, zooms,
// markers — so the encoder can rebuild the timeline exactly.
import fs from 'node:fs';
import path from 'node:path';

export const VIEW = { width: 1280, height: 720 };
// Frames are captured at twice the output size so the zoom in post stays sharp.
export const SCALE = 2;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const now = () => Date.now() / 1000;

export class Recorder {
    constructor(page, dir) {
        this.page = page;
        this.dir = dir;
        this.frames = [];
        this.events = [];
        this.pending = [];
    }

    async start() {
        fs.rmSync(this.dir, { recursive: true, force: true });
        fs.mkdirSync(this.dir, { recursive: true });
        this.cdp = await this.page.context().newCDPSession(this.page);
        this.cdp.on('Page.screencastFrame', (f) => {
            const file = path.join(this.dir, `f${String(this.frames.length).padStart(6, '0')}.jpg`);
            this.frames.push({ file, t: f.metadata.timestamp });
            this.pending.push(fs.promises.writeFile(file, Buffer.from(f.data, 'base64')));
            this.cdp.send('Page.screencastFrameAck', { sessionId: f.sessionId }).catch(() => {});
        });
        this.t0 = now();
        await this.cdp.send('Page.startScreencast', {
            format: 'jpeg',
            quality: 92,
            maxWidth: VIEW.width * SCALE,
            maxHeight: VIEW.height * SCALE,
            everyNthFrame: 1,
        });
        // The first frame arrives on its own; wait for it so t=0 is a picture.
        while (this.frames.length === 0) await sleep(10);
        this.t0 = this.frames[0].t;
    }

    /** Seconds since the first frame. */
    time() {
        return now() - this.t0;
    }

    log(kind, data = {}) {
        this.events.push({ kind, t: this.time(), ...data });
    }

    async stop() {
        this.log('end');
        await this.cdp.send('Page.stopScreencast');
        await Promise.all(this.pending);
        await this.cdp.detach();
        const meta = {
            frames: this.frames.map((f) => ({ file: path.basename(f.file), t: f.t - this.t0 })),
            events: this.events,
            duration: this.events.at(-1).t,
        };
        fs.writeFileSync(path.join(this.dir, 'meta.json'), JSON.stringify(meta, null, 1));
        return meta;
    }
}
