/*
 * The scanner's read errors, sorted for the scan tab: which class each one is,
 * which belong to the newest archive, and how they group. The scanner writes
 * the author metadata failures as "author_metadata: <class>" with a closed
 * class, and an archive it cannot open as "failed to open archive: …"; any
 * other text is a single book's own error.
 */

export type ScanErrorClass =
    | 'metadata_parse_failed'
    | 'invalid_fb2'
    | 'unsupported_encoding'
    | 'entry_missing'
    | 'archive_unreadable'
    | 'other';

const AUTHOR_METADATA_PREFIX = 'author_metadata: ';
const ARCHIVE_OPEN_PREFIX = 'failed to open archive:';

const METADATA_CLASSES: ReadonlySet<string> = new Set([
    'metadata_parse_failed',
    'invalid_fb2',
    'unsupported_encoding',
    'entry_missing',
    'archive_unreadable',
]);

/** The closed class of one scanner error string; anything unknown is other. */
export function classifyScanError(error: string): ScanErrorClass {
    if (error.startsWith(AUTHOR_METADATA_PREFIX)) {
        const cls = error.slice(AUTHOR_METADATA_PREFIX.length);
        return METADATA_CLASSES.has(cls) ? (cls as ScanErrorClass) : 'other';
    }
    if (error.startsWith(ARCHIVE_OPEN_PREFIX)) {
        return 'archive_unreadable';
    }
    return 'other';
}

/** The newest archive's errors (fresh) apart from every older one (old). */
export function splitScanErrors<T extends { archive_name: string }>(
    errors: T[],
    newestArchive: string | null,
): { fresh: T[]; old: T[] } {
    const fresh: T[] = [];
    const old: T[] = [];
    for (const error of errors) {
        (newestArchive !== null && error.archive_name === newestArchive ? fresh : old).push(error);
    }
    return { fresh, old };
}

/** Errors grouped by class: the largest group first, ties by class name. */
export function groupScanErrors<T extends { error: string }>(
    errors: T[],
): { cls: ScanErrorClass; items: T[] }[] {
    const groups = new Map<ScanErrorClass, T[]>();
    for (const error of errors) {
        const cls = classifyScanError(error.error);
        const items = groups.get(cls);
        if (items === undefined) {
            groups.set(cls, [error]);
        } else {
            items.push(error);
        }
    }
    return [...groups.entries()]
        .map(([cls, items]) => ({ cls, items }))
        .sort((a, b) => b.items.length - a.items.length || a.cls.localeCompare(b.cls));
}
