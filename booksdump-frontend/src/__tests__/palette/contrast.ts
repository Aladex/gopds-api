// WCAG 2 contrast arithmetic for the palette test, with alpha compositing:
// several tokens are translucent and only have a colour once laid over the
// surface beneath them.

export type Rgba = [number, number, number, number];

export function parseColour(value: string): Rgba {
    const v = value.trim().toLowerCase();
    const hex = /^#([0-9a-f]{6})$/.exec(v);
    if (hex) {
        const n = parseInt(hex[1], 16);
        return [(n >> 16) & 255, (n >> 8) & 255, n & 255, 1];
    }
    const rgb = /^rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*(?:,\s*([\d.]+)\s*)?\)$/.exec(
        v,
    );
    if (rgb) {
        return [
            Number(rgb[1]),
            Number(rgb[2]),
            Number(rgb[3]),
            rgb[4] === undefined ? 1 : Number(rgb[4]),
        ];
    }
    throw new Error(`not a colour this test understands: ${value}`);
}

/** Lays `top` over `bottom`; the result is opaque when `bottom` is. */
export function over(top: Rgba, bottom: Rgba): Rgba {
    const a = top[3] + bottom[3] * (1 - top[3]);
    const mix = (i: number) => (top[i] * top[3] + bottom[i] * bottom[3] * (1 - top[3])) / a;
    return [mix(0), mix(1), mix(2), a];
}

function luminance([r, g, b]: Rgba): number {
    const lin = (c: number) => {
        const s = c / 255;
        return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
    };
    return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

export function contrast(a: Rgba, b: Rgba): number {
    if (a[3] < 1 || b[3] < 1) throw new Error('composite translucent colours before comparing');
    const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
    return (hi + 0.05) / (lo + 0.05);
}

/** The ratio of `text` laid over `surface`, both given as a stack bottom-up. */
export function ratio(text: string, ...surface: string[]): number {
    const ground = surface.map(parseColour).reduce((below, layer) => over(layer, below));
    return contrast(over(parseColour(text), ground), ground);
}
