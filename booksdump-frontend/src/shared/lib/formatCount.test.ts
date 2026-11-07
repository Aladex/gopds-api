import { formatCount } from '@/shared/lib/formatCount';

describe('formatCount', () => {
    it('groups thousands the way the reader writes them', () => {
        expect(formatCount(48159, 'ru')).toBe('48 159');
        expect(formatCount(48159, 'en')).toBe('48,159');
    });

    it('never lets a number break across a line', () => {
        expect(formatCount(1234567, 'ru')).not.toContain(' ');
    });

    it('leaves a short number alone', () => {
        expect(formatCount(3, 'ru')).toBe('3');
    });

    it('survives a locale the runtime does not know', () => {
        expect(formatCount(1234, 'not a locale')).toBe('1234');
    });
});
