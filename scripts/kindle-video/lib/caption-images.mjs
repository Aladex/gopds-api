// Renders each caption as a transparent 1280×720 PNG (the pill sits where it
// will appear on screen), so ffmpeg only has to fade and overlay them.
import fs from 'node:fs';
import path from 'node:path';

import { VIEW } from './recorder.mjs';

const FONT_DIR = path.join(import.meta.dirname, '..', 'node_modules', '@fontsource', 'onest', 'files');

function fontFaces() {
    const face = (weight, subset, range) => {
        const file = path.join(FONT_DIR, `onest-${subset}-${weight}-normal.woff2`);
        const data = fs.readFileSync(file).toString('base64');
        return `@font-face { font-family: Onest; font-weight: ${weight}; font-display: block;
            src: url(data:font/woff2;base64,${data}) format('woff2'); unicode-range: ${range}; }`;
    };
    const LATIN = 'U+0000-00FF, U+0131, U+0152-0153, U+02BB-02BC, U+02C6, U+02DA, U+02DC, U+2000-206F, U+20AC, U+2122, U+2191, U+2193, U+2212, U+2215, U+FEFF, U+FFFD, U+2190-21FF';
    const CYRILLIC = 'U+0301, U+0400-045F, U+0490-0491, U+04B0-04B1, U+2116';
    return [500, 600]
        .flatMap((w) => [face(w, 'latin', LATIN), face(w, 'cyrillic', CYRILLIC)])
        .join('\n');
}

const page = (cue) => `<!doctype html><html><head><meta charset="utf-8"><style>
${fontFaces()}
html, body { margin: 0; width: ${VIEW.width}px; height: ${VIEW.height}px; background: transparent; }
.cap {
    position: absolute; left: 50%; transform: translateX(-50%);
    ${cue.position === 'top' ? 'top: 84px;' : 'bottom: 30px;'}
    display: flex; align-items: center; gap: 14px; max-width: 1120px;
    padding: 12px 24px 12px 12px; border-radius: 16px;
    background: rgba(18, 20, 24, .88); box-shadow: 0 6px 24px rgba(0, 0, 0, .28);
    font-family: Onest, sans-serif; color: #fff; white-space: nowrap;
}
.step {
    flex: none; width: 34px; height: 34px; border-radius: 50%;
    display: grid; place-items: center;
    background: #f5a524; color: #1b1300; font-weight: 600; font-size: 18px;
}
.cap.plain { padding-left: 24px; }
.text { font-weight: 500; font-size: 26px; line-height: 1.25; letter-spacing: -0.005em; }
</style></head><body><div class="cap${cue.step ? '' : ' plain'}">${cue.step ? `<span class="step">${cue.step}</span>` : ''}<span class="text"></span></div>
<script>document.querySelector('.text').textContent = ${JSON.stringify(cue.text)};</script>
</body></html>`;

/** Writes one PNG per cue into `dir`; returns the cues with `.png` set. */
export async function renderCaptions(browser, cues, dir) {
    fs.mkdirSync(dir, { recursive: true });
    const p = await browser.newPage({ viewport: VIEW, deviceScaleFactor: 1 });
    const out = [];
    for (const [i, cue] of cues.entries()) {
        await p.setContent(page(cue));
        await p.evaluate(() => document.fonts.ready);
        const width = await p.locator('.cap').evaluate((el) => el.getBoundingClientRect().width);
        if (width > VIEW.width - 60) throw new Error(`caption too wide (${Math.round(width)}px): ${cue.text}`);
        const png = path.join(dir, `cap-${String(i).padStart(2, '0')}.png`);
        await p.screenshot({ path: png, omitBackground: true });
        out.push({ ...cue, png });
    }
    await p.close();
    return out;
}
