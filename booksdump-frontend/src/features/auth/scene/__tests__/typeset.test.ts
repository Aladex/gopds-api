import { BODY, setBody, wordStream } from '@/features/auth/scene/typeset';

/*
 * The pages are typeset the way a book is: justified, first lines of
 * paragraphs indented, the verso picking up mid-text. A fixed-width measure
 * stands in for the font, so the arithmetic is exact.
 */

const measure = (s: string) => s.length * 10;
const body = 'Один два три четыре пять шесть семь восемь девять десять одиннадцать двенадцать.';

describe('setBody', () => {
    const placed = setBody(wordStream(body, false), measure, true);
    const lines = new Map<number, typeof placed>();
    for (const w of placed) lines.set(w.y, [...(lines.get(w.y) ?? []), w]);
    const rows = [...lines.values()];

    it('fills the page and stops above the folio', () => {
        expect(rows.length).toBeGreaterThan(10);
        for (const w of placed) expect(w.y).toBeLessThanOrEqual(BODY.bottom + BODY.lineHeight);
    });

    it('never sets a line wider than the measure', () => {
        for (const row of rows) {
            const last = row[row.length - 1];
            expect(last.x + measure(last.word)).toBeLessThanOrEqual(BODY.left + BODY.width + 1e-9);
        }
    });

    it('justifies full lines flush to both margins', () => {
        // A justified line is one followed by more of the same paragraph: it
        // ends exactly on the right margin.
        const justified = rows.filter((row) => row[row.length - 1].justified);
        expect(justified.length).toBeGreaterThan(5);
        for (const row of justified) {
            const last = row[row.length - 1];
            expect(last.x + measure(last.word)).toBeCloseTo(BODY.left + BODY.width, 6);
        }
    });

    it('indents the first line of every paragraph on the recto', () => {
        expect(rows[0][0].x).toBe(BODY.left + BODY.indent);
        const afterBreak = rows.findIndex(
            (row, i) => i > 0 && rows[i - 1].at(-1)!.word.endsWith('.'),
        );
        expect(rows[afterBreak][0].x).toBe(BODY.left + BODY.indent);
    });
});

describe('wordStream', () => {
    it('repeats the passage with a paragraph break between copies', () => {
        const words = wordStream(body, false);
        expect(words.length).toBeGreaterThanOrEqual(400);
        expect(words).toContain('¶');
        expect(words[0]).toBe('Один');
    });

    it('lets the verso pick up mid-text, as the back of a page does', () => {
        const recto = wordStream(body, false);
        const verso = wordStream(body, true);
        expect(verso[0]).toBe(recto[17]);
    });

    it('starts the verso without an indent: it continues a paragraph', () => {
        const placed = setBody(wordStream(body, true), measure, false);
        expect(placed[0].x).toBe(BODY.left);
    });
});
