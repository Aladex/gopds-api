import { needsResize } from '../resize';

/*
 * A phone's URL bar slides the viewport's height by a few percent all the
 * time; the renderer should not reallocate its post-processing for that.
 */

describe('needsResize', () => {
    const was = { width: 390, height: 844 };

    it('ignores the URL bar sliding: a small height-only change', () => {
        expect(needsResize(was, { width: 390, height: 788 })).toBe(false);
        expect(needsResize(was, { width: 390, height: 900 })).toBe(false);
    });

    it('follows the keyboard: a large height-only change', () => {
        expect(needsResize(was, { width: 390, height: 420 })).toBe(true);
    });

    it('always follows a width change', () => {
        expect(needsResize(was, { width: 391, height: 844 })).toBe(true);
    });

    it('does nothing for no change', () => {
        expect(needsResize(was, { ...was })).toBe(false);
    });
});
