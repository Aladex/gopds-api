import React, { useRef } from 'react';
import { render, screen } from '@testing-library/react';

import { useBarLabel } from '@/shared/layout/useBarLabel';

// jsdom has no widths, so the bar lends them. What is on trial is that the label
// is measured again when something other than the label's own text takes room:
// the pill arrives after the first paint, with the donate methods.

const WIDTHS: Record<string, number> = {
    bar: 320,
    logo: 40,
    me: 40,
    pill: 88,
    name: 70,
    long: 68,
    short: 34,
};

const Bar: React.FC<{ pill: boolean }> = ({ pill }) => {
    const bar = useRef<HTMLDivElement | null>(null);
    const { label, group, rig } = useBarLabel(bar, String(pill));
    return (
        <div ref={bar} data-w="bar">
            <span data-w="logo" />
            <span ref={group}>
                <span ref={rig}>
                    <span data-w="long" />
                    <span data-w="short" />
                </span>
                <button data-book-language="" data-w="name" />
            </span>
            <span data-bar-spring="" />
            {pill && <span data-w="pill" />}
            <span data-w="me" />
            <output>{label}</output>
        </div>
    );
};

describe('useBarLabel', () => {
    const rect = Element.prototype.getBoundingClientRect;

    beforeEach(() => {
        Element.prototype.getBoundingClientRect = function (this: Element) {
            const key = this.getAttribute('data-w') ?? '';
            return { ...new DOMRect(), width: WIDTHS[key] ?? 0 };
        };
    });

    afterEach(() => {
        Element.prototype.getBoundingClientRect = rect;
    });

    it('gives way when the pill arrives after the first paint', () => {
        const { rerender } = render(<Bar pill={false} />);
        // 320 - 80 - 70 leaves 170, room for the whole label.
        expect(screen.getByRole('status')).toHaveTextContent('long');

        rerender(<Bar pill />);

        // 82 is short of 68 and the 24px gap, and has room for 34 and the gap.
        expect(screen.getByRole('status')).toHaveTextContent('short');
    });
});
