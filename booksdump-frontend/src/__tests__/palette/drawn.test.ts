import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

import { cn } from '@/shared/lib/utils';
import { buttonVariants } from '@/shared/ui/button';

import { ratio } from './contrast';
import { BODY, OUTLINE, dark, light, room, root, tintOf } from './tokens';

// Colours as the components draw them rather than as the tokens declare
// them: a class like `ring-focus` or `hover:bg-destructive/50` is read out of
// the source, resolved against the theme, given its opacity and laid over the
// surface it sits on.

type Theme = Record<string, string>;
const themes: [string, Theme][] = [
    ['light', light],
    ['dark', dark],
];

/** Tailwind palette colours a component may name directly. */
const PALETTE: Record<string, string> = { 'green-300': '#86efac' };

/** `destructive/60` in a theme: the token, or a Tailwind colour, at its opacity. */
function colourOf(name: string, t: Theme): string {
    const [, token, percent] = /^([a-z][\w-]*?)(?:\/(\d+))?$/.exec(name)!;
    const base = t[`--${token}`] ?? PALETTE[token];
    if (!base) throw new Error(`no colour for ${name}`);
    return percent ? tintOf(base, Number(percent) / 100) : base;
}

/**
 * The class that wins for one utility among a variant's classes, given the
 * variants in force. Every variant but `dark` adds to the selector's
 * specificity (`:hover`, `[aria-invalid]`, `a`), while `dark` is a `:where()`
 * and adds nothing — so `hover:bg-x` beats `dark:bg-y` in the dark theme.
 * Between equals, the rule Tailwind emits later wins: the one with `dark`.
 */
function winner(classes: string[], utility: string, active: Set<string>): string {
    let best: { name: string; weight: number } | null = null;
    for (const c of classes) {
        const parts = c.split(':');
        const last = parts.pop()!;
        if (!last.startsWith(`${utility}-`)) continue;
        if (!parts.every((v) => active.has(v))) continue;
        const specific = parts.filter((v) => v !== 'dark').length;
        const weight = specific * 2 + (parts.includes('dark') ? 1 : 0);
        if (!best || weight >= best.weight) {
            best = { name: last.slice(utility.length + 1), weight };
        }
    }
    if (!best) throw new Error(`no ${utility} class among ${classes.join(' ')}`);
    return best.name;
}

const source = (path: string) => readFileSync(join(root, path), 'utf8');

/**
 * The backgrounds a control can find itself on beyond the bare surfaces: every
 * stateful fill the shared components paint — a hovered or selected table
 * row, a highlighted menu item, an open trigger, a hovered button — as
 * written in src/shared/ui. A translucent fill inherits all of them, so a
 * label on one is measured over each.
 */
function underlays(theme: string, exclude: string): { name: string; fill: string }[] {
    const found = new Map<string, { name: string; fill: string }>();
    const dir = join(root, 'src/shared/ui');
    for (const entry of readdirSync(dir)) {
        if (!entry.endsWith('.tsx')) continue;
        let text = readFileSync(join(dir, entry), 'utf8');
        text = text.replace(exclude, '');
        for (const m of text.matchAll(
            /(?<=^|[\s"'`])((?:[^\s"'`:]+:)+)bg-([a-z][\w-]*(?:\/\d+)?)(?=[\s"'`])/g,
        )) {
            const variants = m[1].split(':').filter(Boolean);
            // A pseudo-element or the text selection is not something a
            // control sits on.
            if (
                variants.some((v) =>
                    ['file', 'selection', 'after', 'before', 'placeholder'].includes(v),
                )
            ) {
                continue;
            }
            if (variants.includes('dark') && theme !== 'dark') continue;
            if (m[2].startsWith('gradient') || m[2] === 'transparent') continue;
            const name = `${entry}: ${m[1]}bg-${m[2]}`;
            found.set(name, { name, fill: m[2] });
        }
    }
    return [...found.values()];
}

describe.each([
    // A badge only hovers when it is a link, which its [a&] variant says.
    ['button', 'src/shared/ui/button.tsx', ['hover']],
    ['badge', 'src/shared/ui/badge.tsx', ['[a&]', 'hover']],
])('the destructive %s label', (_, path, hover) => {
    const variant = /destructive:\s*"([^"]+)"/.exec(source(path))![1];
    const classes = variant.split(/\s+/);

    describe.each(themes)('in the %s theme', (theme, t) => {
        const surfaces = {
            page: t['--background'],
            card: t['--card'],
            popover: t['--popover'],
        };
        const below = underlays(theme, variant);

        it('is measured over the shared hover and selected fills', () => {
            expect(below.some((u) => u.name.startsWith('table.tsx: hover:bg-muted'))).toBe(true);
            expect(below.length).toBeGreaterThan(10);
        });

        it.each(
            ['at rest', 'under the pointer'].flatMap((state) =>
                Object.entries(surfaces).map(([on, surface]) => [state, on, surface]),
            ),
        )('clears 4.5:1 %s on the %s, bare or under any of them', (state, _on, surface) => {
            const active = new Set<string>(theme === 'dark' ? ['dark'] : []);
            if (state === 'under the pointer') hover.forEach((v) => active.add(v));
            const fill = colourOf(winner(classes, 'bg', active), t);
            const label = colourOf(winner(classes, 'text', active), t);
            const failures = [{ name: 'bare', fill: null as string | null }, ...below]
                .map((u) => {
                    const ground = u.fill ? [surface, colourOf(u.fill, t)] : [surface];
                    return [u.name, ratio(label, ...ground, fill)] as const;
                })
                .filter(([, r]) => r < BODY)
                .map(([name, r]) => `${name} ${r.toFixed(2)}:1`);
            expect(failures).toEqual([]);
        });
    });
});

// Everything that draws a focus ring or an invalid field's ring, as written
// in the source. The header, footer and bottom bar are dark in both themes,
// so a ring there is measured on them; every other ring on the page, a card
// and a popover.
const CHROME = ['Header.tsx', 'ThemeToggle.tsx', 'BottomNavigation.tsx', 'Footer.tsx'];

function ringsInSource(): { file: string; variants: string[]; name: string }[] {
    const found: { file: string; variants: string[]; name: string }[] = [];
    const walk = (dir: string) => {
        for (const entry of readdirSync(dir)) {
            const path = join(dir, entry);
            if (statSync(path).isDirectory()) {
                walk(path);
            } else if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry)) {
                const text = readFileSync(path, 'utf8');
                for (const m of text.matchAll(
                    /(?<=^|[\s"'`])((?:[^\s"'`:]+:)*)ring-([a-z][\w-]*(?:\/\d+)?)(?=[\s"'`])/g,
                )) {
                    if (m[2].startsWith('offset')) continue;
                    found.push({
                        file: relative(root, path),
                        variants: m[1].split(':').filter(Boolean),
                        name: m[2],
                    });
                }
            }
        }
    };
    walk(join(root, 'src'));
    return found;
}

describe('focus rings as drawn', () => {
    const rings = ringsInSource();

    it('are found where the controls draw them', () => {
        expect(rings.length).toBeGreaterThan(20);
        expect(rings.some((r) => r.file.endsWith('ui/button.tsx'))).toBe(true);
        expect(rings.some((r) => r.file.endsWith('ui/input.tsx'))).toBe(true);
    });

    const cases = rings.flatMap((r) => {
        const chrome = CHROME.some((f) => r.file.endsWith(`layout/${f}`));
        return themes
            .filter(([theme]) => theme === 'dark' || !r.variants.includes('dark'))
            .flatMap(([theme, t]) =>
                (chrome
                    ? [
                          ['the header', t['--chrome']],
                          ['the footer and bottom bar', t['--chrome-raised']],
                      ]
                    : [
                          ['the page', t['--background']],
                          ['a card', t['--card']],
                          ['a popover', t['--popover']],
                      ]
                ).map(([on, surface]) => [
                    `${r.file}: ${[...r.variants, `ring-${r.name}`].join(':')} (${theme}) on ${on}`,
                    colourOf(r.name, t),
                    surface,
                ]),
            );
    });

    it.each(cases)('%s clears 3:1', (_, ring, surface) => {
        expect(ratio(ring, surface)).toBeGreaterThanOrEqual(OUTLINE);
    });

    // A ghost button in the header takes the shared ring unless it says
    // otherwise, and the shared one is drawn for the page, not the header.
    it('give every button in the header its own ring, and keep it over the shared one', () => {
        for (const path of ['src/shared/layout/Header.tsx', 'src/shared/layout/ThemeToggle.tsx']) {
            const buttons = source(path).match(/<Button\b[\s\S]*?<\/Button>/g) ?? [];
            expect(buttons.length).toBeGreaterThan(0);
            for (const button of buttons) {
                expect(button).toMatch(/focus-visible:ring-chrome-muted\b/);
            }
        }
        const merged = cn(buttonVariants({ variant: 'ghost' }), 'focus-visible:ring-chrome-muted');
        expect(merged).toMatch(/focus-visible:ring-chrome-muted\b/);
        expect(merged).not.toMatch(/focus-visible:ring-focus\b/);
    });
});

describe('the sign-in card', () => {
    it.each([
        ['the bare room', '#171310'],
        ['the lit table', '#272019'],
    ])('draws its focus rings at 3:1 over %s', (_, behind) => {
        const card = [behind, room['--card']];
        expect(ratio(room['--focus'], ...card)).toBeGreaterThanOrEqual(OUTLINE);
        expect(ratio(room['--focus-destructive'], ...card)).toBeGreaterThanOrEqual(OUTLINE);
    });
});
