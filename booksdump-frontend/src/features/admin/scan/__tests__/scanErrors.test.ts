import {
    classifyScanError,
    groupScanErrors,
    splitScanErrors,
} from '@/features/admin/scan/scanErrors';

describe('classifyScanError', () => {
    it.each([
        ['author_metadata: metadata_parse_failed', 'metadata_parse_failed'],
        ['author_metadata: invalid_fb2', 'invalid_fb2'],
        ['author_metadata: unsupported_encoding', 'unsupported_encoding'],
        ['author_metadata: entry_missing', 'entry_missing'],
        ['failed to open archive: unexpected EOF', 'archive_unreadable'],
        ['author_metadata: weird', 'other'],
        ['author_metadata:metadata_parse_failed', 'other'],
        ['boom', 'other'],
        ['', 'other'],
    ])('%s → %s', (error, expected) => {
        expect(classifyScanError(error)).toBe(expected);
    });
});

describe('splitScanErrors', () => {
    const errors = [
        { archive_name: 'new.zip', file_name: '1.fb2' },
        { archive_name: 'old.zip', file_name: '2.fb2' },
        { archive_name: 'new.zip', file_name: '3.fb2' },
    ];

    it('sends only the newest archive to fresh', () => {
        const { fresh, old } = splitScanErrors(errors, 'new.zip');
        expect(fresh.map((e) => e.file_name)).toEqual(['1.fb2', '3.fb2']);
        expect(old.map((e) => e.file_name)).toEqual(['2.fb2']);
    });

    it('keeps everything old without a newest archive', () => {
        const { fresh, old } = splitScanErrors(errors, null);
        expect(fresh).toEqual([]);
        expect(old).toHaveLength(3);
    });
});

describe('groupScanErrors', () => {
    it('groups by class, largest first, keeping the order inside a group', () => {
        // The larger class sorts after the smaller one by name, so only the
        // size can put it first.
        const encoding = (n: number) =>
            Array.from({ length: n }, (_, i) => ({
                error: 'author_metadata: unsupported_encoding',
                id: `e${i}`,
            }));
        const parse = Array.from({ length: 6 }, (_, i) => ({
            error: 'author_metadata: invalid_fb2',
            id: `p${i}`,
        }));
        const groups = groupScanErrors([...parse.slice(0, 3), ...encoding(31), ...parse.slice(3)]);
        expect(groups.map((g) => [g.cls, g.items.length])).toEqual([
            ['unsupported_encoding', 31],
            ['invalid_fb2', 6],
        ]);
        expect(groups[1].items.map((e) => e.id)).toEqual(['p0', 'p1', 'p2', 'p3', 'p4', 'p5']);
    });

    it('breaks a tie by class name', () => {
        const groups = groupScanErrors([
            { error: 'boom' },
            { error: 'author_metadata: entry_missing' },
        ]);
        expect(groups.map((g) => g.cls)).toEqual(['entry_missing', 'other']);
    });

    it('is empty for no errors', () => {
        expect(groupScanErrors([])).toEqual([]);
    });
});
