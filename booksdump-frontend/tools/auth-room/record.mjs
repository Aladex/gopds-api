#!/usr/bin/env node
/**
 * Renders the reading-room videos the sign-in screens play, from the real
 * scene, deterministically. See README.md next to this file.
 *
 *   node tools/auth-room/record.mjs [variant ...]
 *
 * variants: desktop, desktop-torn, phone, phone-torn (default: all four).
 * Environment: AUTH_ROOM_CHROME — a Chromium to drive (default: Playwright's);
 * AUTH_ROOM_SEED — the scene's random seed (default 7); AUTH_ROOM_MARKERS_ONLY=1
 * — only re-export the Easter eggs' positions, without rendering.
 */
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { chromium } from 'playwright-core';
import { format, resolveConfig } from 'prettier';
import { build } from 'vite';

import { mergeMarkers } from './recorder/markers.ts';

const here = path.dirname(fileURLToPath(import.meta.url));
const frontend = path.resolve(here, '../..');
const work = path.join(here, 'work');
const outDir = path.join(frontend, 'public/auth-room');
const markersFile = path.join(frontend, 'src/features/auth/room/markers.json');

const FPS = 30;
/** The loop, in seconds, and the crossfade that closes it. */
const LOOP_S = 24;
const FADE_S = 2;
/** Simulated before the first captured frame, so the swarm has settled. */
const WARMUP_S = 3;
const SEED = Number(process.env.AUTH_ROOM_SEED ?? 7);

const VARIANTS = {
    desktop: { width: 1920, height: 1080, phone: false, torn: false, level: 0, budget: 3.0e6 },
    'desktop-torn': {
        width: 1920,
        height: 1080,
        phone: false,
        torn: true,
        level: 0,
        budget: 3.0e6,
    },
    phone: { width: 1080, height: 1920, phone: true, torn: false, level: 1, budget: 2.5e6 },
    'phone-torn': { width: 1080, height: 1920, phone: true, torn: true, level: 1, budget: 2.5e6 },
};

/**
 * The scripted air: a few slow sweeps of a "hand" across the room, so sheets
 * tumble without anyone at the mouse. Kept clear of the first and the last
 * FADE_S of the loop, where the crossfade would show a gust twice.
 */
function gust(t) {
    const sweeps = [4, 11, 18].map((s) => s + WARMUP_S);
    const DURATION = 1.6;
    for (const [i, start] of sweeps.entries()) {
        const p = (t - start) / DURATION;
        if (p < 0 || p > 1) continue;
        const dir = i % 2 ? -1 : 1;
        const e = p * p * (3 - 2 * p);
        const x = dir * (-0.85 + 1.7 * e);
        const y = 0.25 - 0.3 * e + 0.05 * Math.sin(p * Math.PI * 2);
        // The derivative of the eased path, in screen units per second.
        const vx = (dir * 1.7 * 6 * p * (1 - p)) / DURATION;
        const vy = (-0.3 * 6 * p * (1 - p)) / DURATION;
        return { ndc: [x, y], vel: [vx, vy] };
    }
    return null;
}

function run(cmd, args) {
    const r = spawnSync(cmd, args, { stdio: ['ignore', 'pipe', 'pipe'], encoding: 'utf8' });
    if (r.status !== 0) throw new Error(`${cmd} ${args.join(' ')}\n${r.stderr}`);
    return r.stderr + r.stdout;
}

async function buildPage() {
    const dist = path.join(work, 'dist');
    await build({
        root: path.join(here, 'recorder'),
        publicDir: path.join(here, 'public'),
        logLevel: 'warn',
        configFile: false,
        build: { outDir: dist, emptyOutDir: true, target: 'esnext' },
    });
    return dist;
}

/** Serves the recorder page. */
function serve(dist) {
    const types = {
        '.html': 'text/html',
        '.js': 'text/javascript',
        '.webp': 'image/webp',
        '.png': 'image/png',
        '.woff2': 'font/woff2',
        '.hdr': 'application/octet-stream',
    };
    const server = http.createServer((req, res) => {
        const url = new URL(req.url, 'http://x');
        const file = path.join(dist, url.pathname === '/' ? 'index.html' : url.pathname);
        if (!file.startsWith(dist) || !fs.existsSync(file)) {
            res.statusCode = 404;
            return res.end();
        }
        res.setHeader('Content-Type', types[path.extname(file)] ?? 'application/octet-stream');
        fs.createReadStream(file).pipe(res);
    });
    return new Promise((resolve) =>
        server.listen(0, '127.0.0.1', () =>
            resolve({
                url: `http://127.0.0.1:${server.address().port}`,
                close: () => server.close(),
            }),
        ),
    );
}

async function record(browser, server, name, v) {
    const dir = path.join(work, name);
    const framesDir = path.join(dir, 'frames');
    // A markers-only run leaves the last render's frames and master alone.
    if (!process.env.AUTH_ROOM_MARKERS_ONLY) {
        fs.rmSync(dir, { recursive: true, force: true });
        fs.mkdirSync(framesDir, { recursive: true });
    }

    const page = await browser.newPage({
        viewport: { width: v.width, height: v.height },
        deviceScaleFactor: 1,
    });
    page.on('pageerror', (e) => console.error(`[${name}] ${e.message}`));
    await page.goto(
        `${server.url}/?phone=${+v.phone}&torn=${+v.torn}&level=${v.level}&seed=${SEED}`,
    );
    await page.waitForFunction(() => document.title === 'ready', null, { timeout: 120000 });
    const markers = await page.evaluate(() => window.recorder.markers());
    if (process.env.AUTH_ROOM_MARKERS_ONLY) {
        await page.close();
        return { dir, framesDir, markers };
    }

    const warm = WARMUP_S * FPS;
    const total = warm + (LOOP_S + FADE_S) * FPS;
    for (let i = 0; i < total; i++) {
        const t = i / FPS;
        const png = await page.evaluate(
            ([index, now, g, capture]) => window.recorder.frame(index, now, g, capture),
            [i - warm, t * 1000, gust(t), i >= warm],
        );
        if (png) {
            const file = path.join(framesDir, `${String(i - warm).padStart(5, '0')}.png`);
            fs.writeFileSync(file, Buffer.from(png.slice(png.indexOf(',') + 1), 'base64'));
        }
        if (i % 150 === 0) process.stdout.write(`[${name}] ${i}/${total}\n`);
    }
    await page.close();
    return { dir, framesDir, markers };
}

/**
 * The loop: frames 0..L+F rendered; the output's first F frames crossfade
 * from the frames after the loop (L..L+F) into its start (0..F), so the last
 * output frame (L-1) is followed by L — the very next rendered frame — when
 * the video wraps. The seam itself is never blended; the blend is inside.
 */
function assemble(dir, framesDir) {
    const L = LOOP_S * FPS;
    const F = FADE_S * FPS;
    const master = path.join(dir, 'master.mkv');
    run('ffmpeg', [
        '-y',
        '-framerate',
        String(FPS),
        '-i',
        path.join(framesDir, '%05d.png'),
        '-filter_complex',
        `[0:v]format=gbrp,split=3[s0][s1][s2];` +
            `[s0]trim=start_frame=${L}:end_frame=${L + F},setpts=PTS-STARTPTS[tail];` +
            `[s1]trim=start_frame=0:end_frame=${F},setpts=PTS-STARTPTS[head];` +
            `[s2]trim=start_frame=${F}:end_frame=${L},setpts=PTS-STARTPTS[body];` +
            `[tail][head]blend=all_expr='A*(1-T*${FPS}/${F})+B*(T*${FPS}/${F})'[fade];` +
            `[fade][body]concat=n=2:v=1[out]`,
        '-map',
        '[out]',
        '-c:v',
        'ffv1',
        master,
    ]);
    return master;
}

/** Encode under the variant's budget: the lowest CRF that fits. */
function encode(master, out, budget) {
    const results = {};
    for (const [ext, crfs, args] of [
        [
            'webm',
            [24, 28, 32, 36, 40],
            (crf) => [
                '-c:v',
                'libvpx-vp9',
                '-b:v',
                '0',
                '-crf',
                String(crf),
                '-row-mt',
                '1',
                '-tile-columns',
                '2',
                '-g',
                '240',
                '-pix_fmt',
                'yuv420p',
                '-an',
            ],
        ],
        [
            'mp4',
            [19, 22, 25, 28, 31],
            (crf) => [
                '-c:v',
                'libx264',
                '-profile:v',
                'high',
                '-preset',
                'slow',
                '-crf',
                String(crf),
                '-g',
                '240',
                '-pix_fmt',
                'yuv420p',
                '-movflags',
                '+faststart',
                '-an',
            ],
        ],
    ]) {
        const file = `${out}.${ext}`;
        for (const crf of crfs) {
            if (ext === 'webm') {
                const log = `${out}-vp9`;
                run('ffmpeg', [
                    '-y',
                    '-i',
                    master,
                    ...args(crf),
                    '-pass',
                    '1',
                    '-passlogfile',
                    log,
                    '-f',
                    'null',
                    '/dev/null',
                ]);
                run('ffmpeg', [
                    '-y',
                    '-i',
                    master,
                    ...args(crf),
                    '-pass',
                    '2',
                    '-passlogfile',
                    log,
                    file,
                ]);
                for (const f of fs.readdirSync(path.dirname(log)))
                    if (f.startsWith(path.basename(log)))
                        fs.rmSync(path.join(path.dirname(log), f));
            } else run('ffmpeg', ['-y', '-i', master, ...args(crf), file]);
            const size = fs.statSync(file).size;
            results[ext] = { crf, bytes: size, kbps: Math.round((size * 8) / LOOP_S / 1000) };
            if (size <= budget) break;
        }
    }
    run('ffmpeg', [
        '-y',
        '-i',
        master,
        '-frames:v',
        '1',
        '-c:v',
        'libwebp',
        '-quality',
        '80',
        `${out}.webp`,
    ]);
    results.poster = { bytes: fs.statSync(`${out}.webp`).size };
    return results;
}

/** How alike two frames are; the seam should be as alike as any two neighbours. */
function ssim(master, a, b, dir) {
    const grab = (n) => {
        const f = path.join(dir, `ssim-${n}.png`);
        run('ffmpeg', [
            '-y',
            '-i',
            master,
            '-vf',
            `select=eq(n\\,${n})`,
            '-fps_mode',
            'passthrough',
            '-frames:v',
            '1',
            f,
        ]);
        return f;
    };
    const out = run('ffmpeg', ['-i', grab(a), '-i', grab(b), '-lavfi', 'ssim', '-f', 'null', '-']);
    return Number(/All:([\d.]+)/.exec(out)[1]);
}

async function main() {
    const names = process.argv.slice(2).length ? process.argv.slice(2) : Object.keys(VARIANTS);
    fs.mkdirSync(work, { recursive: true });
    fs.mkdirSync(outDir, { recursive: true });
    const dist = await buildPage();
    const server = await serve(dist);
    const browser = await chromium.launch({
        executablePath: process.env.AUTH_ROOM_CHROME || undefined,
        args: ['--use-angle=vulkan', '--enable-gpu', '--ignore-gpu-blocklist'],
    });
    const markers = {};
    const report = {};
    try {
        for (const name of names) {
            const v = VARIANTS[name];
            const r = await record(browser, server, name, v);
            markers[name] = r.markers;
            if (process.env.AUTH_ROOM_MARKERS_ONLY) continue;
            const master = assemble(r.dir, r.framesDir);
            const L = LOOP_S * FPS;
            const sizes = encode(master, path.join(outDir, name), v.budget);
            const neighbours = [1, 300, 600, L - 2].map((k) => ssim(master, k - 1, k, r.dir));
            report[name] = {
                sizes,
                seamSsim: ssim(master, L - 1, 0, r.dir),
                neighbourSsim: neighbours,
            };
            console.log(name, JSON.stringify(report[name]));
        }
    } finally {
        await browser.close();
        server.close();
    }
    // One entry per composition, which is all the app bundles; what this run
    // did not render keeps the entry it had (see recorder/markers.ts).
    const existing = fs.existsSync(markersFile)
        ? JSON.parse(fs.readFileSync(markersFile, 'utf8'))
        : {};
    const byComposition = mergeMarkers(existing, markers);
    fs.mkdirSync(path.dirname(markersFile), { recursive: true });
    // Written as the repository formats it, so a run that changes nothing
    // leaves no diff.
    const json = await format(JSON.stringify(byComposition, null, 2), {
        ...(await resolveConfig(markersFile)),
        filepath: markersFile,
    });
    fs.writeFileSync(markersFile, json);
    // A markers-only run has no sizes or seams to report: keep the last render's.
    if (!process.env.AUTH_ROOM_MARKERS_ONLY) {
        fs.writeFileSync(path.join(work, 'report.json'), JSON.stringify(report, null, 2));
    }
}

await main();
