import React from 'react';
import { render, screen, act } from '@testing-library/react';

import { Expandable } from '@/components/ui/expandable';

// The point of this component is that it animates where the obvious techniques
// cannot. jsdom computes no real layout, so what is asserted here is the state
// machine that makes the animation possible: an explicit pixel height to
// transition between, released once open so later reflows are not clipped, and
// pinned again before closing so there is a value to animate down from.

const LONG = 'Аннотация '.repeat(60);

function setReducedMotion(reduce: boolean) {
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
        matches: query.includes('prefers-reduced-motion') ? reduce : false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
    })) as unknown as typeof window.matchMedia;
}

/** endTransition fires the event the component waits for. */
function endTransition(el: HTMLElement) {
    act(() => {
        el.dispatchEvent(
            Object.assign(new Event('transitionend', { bubbles: true }), { propertyName: 'max-height' }),
        );
    });
}

beforeEach(() => {
    setReducedMotion(false);
});

describe('Expandable', () => {
    it('reports its state so styling and tests can key on it', () => {
        const { rerender } = render(
            <Expandable open={false} data-testid="box">
                {LONG}
            </Expandable>,
        );

        expect(screen.getByTestId('box')).toHaveAttribute('data-state', 'collapsed');

        rerender(
            <Expandable open data-testid="box">
                {LONG}
            </Expandable>,
        );

        expect(screen.getByTestId('box')).toHaveAttribute('data-state', 'open');
    });

    it('clips to a peek height while collapsed', () => {
        render(
            <Expandable open={false} data-testid="box">
                {LONG}
            </Expandable>,
        );

        expect(screen.getByTestId('box').style.maxHeight).toMatch(/px$/);
    });

    it('animates towards an explicit height, not towards auto', () => {
        const { rerender } = render(
            <Expandable open={false} data-testid="box">
                {LONG}
            </Expandable>,
        );

        rerender(
            <Expandable open data-testid="box">
                {LONG}
            </Expandable>,
        );

        // A height in pixels is what makes the transition possible at all.
        expect(screen.getByTestId('box').style.maxHeight).toMatch(/px$/);
    });

    it('releases the height once open so later reflows are not clipped', () => {
        const { rerender } = render(
            <Expandable open={false} data-testid="box">
                {LONG}
            </Expandable>,
        );
        rerender(
            <Expandable open data-testid="box">
                {LONG}
            </Expandable>,
        );

        endTransition(screen.getByTestId('box'));

        expect(screen.getByTestId('box').style.maxHeight).toBe('');
    });

    it('pins the height again before closing', () => {
        const { rerender } = render(
            <Expandable open data-testid="box">
                {LONG}
            </Expandable>,
        );
        endTransition(screen.getByTestId('box'));
        expect(screen.getByTestId('box').style.maxHeight).toBe('');

        rerender(
            <Expandable open={false} data-testid="box">
                {LONG}
            </Expandable>,
        );

        // Without a pixel value here the element would jump from none to the peek.
        expect(screen.getByTestId('box').style.maxHeight).toMatch(/px$/);
    });

    it('keeps the content mounted while collapsed, so it stays searchable', () => {
        render(<Expandable open={false}>полный текст аннотации</Expandable>);

        expect(screen.getByText('полный текст аннотации')).toBeInTheDocument();
    });

    it('skips the transition when the reader asked for less motion', () => {
        setReducedMotion(true);
        const { rerender } = render(
            <Expandable open={false} data-testid="box">
                {LONG}
            </Expandable>,
        );

        rerender(
            <Expandable open data-testid="box">
                {LONG}
            </Expandable>,
        );

        // Straight to released height, no intermediate pixel target.
        expect(screen.getByTestId('box').style.maxHeight).toBe('');
        expect(screen.getByTestId('box')).toHaveAttribute('data-state', 'open');
    });
});
