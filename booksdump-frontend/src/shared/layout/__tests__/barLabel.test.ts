import { BAR_LABEL_GAP, chooseBarLabel } from '@/shared/layout/barLabel';

describe('chooseBarLabel', () => {
    const sizes = { long: 70, short: 34 };

    it('takes the whole label where it leaves the gap', () => {
        expect(chooseBarLabel({ ...sizes, free: 70 + BAR_LABEL_GAP })).toBe('long');
    });

    it('takes the short form a pixel under that', () => {
        expect(chooseBarLabel({ ...sizes, free: 70 + BAR_LABEL_GAP - 1 })).toBe('short');
        expect(chooseBarLabel({ ...sizes, free: 34 + BAR_LABEL_GAP })).toBe('short');
    });

    it('takes nothing a pixel under that, leaving the name', () => {
        expect(chooseBarLabel({ ...sizes, free: 34 + BAR_LABEL_GAP - 1 })).toBe('none');
    });

    it('takes nothing in a bar with no room at all', () => {
        expect(chooseBarLabel({ ...sizes, free: -10 })).toBe('none');
    });

    it('takes the whole label before anything can be measured', () => {
        expect(chooseBarLabel({ free: 0, long: 0, short: 0 })).toBe('long');
    });
});
