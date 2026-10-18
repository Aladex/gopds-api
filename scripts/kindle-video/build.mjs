// Builds the "How to set up Send to Kindle" videos.
//
//   node build.mjs                 record + encode both languages
//   node build.mjs --lang ru       one language
//   node build.mjs --skip-record   re-encode from the last recording in out/
//   node build.mjs --vite-url http://127.0.0.1:3000   use a running dev server
//
// Outputs booksdump-frontend/public/videos/kindle-setup-{ru,en}.{mp4,jpg} and
// out/keyframes-{ru,en}.png (a contact sheet, not committed).
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';

import { chromium } from 'playwright';

import { CAPTIONS } from './captions.mjs';
import { renderCaptions } from './lib/caption-images.mjs';
import {
    assemble,
    contactSheet,
    frameAt,
    MAX_BYTES,
    probeDuration,
    renderScene,
    timeline,
} from './lib/encode.mjs';
import { Recorder, SCALE, VIEW } from './lib/recorder.mjs';
import { Shot } from './lib/shot.mjs';
import { amazonApprove, amazonPreferences } from './scenes/amazon.mjs';
import { ORIGIN, profileCopy, profilePaste, sendBook, setupDialog } from './scenes/booksdump.mjs';

const HERE = import.meta.dirname;
const FRONTEND = path.resolve(HERE, '..', '..', 'booksdump-frontend');
const PUBLIC = path.join(FRONTEND, 'public', 'videos');
const OUT = path.join(HERE, 'out');
const SCENES = [setupDialog, profileCopy, amazonPreferences, amazonApprove, profilePaste, sendBook];
const CRFS = [22, 24, 26, 28, 30, 32];

const { values: args } = parseArgs({
    options: {
        lang: { type: 'string', multiple: true },
        'skip-record': { type: 'boolean', default: false },
        'vite-url': { type: 'string' },
    },
});
const LANGS = args.lang ?? ['ru', 'en'];

async function startVite() {
    if (args['vite-url']) return { url: args['vite-url'], stop() {} };
    const port = 3417;
    const child = spawn(
        path.join(FRONTEND, 'node_modules', '.bin', 'vite'),
        ['--port', String(port), '--strictPort', '--host', '127.0.0.1'],
        { cwd: FRONTEND, stdio: ['ignore', 'pipe', 'inherit'] },
    );
    const url = `http://127.0.0.1:${port}`;
    for (let i = 0; i < 100; i++) {
        try {
            if ((await fetch(url)).ok) return { url, stop: () => child.kill() };
        } catch {
            // not up yet
        }
        await new Promise((r) => setTimeout(r, 200));
    }
    child.kill();
    throw new Error('vite did not start');
}

async function record(browser, lang, viteUrl) {
    const context = await browser.newContext({
        viewport: VIEW,
        deviceScaleFactor: SCALE,
        locale: lang === 'ru' ? 'ru-RU' : 'en-US',
        colorScheme: 'light',
        timezoneId: 'Europe/Berlin',
        // Copy and paste are real: what scene 1 copies is what scene 3 pastes.
        permissions: ['clipboard-read', 'clipboard-write'],
    });
    // Nothing leaves the machine: booksdump.com is served locally, the Amazon
    // mock is a file, and anything else is refused.
    await context.route(
        (url) => url.protocol.startsWith('http') && url.hostname !== new URL(ORIGIN).hostname,
        (route) => route.abort(),
    );
    await context.routeWebSocket(
        (url) => new URL(url).pathname !== '/api/ws',
        () => {
            // Vite's HMR socket and anything else: accepted and left silent.
        },
    );
    await context.addInitScript({ path: path.join(HERE, 'lib', 'overlay.js') });
    await context.addInitScript((l) => {
        try {
            localStorage.setItem('interfaceLang', l);
        } catch {
            // file:// pages have no storage; they do not need it
        }
    }, lang);

    const env = {
        context,
        lang,
        viteUrl,
        captions: CAPTIONS[lang],
        state: { kindleEmail: '', unmocked: new Set() },
    };
    let page = null;
    let cursor;
    for (const scene of SCENES) {
        if (!scene.continues) {
            await page?.close();
            page = await scene.open(env);
        }
        env.page = page;
        const rec = new Recorder(page, path.join(OUT, lang, scene.id));
        await rec.start();
        const shot = new Shot({
            page,
            recorder: rec,
            captions: env.captions,
            step: scene.step,
            cursorStart: cursor,
        });
        await shot.begin();
        await scene.play(shot, env);
        cursor = shot.cursor;
        const meta = await rec.stop();
        console.log(`  ${lang} ${scene.id}: ${meta.duration.toFixed(1)} s, ${meta.frames.length} frames`);
    }
    await context.close();
    if (env.state.unmocked.size) console.warn(`  unmocked API calls: ${[...env.state.unmocked].join(', ')}`);
}

async function encode(browser, lang) {
    const scenes = SCENES.map((s) => {
        const dir = path.join(OUT, lang, s.id);
        const meta = JSON.parse(fs.readFileSync(path.join(dir, 'meta.json'), 'utf8'));
        const clip = path.join(OUT, lang, `${s.id}.mp4`);
        renderScene(dir, meta, clip);
        return { ...s, meta, clip };
    });
    const { cues, marks, duration } = timeline(scenes);
    const withPng = await renderCaptions(browser, cues, path.join(OUT, lang, 'captions'));

    fs.mkdirSync(PUBLIC, { recursive: true });
    const video = path.join(PUBLIC, `kindle-setup-${lang}.mp4`);
    let crf;
    let size;
    for (crf of CRFS) {
        size = assemble(scenes, withPng, video, crf);
        if (size <= MAX_BYTES) break;
    }
    if (size > MAX_BYTES) throw new Error(`${lang}: ${size} bytes even at crf ${crf}`);

    const posterAt = marks.poster ?? duration / 4;
    frameAt(video, posterAt, path.join(PUBLIC, `kindle-setup-${lang}.jpg`));
    contactSheet(video, withPng, path.join(OUT, `keyframes-${lang}.png`), path.join(OUT, lang, 'keyframes'));

    const summary = {
        lang,
        file: path.relative(path.resolve(HERE, '..', '..'), video),
        seconds: Number(probeDuration(video).toFixed(2)),
        bytes: size,
        crf,
        posterAt: Number(posterAt.toFixed(2)),
        scenes: scenes.map((s) => ({ id: s.id, step: s.step, seconds: Number(s.meta.duration.toFixed(2)) })),
        captions: cues.map((c) => ({
            step: c.step,
            from: Number(c.start.toFixed(2)),
            to: Number(c.end.toFixed(2)),
            text: c.text,
        })),
    };
    fs.writeFileSync(path.join(OUT, `summary-${lang}.json`), JSON.stringify(summary, null, 2));
    console.log(`  ${lang}: ${summary.seconds} s, ${(size / 1e6).toFixed(2)} MB at crf ${crf}`);
}

const browser = await chromium.launch();
const vite = args['skip-record'] ? null : await startVite();
try {
    for (const lang of LANGS) {
        console.log(`[${lang}]`);
        if (vite) await record(browser, lang, vite.url);
        await encode(browser, lang);
    }
} finally {
    vite?.stop();
    await browser.close();
}
