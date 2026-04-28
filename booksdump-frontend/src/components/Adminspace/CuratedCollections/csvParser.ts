// Parser for CSV file uploads and free-form textarea pastes that admins use to
// build a curated collection. Both inputs collapse into the same ParsedItem shape
// before being sent to the server.

export interface ParsedItem {
    title: string;
    author: string;
    year?: number;
}

export interface ParseResult {
    items: ParsedItem[];
    errors: string[];
}

// parseCsvLine splits one CSV line respecting quoted values and `""` escapes.
function parseCsvLine(line: string): string[] {
    const out: string[] = [];
    let cur = '';
    let inQuotes = false;
    for (let i = 0; i < line.length; i++) {
        const ch = line[i];
        if (inQuotes) {
            if (ch === '"' && line[i + 1] === '"') {
                cur += '"';
                i++;
                continue;
            }
            if (ch === '"') {
                inQuotes = false;
                continue;
            }
            cur += ch;
            continue;
        }
        if (ch === ',') {
            out.push(cur);
            cur = '';
            continue;
        }
        if (ch === '"' && cur === '') {
            inQuotes = true;
            continue;
        }
        cur += ch;
    }
    out.push(cur);
    return out.map((s) => s.trim());
}

// parseCsv accepts a CSV with a strict header `title,author,year` (year optional).
// Quoted values, escaped quotes (`""` → `"`) and \r\n line endings are supported.
// Rows with an empty title or author are silently skipped — these are common in
// scraped data (anthologies / multi-volume sets / conference proceedings) and
// would only spam the warning panel, not give the admin anything actionable.
export function parseCsv(text: string): ParseResult {
    const items: ParsedItem[] = [];
    const errors: string[] = [];

    const trimmed = text.replace(/^﻿/, '').replace(/\r\n/g, '\n').trim();
    if (trimmed === '') {
        return { items, errors };
    }

    const lines = trimmed.split('\n').filter((l) => l.trim() !== '');
    if (lines.length === 0) {
        return { items, errors };
    }

    const header = parseCsvLine(lines[0]).map((h) => h.toLowerCase());
    const titleIdx = header.indexOf('title');
    const authorIdx = header.indexOf('author');
    const yearIdx = header.indexOf('year');

    if (titleIdx < 0 || authorIdx < 0) {
        errors.push('csv header must contain "title" and "author" columns');
        return { items, errors };
    }

    for (let i = 1; i < lines.length; i++) {
        const cols = parseCsvLine(lines[i]);
        const title = (cols[titleIdx] ?? '').trim();
        const author = (cols[authorIdx] ?? '').trim();
        if (!title || !author) {
            // Silent skip — see header comment.
            continue;
        }
        const item: ParsedItem = { title, author };
        if (yearIdx >= 0) {
            const raw = (cols[yearIdx] ?? '').trim();
            if (raw !== '') {
                const y = parseInt(raw, 10);
                if (!Number.isNaN(y)) {
                    item.year = y;
                }
            }
        }
        items.push(item);
    }

    return { items, errors };
}

// detectSeparator picks the most common candidate from `;`, `\t`, `,`. Lines without
// any candidate are not considered. Falls back to `;` if nothing is found.
function detectSeparator(lines: string[]): string {
    const candidates = [';', '\t', ','];
    let bestSep = ';';
    let bestCount = 0;
    for (const sep of candidates) {
        let count = 0;
        for (const line of lines) {
            if (line.includes(sep)) count++;
        }
        if (count > bestCount) {
            bestCount = count;
            bestSep = sep;
        }
    }
    return bestSep;
}

// parseTextarea auto-detects the column separator (`;`, `\t`, `,`) and parses
// each non-empty line as `Author<sep>Title` or `Author<sep>Title<sep>Year`.
// Empty / whitespace-only lines are silently skipped; lines without a separator
// produce an error but do not abort parsing.
export function parseTextarea(text: string): ParseResult {
    const items: ParsedItem[] = [];
    const errors: string[] = [];

    const trimmed = text.replace(/^﻿/, '').replace(/\r\n/g, '\n').trim();
    if (trimmed === '') {
        return { items, errors };
    }
    const lines = trimmed.split('\n').filter((l) => l.trim() !== '');
    if (lines.length === 0) {
        return { items, errors };
    }

    const sep = detectSeparator(lines);

    lines.forEach((line, idx) => {
        if (!line.includes(sep)) {
            errors.push(`line ${idx + 1}: missing separator "${sep === '\t' ? '\\t' : sep}"`);
            return;
        }
        const cols = line.split(sep).map((s) => s.trim());
        const author = cols[0] ?? '';
        const title = cols[1] ?? '';
        if (!author || !title) {
            // Silent skip — incomplete pasted lines are not actionable warnings.
            return;
        }
        const item: ParsedItem = { author, title };
        if (cols.length >= 3) {
            const y = parseInt(cols[2], 10);
            if (!Number.isNaN(y)) {
                item.year = y;
            }
        }
        items.push(item);
    });

    return { items, errors };
}
