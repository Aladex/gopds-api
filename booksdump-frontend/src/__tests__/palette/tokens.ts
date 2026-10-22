import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { parseColour } from './contrast';

// The palette as index.css declares it, for the tests in this folder.

export const root = join(dirname(fileURLToPath(import.meta.url)), '../../..');
const css = readFileSync(join(root, 'src/index.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');

export function block(selector: string): Record<string, string> {
    const start = css.indexOf(`\n${selector} {`);
    if (start < 0) throw new Error(`no ${selector} block in index.css`);
    const body = css.slice(css.indexOf('{', start) + 1, css.indexOf('}', start));
    return Object.fromEntries(
        [...body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]),
    );
}

export const light = block(':root');
export const dark = { ...light, ...block("[data-theme='dark']") };
export const room = { ...dark, ...block('.auth-room') };

export const BODY = 4.5;
export const OUTLINE = 3;

/** A colour at a fraction of its own opacity, as Tailwind's `/50` draws it. */
export function tintOf(colour: string, alpha: number): string {
    const [r, g, b, a] = parseColour(colour);
    return `rgba(${r}, ${g}, ${b}, ${a * alpha})`;
}
