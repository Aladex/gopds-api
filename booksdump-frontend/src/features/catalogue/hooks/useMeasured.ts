import { useLayoutEffect, useState } from 'react';

/*
 * One ResizeObserver for every measured element on the page, and one wait
 * for the fonts. A card measures up to three things and a page holds fifty
 * cards: an observer and a fonts callback each came to 150 of both. The
 * observer reports which elements changed, and each is handed to the
 * callbacks subscribed to it alone.
 */
const subscribers = new Map<Element, Set<() => void>>();
let sharedObserver: ResizeObserver | null = null;
let fontsAwaited = false;

const observer = () => {
    sharedObserver ??= new ResizeObserver((entries) => {
        for (const entry of entries) {
            subscribers.get(entry.target)?.forEach((update) => update());
        }
    });
    return sharedObserver;
};

/**
 * Once the fonts have loaded, everything measured so far is measured again:
 * a fallback face sets the text at another width. Anything subscribing after
 * that measures with the real face already.
 */
const awaitFonts = () => {
    if (fontsAwaited || typeof document === 'undefined' || !document.fonts) return;
    fontsAwaited = true;
    void document.fonts.ready.then(() => {
        subscribers.forEach((updates) => updates.forEach((update) => update()));
    });
};

/** Calls `update` whenever `element` is resized; returns the way to stop. */
const watchSize = (element: Element, update: () => void) => {
    let updates = subscribers.get(element);
    if (!updates) {
        updates = new Set();
        subscribers.set(element, updates);
        observer().observe(element);
    }
    updates.add(update);
    awaitFonts();
    return () => {
        updates.delete(update);
        if (updates.size === 0) {
            subscribers.delete(element);
            sharedObserver?.unobserve(element);
        }
    };
};

/**
 * useMeasured reads something off an element's layout and keeps reading it
 * as the element changes size.
 *
 * The card offers «Подробнее» only when it is holding something back, and
 * only the browser knows whether three lines of an annotation were enough or
 * a row of genres overflowed: that depends on the width the card happens to
 * get and the face the text happens to be set in. So the card asks.
 *
 * `measure` is called once the element is in the document, again whenever it
 * is resized, and once more when the fonts have loaded. It must be a stable
 * function — a module-level one — or every render would re-subscribe.
 *
 * While `active` is false nothing is measured and the last answer stands. An
 * opened card has removed its own clamps, and asking then would report that
 * nothing is clipped — taking away the control that closes it.
 *
 * Returns the ref to put on the element, and the latest answer.
 */
export function useMeasured<E extends HTMLElement, T>(
    measure: (element: E) => T,
    initial: T,
    active: boolean,
    same: (a: T, b: T) => boolean = Object.is,
): [(element: E | null) => void, T] {
    const [element, setElement] = useState<E | null>(null);
    const [value, setValue] = useState<T>(initial);

    useLayoutEffect(() => {
        if (!element || !active) return;
        const update = () => {
            const next = measure(element);
            setValue((previous) => (same(previous, next) ? previous : next));
        };
        update();
        return watchSize(element, update);
    }, [element, active, measure, same]);

    return [setElement, value];
}

/** Whether a line-clamped block runs past its lines. */
export const isClippedBlock = (element: HTMLElement) =>
    element.scrollHeight > element.clientHeight + 1;

/** Whether a single truncated line runs past its width. */
export const isClippedLine = (element: HTMLElement) =>
    element.scrollWidth > element.clientWidth + 1;
