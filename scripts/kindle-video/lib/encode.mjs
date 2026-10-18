// ffmpeg side: recorded frames → one 30 fps clip per scene (with the zoom
// applied), then the scenes cross-faded, captions overlaid and the result
// encoded to the delivery spec.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

import { SCALE, VIEW } from './recorder.mjs';

export const FPS = 30;
export const XFADE = 0.5;
const FADE = 0.25;
export const MAX_BYTES = 4_000_000;

function ffmpeg(args) {
    execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...args], { stdio: 'inherit' });
}

/**
 * A piecewise expression of `it` (input time): holds each value, then eases
 * to the next one over the event's duration with a smoothstep.
 */
function piecewise(steps, initial) {
    let expr = steps.length ? steps.at(-1).v.toFixed(3) : initial.toFixed(3);
    for (let i = steps.length - 1; i >= 0; i--) {
        const prev = i === 0 ? initial : steps[i - 1].v;
        const { t, d, v } = steps[i];
        const p = `((it-${t.toFixed(3)})/${d.toFixed(3)})`;
        const lerp = `(${prev.toFixed(3)}+(${(v - prev).toFixed(3)})*${p}*${p}*(3-2*${p}))`;
        expr = `if(lt(it,${t.toFixed(3)}),${prev.toFixed(3)},if(lt(it,${(t + d).toFixed(3)}),${lerp},${expr}))`;
    }
    return expr;
}

/** Renders one recorded scene to an intermediate clip. */
export function renderScene(dir, meta, out) {
    const lines = ['ffconcat version 1.0'];
    meta.frames.forEach((f, i) => {
        const next = i + 1 < meta.frames.length ? meta.frames[i + 1].t : meta.duration;
        lines.push(`file ${f.file}`, `duration ${Math.max(0.001, next - f.t).toFixed(4)}`);
    });
    lines.push(`file ${meta.frames.at(-1).file}`);
    const list = path.join(dir, 'frames.ffconcat');
    fs.writeFileSync(list, lines.join('\n') + '\n');

    const W = VIEW.width * SCALE;
    const H = VIEW.height * SCALE;
    const zooms = meta.events.filter((e) => e.kind === 'zoom');
    const z = piecewise(
        zooms.map((e) => ({ t: e.t, d: e.dur, v: e.scale })),
        1,
    );
    const cx = piecewise(
        zooms.map((e) => ({ t: e.t, d: e.dur, v: e.cx * SCALE })),
        W / 2,
    );
    const cy = piecewise(
        zooms.map((e) => ({ t: e.t, d: e.dur, v: e.cy * SCALE })),
        H / 2,
    );
    const graph =
        `[0:v]scale=${W}:${H}:in_range=pc:out_range=tv,format=yuv420p,fps=${FPS},` +
        `zoompan=z='${z}':x='clip(${cx}-iw/zoom/2,0,iw-iw/zoom)':y='clip(${cy}-ih/zoom/2,0,ih-ih/zoom)'` +
        `:d=1:fps=${FPS}:s=${VIEW.width}x${VIEW.height},format=yuv420p[v]`;
    const script = path.join(dir, 'zoom.filter');
    fs.writeFileSync(script, graph);
    ffmpeg([
        '-f', 'concat', '-safe', '0', '-i', list,
        '-/filter_complex', script, '-map', '[v]',
        '-t', meta.duration.toFixed(3),
        '-c:v', 'libx264', '-preset', 'veryfast', '-crf', '10', '-r', String(FPS),
        out,
    ]);
}

/**
 * Lays the scenes end to end with cross-fades and works out where every
 * caption lands on the joined timeline.
 */
export function timeline(scenes) {
    let offset = 0;
    const cues = [];
    const marks = {};
    scenes.forEach((s, i) => {
        const last = i === scenes.length - 1;
        const sceneEnd = s.meta.duration - (last ? 0.15 : XFADE);
        const caps = s.meta.events.filter((e) => e.kind === 'caption');
        caps.forEach((c, k) => {
            const end = k + 1 < caps.length ? caps[k + 1].t : sceneEnd;
            cues.push({ text: c.text, step: c.step, position: c.position, start: offset + c.t, end: offset + end });
        });
        for (const m of s.meta.events.filter((e) => e.kind === 'mark')) marks[m.name] = offset + m.t;
        s.offset = offset;
        offset += s.meta.duration - (last ? 0 : XFADE);
    });
    return { cues, marks, duration: offset };
}

/** Joins the scene clips, overlays the caption PNGs, encodes at `crf`. */
export function assemble(scenes, cues, out, crf) {
    const inputs = [];
    const parts = [];
    scenes.forEach((s) => inputs.push('-i', s.clip));
    let v = '[0:v]';
    for (let i = 1; i < scenes.length; i++) {
        const off = scenes[i].offset;
        parts.push(`${v}[${i}:v]xfade=transition=fade:duration=${XFADE}:offset=${off.toFixed(3)}[x${i}]`);
        v = `[x${i}]`;
    }
    cues.forEach((c, k) => {
        const idx = scenes.length + k;
        const dur = c.end - c.start;
        inputs.push('-loop', '1', '-framerate', String(FPS), '-t', dur.toFixed(3), '-i', c.png);
        parts.push(
            `[${idx}:v]format=rgba,fade=t=in:st=0:d=${FADE}:alpha=1,` +
                `fade=t=out:st=${(dur - FADE).toFixed(3)}:d=${FADE}:alpha=1,` +
                `setpts=PTS-STARTPTS+${c.start.toFixed(3)}/TB[c${k}]`,
        );
        parts.push(`${v}[c${k}]overlay=0:0:eof_action=pass[o${k}]`);
        v = `[o${k}]`;
    });
    parts.push(`${v}format=yuv420p[out]`);
    const script = `${out}.filter`;
    fs.writeFileSync(script, parts.join(';\n'));
    ffmpeg([
        ...inputs,
        '-/filter_complex', script, '-map', '[out]',
        '-an', '-r', String(FPS),
        '-c:v', 'libx264', '-profile:v', 'high', '-pix_fmt', 'yuv420p', '-color_range', 'tv',
        '-preset', 'slow', '-tune', 'animation', '-crf', String(crf),
        '-movflags', '+faststart',
        out,
    ]);
    fs.rmSync(script);
    return fs.statSync(out).size;
}

export function frameAt(video, seconds, out, { width } = {}) {
    ffmpeg([
        '-ss', seconds.toFixed(3), '-i', video, '-frames:v', '1',
        ...(width ? ['-vf', `scale=${width}:-2`] : []),
        '-q:v', '3', out,
    ]);
}

/** A grid of keyframes, one per caption, for reviewing the cut at a glance. */
export function contactSheet(video, cues, out, tmp) {
    fs.rmSync(tmp, { recursive: true, force: true });
    fs.mkdirSync(tmp, { recursive: true });
    cues.forEach((c, i) => {
        const t = c.start + Math.min(2.2, (c.end - c.start) * 0.6);
        frameAt(video, t, path.join(tmp, `k${String(i).padStart(2, '0')}.png`), { width: 640 });
    });
    const cols = 3;
    const rows = Math.ceil(cues.length / cols);
    ffmpeg([
        '-framerate', '1', '-i', path.join(tmp, 'k%02d.png'),
        '-vf', `tile=${cols}x${rows}:padding=6:margin=6:color=white`,
        '-frames:v', '1', out,
    ]);
}

export function probeDuration(file) {
    return Number(
        execFileSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', file])
            .toString()
            .trim(),
    );
}
