import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

import { contrast, parseColour, ratio } from './contrast';
import { BODY, OUTLINE, block, dark, light, room, root } from './tokens';

// The palette is read from index.css itself, so a later tweak to a token is
// checked against every surface its text is set on, in both themes. Each pair
// below is one the components actually draw.

type Pair = [text: string, surface: string[], min: number];

/** Every text and outline token, over every surface it is drawn on. */
function pairs(t: Record<string, string>): Record<string, Pair> {
    const page = [t['--background']];
    const card = [t['--card']];
    const popover = [t['--popover']];
    const on = (base: string[], token: string) => [...base, t[token]];
    const tint = (base: string[], token: string, percent: number) => {
        const [r, g, b, a] = parseColour(t[token]);
        return [...base, `rgba(${r}, ${g}, ${b}, ${a * (percent / 100)})`];
    };

    return {
        'foreground on the page': [t['--foreground'], page, BODY],
        'card text on a card': [t['--card-foreground'], card, BODY],
        'popover text on a popover': [t['--popover-foreground'], popover, BODY],
        'foreground on a muted panel on a card': [t['--foreground'], on(card, '--muted'), BODY],
        'secondary text on a secondary button on a card': [
            t['--secondary-foreground'],
            on(card, '--secondary'),
            BODY,
        ],
        'secondary text on a secondary button on the page': [
            t['--secondary-foreground'],
            on(page, '--secondary'),
            BODY,
        ],
        'accent text on a hovered item in a popover': [
            t['--accent-foreground'],
            on(popover, '--accent'),
            BODY,
        ],
        'accent text on a hovered item on the page': [
            t['--accent-foreground'],
            on(page, '--accent'),
            BODY,
        ],
        'secondary text on the page': [t['--muted-foreground'], page, BODY],
        'secondary text on a card': [t['--muted-foreground'], card, BODY],
        'secondary text on a popover': [t['--muted-foreground'], popover, BODY],
        'secondary text on a muted panel on a card': [
            t['--muted-foreground'],
            on(card, '--muted'),
            BODY,
        ],
        'secondary text on a muted tab list on the page': [
            t['--muted-foreground'],
            on(page, '--muted'),
            BODY,
        ],
        'a link on the page': [t['--primary'], page, BODY],
        'a link on a card': [t['--primary'], card, BODY],
        'a link in a popover': [t['--primary'], popover, BODY],
        'button text on the primary button': [t['--primary-foreground'], [t['--primary']], BODY],
        'an error on the page': [t['--destructive'], page, BODY],
        'an error on a card': [t['--destructive'], card, BODY],
        'an error in a popover': [t['--destructive'], popover, BODY],
        'an error in its tinted alert on a card': [
            t['--destructive'],
            tint(card, '--destructive', 10),
            BODY,
        ],
        "a field's outline on the page": [t['--input'], page, OUTLINE],
        "a field's outline on a card": [t['--input'], card, OUTLINE],
        "a field's outline in a popover": [t['--input'], popover, OUTLINE],
        'header text on the header': [t['--chrome-foreground'], [t['--chrome']], BODY],
        'header text on the footer and bottom bar': [
            t['--chrome-foreground'],
            [t['--chrome-raised']],
            BODY,
        ],
        'an inactive header link on the header': [t['--chrome-muted'], [t['--chrome']], BODY],
        'an inactive tab on the bottom bar': [t['--chrome-muted'], [t['--chrome-raised']], BODY],
    };
}

const themes = { light: pairs(light), dark: pairs(dark) };

describe.each(Object.entries(themes))('the %s palette', (_, table) => {
    it.each(Object.entries(table))('%s clears its WCAG AA ratio', (_, [text, surface, min]) => {
        expect(ratio(text, ...surface)).toBeGreaterThanOrEqual(min);
    });
});

describe.each([
    ['light', light],
    ['dark', dark],
])('the %s surfaces', (_, t) => {
    it('are opaque in the app: no card, popover or page lets anything show through', () => {
        for (const token of [
            '--background',
            '--card',
            '--popover',
            '--chrome',
            '--chrome-raised',
        ]) {
            expect(parseColour(t[token])[3]).toBe(1);
        }
    });

    it('raise a card visibly off the page, and lighter than it', () => {
        const page = parseColour(t['--background']);
        const card = parseColour(t['--card']);
        expect(contrast(card, page)).toBeGreaterThanOrEqual(1.1);
        expect(card.slice(0, 3).reduce((a, b) => a + b)).toBeGreaterThan(
            page.slice(0, 3).reduce((a, b) => a + b),
        );
    });

    it('make popovers, dialogs and sheets of the card', () => {
        expect(t['--popover']).toBe(t['--card']);
    });
});

describe('the sign-in screens', () => {
    it('keep their smoked glass over the video', () => {
        expect(parseColour(room['--card'])[3]).toBeLessThan(1);
    });

    // A colour the room does not set itself falls through to the app's dark
    // theme, so a later change there would quietly repaint the sign-in card.
    it('set every colour the dark theme does, so the app palette cannot leak in', () => {
        const own = block('.auth-room');
        const colours = Object.keys(block("[data-theme='dark']"));
        expect(colours.filter((token) => !(token in own))).toEqual([]);
    });

    // Their contrast was measured with the glass laid over the room's
    // darkest and brightest areas behind the card.
    it.each([
        ['the bare room', '#171310'],
        ['the lit table', '#272019'],
    ])('keep their text readable over %s', (_, behind) => {
        const card = [behind, room['--card']];
        expect(ratio(room['--card-foreground'], ...card)).toBeGreaterThanOrEqual(BODY);
        expect(ratio(room['--muted-foreground'], ...card)).toBeGreaterThanOrEqual(BODY);
        expect(ratio(room['--destructive'], ...card)).toBeGreaterThanOrEqual(BODY);
        expect(ratio(room['--input'], ...card)).toBeGreaterThanOrEqual(OUTLINE);
    });
});

describe('outside the stylesheet', () => {
    const html = readFileSync(join(root, 'index.html'), 'utf8');
    const manifest = JSON.parse(readFileSync(join(root, 'public/manifest.json'), 'utf8'));

    it("colours the browser's own bar like the header", () => {
        expect(html).toContain(`<meta name="theme-color" content="${light['--chrome']}" />`);
        expect(manifest.theme_color).toBe(light['--chrome']);
    });

    it("paints the loading screen and the installed app's splash in the page colour", () => {
        expect(html).toMatch(
            new RegExp(`\\.app-loading \\{[^}]*background-color: ${light['--background']};`),
        );
        expect(html).toMatch(
            new RegExp(
                `prefers-color-scheme: dark[^@]*\\.app-loading \\{[^}]*background-color: ${dark['--background']};`,
            ),
        );
        expect(manifest.background_color).toBe(light['--background']);
    });

    it('draws the donation QR code in the card and its text', () => {
        const source = readFileSync(join(root, 'src/shared/layout/DonateModal.tsx'), 'utf8');
        expect(source).toContain(
            `{ dark: '${dark['--card-foreground']}', light: '${dark['--card']}' }`,
        );
        expect(source).toContain(
            `{ dark: '${light['--card-foreground']}', light: '${light['--card']}' }`,
        );
    });

    // Neutral greys from Tailwind's palette bypass both themes. The sign-in
    // room is the only place that blurs what is behind a panel.
    it('leaves no neutral Tailwind grey and no backdrop blur in the app', () => {
        const offenders: string[] = [];
        const walk = (dir: string) => {
            for (const name of readdirSync(dir)) {
                const path = join(dir, name);
                if (statSync(path).isDirectory()) {
                    walk(path);
                } else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) {
                    const source = readFileSync(path, 'utf8');
                    const file = relative(root, path);
                    if (/\b(?:gray|neutral|zinc|slate|stone)-\d{2,3}\b/.test(source))
                        offenders.push(file);
                    if (/backdrop-blur/.test(source) && !file.startsWith('src/features/auth/')) {
                        offenders.push(`${file} (blur)`);
                    }
                }
            }
        };
        walk(join(root, 'src'));
        expect(offenders).toEqual([]);
    });
});
