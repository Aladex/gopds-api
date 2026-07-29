import React from 'react';
import { render } from '@testing-library/react';

import NavUnderline from '@/shared/layout/NavUnderline';

// The bar both navigations move between their links. jsdom lays nothing out, so
// where it ends up is measured in a browser, not here; what these hold is that
// it takes its place from the box it is handed, stays out of the accessibility
// tree, and does not animate into position the first time.

describe('NavUnderline', () => {
    const box = { left: 112, top: 46, width: 73 };

    it('takes its place from the box it is given', () => {
        const { container } = render(<NavUnderline box={box} placed />);

        const bar = container.querySelector('span')!;
        expect(bar.style.left).toBe('112px');
        expect(bar.style.top).toBe('46px');
        expect(bar.style.width).toBe('73px');
    });

    // Which link is current is said by aria-current on the link itself; saying
    // it again here would only repeat the fact to a screen reader.
    it('stays out of the accessibility tree', () => {
        const { container } = render(<NavUnderline box={box} placed />);

        expect(container.querySelector('span')).toHaveAttribute('aria-hidden');
    });

    it('shows nothing when no link is current', () => {
        const { container } = render(<NavUnderline box={null} placed />);

        const bar = container.querySelector('span')!;
        expect(bar.className).toMatch(/opacity-0/);
        expect(bar.className).not.toMatch(/opacity-100/);
    });

    // There is nowhere to travel from before the first measurement, so the bar
    // is placed rather than slid — otherwise it flies in from the corner.
    it('does not animate into its first position', () => {
        const { container, rerender } = render(<NavUnderline box={box} placed={false} />);

        expect(container.querySelector('span')!.className).not.toMatch(/transition-/);

        rerender(<NavUnderline box={box} placed />);
        expect(container.querySelector('span')!.className).toMatch(/transition-\[/);
    });

    // Wrapping is why the bar carries a vertical position at all: in the admin
    // panel the row breaks in two, and the bar has to follow the active link
    // down rather than sit at the foot of the whole block.
    it('moves vertically as well as horizontally', () => {
        const { container, rerender } = render(<NavUnderline box={box} placed />);
        expect(container.querySelector('span')!.style.top).toBe('46px');

        rerender(<NavUnderline box={{ left: 66, top: 82, width: 79 }} placed />);
        const bar = container.querySelector('span')!;
        expect(bar.style.top).toBe('82px');
        expect(bar.style.left).toBe('66px');
        expect(bar.className).toMatch(/transition-\[left,top,width,opacity\]/);
    });

    it('takes the colour of the bar it is drawn on', () => {
        const { container } = render(<NavUnderline box={box} placed className="bg-foreground" />);

        expect(container.querySelector('span')!.className).toMatch(/bg-foreground/);
    });
});
