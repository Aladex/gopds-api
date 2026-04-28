import { parseCsv, parseTextarea } from '../csvParser';

describe('parseCsv', () => {
    it('parses canonical title,author,year CSV', () => {
        const text = `title,author,year
1984,George Orwell,1949
Brave New World,Aldous Huxley,1932`;
        const { items, errors } = parseCsv(text);
        expect(errors).toHaveLength(0);
        expect(items).toEqual([
            { title: '1984', author: 'George Orwell', year: 1949 },
            { title: 'Brave New World', author: 'Aldous Huxley', year: 1932 },
        ]);
    });

    it('accepts CSV without year column', () => {
        const text = `title,author
1984,George Orwell
Brave New World,Aldous Huxley`;
        const { items, errors } = parseCsv(text);
        expect(errors).toHaveLength(0);
        expect(items).toEqual([
            { title: '1984', author: 'George Orwell' },
            { title: 'Brave New World', author: 'Aldous Huxley' },
        ]);
    });

    it('handles quoted values with commas inside', () => {
        const text = `title,author,year
"Doctor Sleep, A Novel","Stephen King",2013
"Plain Title","King, Stephen",2010`;
        const { items, errors } = parseCsv(text);
        expect(errors).toHaveLength(0);
        expect(items[0]).toEqual({ title: 'Doctor Sleep, A Novel', author: 'Stephen King', year: 2013 });
        expect(items[1]).toEqual({ title: 'Plain Title', author: 'King, Stephen', year: 2010 });
    });

    it('unescapes doubled quotes inside quoted values', () => {
        const text = `title,author
"He said ""hi""",Author`;
        const { items } = parseCsv(text);
        expect(items[0].title).toBe('He said "hi"');
    });

    it('rejects CSV with missing title or author column header', () => {
        const text = `name,who
1984,Orwell`;
        const { items, errors } = parseCsv(text);
        expect(items).toHaveLength(0);
        expect(errors.length).toBeGreaterThan(0);
    });

    it('silently skips rows with empty title or author', () => {
        const text = `title,author,year
,Orwell,1949
Brave New World,,1932
Valid,Author,2000`;
        const { items, errors } = parseCsv(text);
        expect(items).toEqual([{ title: 'Valid', author: 'Author', year: 2000 }]);
        expect(errors).toHaveLength(0);
    });

    it('handles CRLF line endings', () => {
        const text = `title,author\r\n1984,Orwell\r\n`;
        const { items, errors } = parseCsv(text);
        expect(errors).toHaveLength(0);
        expect(items).toEqual([{ title: '1984', author: 'Orwell' }]);
    });

    it('returns empty result for empty input', () => {
        expect(parseCsv('').items).toEqual([]);
        expect(parseCsv('').errors).toHaveLength(0);
    });
});

describe('parseTextarea', () => {
    it('auto-detects semicolon as separator', () => {
        const text = `George Orwell; 1984
Aldous Huxley; Brave New World`;
        const { items, errors } = parseTextarea(text);
        expect(errors).toHaveLength(0);
        expect(items).toEqual([
            { author: 'George Orwell', title: '1984' },
            { author: 'Aldous Huxley', title: 'Brave New World' },
        ]);
    });

    it('auto-detects tab as separator', () => {
        const text = `George Orwell\t1984\nAldous Huxley\tBrave New World`;
        const { items } = parseTextarea(text);
        expect(items).toEqual([
            { author: 'George Orwell', title: '1984' },
            { author: 'Aldous Huxley', title: 'Brave New World' },
        ]);
    });

    it('parses optional third column as year', () => {
        const text = `Orwell; 1984; 1949
Huxley; Brave New World; 1932`;
        const { items } = parseTextarea(text);
        expect(items).toEqual([
            { author: 'Orwell', title: '1984', year: 1949 },
            { author: 'Huxley', title: 'Brave New World', year: 1932 },
        ]);
    });

    it('skips blank and whitespace-only lines', () => {
        const text = `Orwell; 1984


Huxley; Brave New World
`;
        const { items, errors } = parseTextarea(text);
        expect(errors).toHaveLength(0);
        expect(items).toHaveLength(2);
    });

    it('reports error for lines without a separator and continues parsing', () => {
        const text = `Orwell; 1984
nope just text
Huxley; Brave New World`;
        const { items, errors } = parseTextarea(text);
        expect(items).toHaveLength(2);
        expect(errors).toHaveLength(1);
    });

    it('returns empty result for empty input', () => {
        expect(parseTextarea('').items).toEqual([]);
        expect(parseTextarea('').errors).toHaveLength(0);
    });
});
