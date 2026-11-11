import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';

import { useSnapCarousel } from '@/shared/hooks/useSnapCarousel';

/*
 * The snapping track's mechanics, without either of its users. jsdom lays
 * nothing out, so every test gives the track a geometry of its own: slides
 * 100 px wide, 110 px apart, after a 16 px inset, moving with scrollLeft.
 * How that behaves with real snapping is measured in a browser; this is here
 * for what the hook makes of the position it is told.
 */

const INSET = 16;
const PITCH = 110;
const WIDTH = 100;

const Harness: React.FC<{
    count: number;
    align: 'center' | 'start';
    onActiveChange?: (index: number) => void;
    onSlideClick?: (index: number) => void;
}> = ({ count, align, onActiveChange, onSlideClick }) => {
    const { trackRef, active, trackProps } = useSnapCarousel({ count, align, onActiveChange });
    return (
        <div
            ref={trackRef}
            role="group"
            aria-label="track"
            tabIndex={0}
            style={{ paddingLeft: INSET }}
            {...trackProps}
        >
            {Array.from({ length: count }, (_, index) => (
                <div key={index} data-slide={index}>
                    <button type="button" onClick={() => onSlideClick?.(index)}>
                        slide {index}
                    </button>
                </div>
            ))}
            <output aria-label="active">{active}</output>
        </div>
    );
};

const layOut = () => {
    const track = screen.getByRole('group', { name: 'track' });
    const slides = Array.from(track.querySelectorAll<HTMLElement>('[data-slide]'));
    slides.forEach((slide, index) => {
        slide.getBoundingClientRect = () =>
            ({ left: INSET + index * PITCH - track.scrollLeft, width: WIDTH }) as DOMRect;
        Object.defineProperty(slide, 'offsetLeft', { value: INSET + index * PITCH });
    });
    track.getBoundingClientRect = () => ({ left: 0, width: 300 }) as DOMRect;
    const scrollTo = vi.fn(({ left }: { left: number }) => {
        track.scrollLeft = left;
        fireEvent.scroll(track);
    });
    track.scrollTo = scrollTo as never;
    return { track, scrollTo };
};

const activeIndex = () => Number(screen.getByLabelText('active').textContent);

afterEach(() => {
    vi.useRealTimers();
});

describe('useSnapCarousel', () => {
    test('start alignment picks the slide nearest the left inset', () => {
        render(<Harness count={6} align="start" />);
        const { track } = layOut();

        // Slide 2's left edge is 5 px past the inset; slide 3's centre is the
        // one nearest the track's centre, which is what centring would pick.
        track.scrollLeft = 215;
        fireEvent.scroll(track);

        expect(activeIndex()).toBe(2);
    });

    test('centre alignment still picks the slide nearest the centre', () => {
        render(<Harness count={6} align="center" />);
        const { track } = layOut();

        track.scrollLeft = 215;
        fireEvent.scroll(track);

        expect(activeIndex()).toBe(3);
    });

    test('a tap on a peeking slide goes to it instead of clicking it', async () => {
        const clicked = vi.fn();
        render(<Harness count={6} align="start" onSlideClick={clicked} />);
        const { scrollTo } = layOut();
        // Past the tail of any scroll, so the tap is a tap.
        await act(() => new Promise((resolve) => setTimeout(resolve, 200)));

        fireEvent.click(screen.getByRole('button', { name: 'slide 2' }));

        expect(clicked).not.toHaveBeenCalled();
        expect(scrollTo).toHaveBeenCalledWith(expect.objectContaining({ left: 2 * PITCH }));

        // The current slide's own controls are its own.
        await act(() => new Promise((resolve) => setTimeout(resolve, 200)));
        fireEvent.click(screen.getByRole('button', { name: 'slide 2' }));
        expect(clicked).toHaveBeenCalledWith(2);
    });

    test('ArrowRight and End move', () => {
        render(<Harness count={6} align="start" />);
        const { track, scrollTo } = layOut();

        fireEvent.keyDown(track, { key: 'ArrowRight' });
        expect(scrollTo).toHaveBeenLastCalledWith(expect.objectContaining({ left: PITCH }));
        expect(activeIndex()).toBe(1);

        fireEvent.keyDown(track, { key: 'End' });
        expect(scrollTo).toHaveBeenLastCalledWith(expect.objectContaining({ left: 5 * PITCH }));
        expect(activeIndex()).toBe(5);
    });

    test('vertical wheel does not move the track', () => {
        render(<Harness count={6} align="start" />);
        const { track, scrollTo } = layOut();

        fireEvent.wheel(track, { deltaY: 120, deltaX: 0 });

        expect(track.scrollLeft).toBe(0);
        expect(scrollTo).not.toHaveBeenCalled();
        expect(activeIndex()).toBe(0);
    });

    test('the active slide is reported once the track comes to rest', () => {
        vi.useFakeTimers();
        const reported = vi.fn();
        render(<Harness count={6} align="start" onActiveChange={reported} />);
        const { track } = layOut();

        // A swipe passes over slide 1 on its way to slide 2: only where it
        // stops is news, so the page does not open every stage it passes.
        track.scrollLeft = 110;
        fireEvent.scroll(track);
        act(() => vi.advanceTimersByTime(50));
        track.scrollLeft = 220;
        fireEvent.scroll(track);
        expect(reported).not.toHaveBeenCalled();

        act(() => vi.advanceTimersByTime(400));
        expect(reported).toHaveBeenCalledTimes(1);
        expect(reported).toHaveBeenCalledWith(2);
    });
});
