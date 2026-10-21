import type { Passage } from './text';

/**
 * Book typesetting onto a page texture: running head, chapter heading, a
 * justified text block, the folio.
 *
 * The line breaking is separate from the drawing and takes the measure as a
 * function, so it can be checked with a fixed-width stand-in for the font.
 */

/** A page texture, in pixels: A-series proportions, as the sheet. */
export const TEX_W = 768;
export const TEX_H = 1086;

/** The text block. */
export const BODY = {
    left: 78,
    width: TEX_W - 78 * 2,
    top: 110,
    /** Lines whose baseline would fall below this are not set. */
    bottom: TEX_H - 100,
    lineHeight: 38,
    indent: 36,
} as const;

/** Paragraph break in a word stream. */
const PARA = '¶';

/**
 * The words of a page: the passage over and over, a paragraph apiece, until
 * there is more than a page can hold. The verso of a sheet starts seventeen
 * words in — the back of a page continues the front, it does not start again.
 */
export function wordStream(body: string, isBack: boolean): string[] {
    const words: string[] = [];
    while (words.length < 400) words.push(...body.split(' '), PARA);
    if (isBack) words.splice(0, 17);
    return words;
}

export interface PlacedWord {
    word: string;
    x: number;
    /** Baseline. */
    y: number;
    /** Set on every word of a line spread to both margins. */
    justified: boolean;
}

/**
 * Break the words into lines and place them. Full lines are justified: the
 * spare width goes into the gaps between words. The last line of a paragraph
 * is set with ordinary spaces, and the first line of each paragraph is
 * indented — except where the page opens mid-paragraph.
 */
export function setBody(
    words: readonly string[],
    measure: (text: string) => number,
    indentFirst: boolean,
    top: number = BODY.top,
): PlacedWord[] {
    const placed: PlacedWord[] = [];
    let y = top;
    let line: string[] = [];
    let first = indentFirst;

    const flush = (justify: boolean) => {
        const indent = first ? BODY.indent : 0;
        const textW = line.reduce((s, w) => s + measure(w), 0);
        const gaps = line.length - 1;
        const space = justify && gaps > 0 ? (BODY.width - indent - textW) / gaps : measure(' ');
        let x = BODY.left + indent;
        for (const word of line) {
            placed.push({ word, x, y, justified: justify && gaps > 0 });
            x += measure(word) + space;
        }
        y += BODY.lineHeight;
        line = [];
        first = false;
    };

    for (const w of words) {
        if (y > BODY.bottom) break;
        if (w === PARA) {
            if (line.length) flush(false);
            first = true;
            continue;
        }
        const indent = first ? BODY.indent : 0;
        if (line.length && measure([...line, w].join(' ')) + indent > BODY.width) flush(true);
        line.push(w);
    }
    return placed;
}

/**
 * Typeset one side of a page with the context's current fill. Each word gets a
 * slightly different ink density, as with real letterpress.
 */
export function typeset(
    g: CanvasRenderingContext2D,
    passage: Passage,
    folio: number,
    isBack: boolean,
    random: () => number = Math.random,
): void {
    g.textBaseline = 'alphabetic';
    g.globalAlpha = 0.9;
    g.font = 'italic 20px "PT Serif"';
    g.textAlign = 'center';
    g.fillText(passage.title, TEX_W / 2, 62);
    g.font = '20px "PT Serif"';
    g.fillText(String(folio + (isBack ? 1 : 0)), TEX_W / 2, TEX_H - 50);

    let top: number = BODY.top;
    if (passage.heading && !isBack) {
        g.font = 'bold 30px "PT Serif"';
        g.fillText(passage.heading, TEX_W / 2, top + 40);
        top += 110;
    }
    g.textAlign = 'left';

    g.font = '25px "PT Serif"';
    const measure = (s: string) => g.measureText(s).width;
    for (const w of setBody(wordStream(passage.body, isBack), measure, !isBack, top)) {
        g.globalAlpha = 0.78 + random() * 0.2;
        g.fillText(w.word, w.x, w.y);
    }
    g.globalAlpha = 1;
}
