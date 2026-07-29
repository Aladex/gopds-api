import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

// The application hides itself while it starts, so nobody watches it assemble.
// That is worth doing and easy to get wrong in a way no other test would catch:
// hide the page on a condition that only JavaScript can lift, and a visitor
// without JavaScript gets a blank white screen — including the <noscript> line
// that exists to tell them why.
//
// It had exactly that shape. A bundled stylesheet hid `html` outright and
// waited for a class an inline script adds on DOMContentLoaded, so with
// scripting off nothing ever removed it. These guard the invariant rather than
// the file that broke it.

const here = dirname(fileURLToPath(import.meta.url));
const read = (path: string) => readFileSync(resolve(here, '../..', path), 'utf8');

const indexHtml = read('index.html');
const indexCss = read('src/index.css');

/** Strips comments, so a rule quoted in prose does not read as a live one. */
const withoutComments = (css: string) =>
    css.replace(/\/\*[\s\S]*?\*\//g, '').replace(/<!--[\s\S]*?-->/g, '');

describe('page visibility while starting up', () => {
    it('never hides the document unconditionally', () => {
        for (const [name, source] of [
            ['index.html', indexHtml],
            ['index.css', indexCss],
        ] as const) {
            // `html { visibility: hidden }` with no qualifier on the selector.
            const unconditional = /(^|[},])\s*html\s*\{[^}]*visibility\s*:\s*hidden/i;
            expect(
                withoutComments(source),
                `${name} hides html without a condition JavaScript-free browsers can meet`,
            ).not.toMatch(unconditional);
        }
    });

    // The mechanism that is allowed to hide it: a class the inline script adds,
    // which therefore never appears when scripting is off.
    it('hides the document only while the no-js class is on it', () => {
        const html = withoutComments(indexHtml);
        expect(html).toMatch(/html\.no-js\s*\{[^}]*visibility\s*:\s*hidden/i);
        expect(html).toMatch(/html\.loaded\s*\{[^}]*visibility\s*:\s*visible/i);
    });

    it('adds that class from a script, so it cannot appear without one', () => {
        expect(indexHtml).toMatch(/documentElement\.className\s*=\s*'no-js'/);
    });

    it('removes it on an event that always arrives', () => {
        expect(indexHtml).toMatch(/DOMContentLoaded/);
        expect(indexHtml).toMatch(/documentElement\.className\s*=\s*'js loaded'/);
    });

    it('keeps a message for a visitor without JavaScript', () => {
        expect(indexHtml).toMatch(/<noscript>[^<]*\S[^<]*<\/noscript>/);
    });
});
