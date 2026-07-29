import { useCallback, useLayoutEffect, useRef, useState } from 'react';

export interface UnderlineBox {
    left: number;
    top: number;
    width: number;
}

/** The bar's thickness, in pixels. Matches the h-0.5 it is drawn with. */
const THICKNESS = 2;

/**
 * Places one bar under whichever navigation item is current, so that changing
 * section moves it rather than lighting a border in one place and putting one
 * out in another. Two borders swapping at once reads as a flicker between two
 * places; one bar moving reads as travel between them.
 *
 * It has to be measured rather than styled. The items are sized by their words,
 * the words change with the interface language, and in the admin panel the row
 * wraps — so the bar needs a vertical position as much as a horizontal one, and
 * both change when the window narrows.
 *
 * `shape` is anything that would move the bar without the active item changing:
 * the labels themselves, usually. The items are re-measured whenever it does,
 * and whenever the container resizes, which covers wrapping, a language change
 * and the browser settling on its fonts.
 *
 * Returns `placed: false` until the first measurement, so the bar can be put
 * where it belongs rather than sliding in from the corner on load.
 */
export function useTravellingUnderline<T extends HTMLElement>(
    activeKey: string | null | undefined,
    shape: string,
) {
    const containerRef = useRef<HTMLElement | null>(null);
    const itemRefs = useRef(new Map<string, T>());
    const [box, setBox] = useState<UnderlineBox | null>(null);
    const [placed, setPlaced] = useState(false);

    /** Ref callback for one navigation item, keyed however the caller likes. */
    const setItemRef = useCallback(
        (key: string) => (node: T | null) => {
            if (node) {
                itemRefs.current.set(key, node);
            } else {
                itemRefs.current.delete(key);
            }
        },
        [],
    );

    const measure = useCallback(() => {
        const container = containerRef.current;
        const item = activeKey ? itemRefs.current.get(activeKey) : undefined;
        if (!container || !item) {
            setBox(null);
            return;
        }
        const containerBox = container.getBoundingClientRect();
        const itemBox = item.getBoundingClientRect();
        const next: UnderlineBox = {
            left: itemBox.left - containerBox.left,
            // The bottom edge of the item's own row, which is not the bottom of
            // the container once the row has wrapped.
            top: itemBox.bottom - containerBox.top - THICKNESS,
            width: itemBox.width,
        };
        // Handing back a fresh object every render would loop: the effect below
        // runs on a callback that changes with this state's inputs.
        setBox((prev) =>
            prev && prev.left === next.left && prev.top === next.top && prev.width === next.width
                ? prev
                : next,
        );
        setPlaced(true);
    }, [activeKey]);

    useLayoutEffect(() => {
        const container = containerRef.current;
        if (!container) {
            return;
        }
        measure();
        const observer = new ResizeObserver(measure);
        observer.observe(container);
        return () => observer.disconnect();
        // `shape` stands in for the item list, which callers rebuild on every
        // render; depending on the array itself would re-run this each time.
    }, [measure, shape]);

    return { containerRef, setItemRef, box, placed };
}

export default useTravellingUnderline;
