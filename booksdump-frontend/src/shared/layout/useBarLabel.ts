import { useLayoutEffect, useRef, useState, type RefObject } from 'react';

import { chooseBarLabel, type BarLabel } from '@/shared/layout/barLabel';

const widthOf = (element: Element | null | undefined) =>
    element?.getBoundingClientRect().width ?? 0;

/**
 * useBarLabel fits the words before the books language into the bar.
 *
 * The bar holds the logo, the language and whatever follows it, and the label
 * gives way before the gap to the pill closes: all of it, then the short form,
 * then nothing, leaving the name, which is never cut. What is free is worked out
 * from everything but the label, off a rig holding both forms, so the choice
 * cannot change what it was measured from.
 */
export function useBarLabel(bar: RefObject<HTMLElement | null>, key: string) {
    const group = useRef<HTMLSpanElement | null>(null);
    const rig = useRef<HTMLSpanElement | null>(null);
    const [label, setLabel] = useState<BarLabel>('long');

    useLayoutEffect(() => {
        const row = bar.current;
        const held = group.current;
        const forms = rig.current;
        if (!row || !held || !forms || typeof ResizeObserver === 'undefined') {
            return;
        }
        const measure = () => {
            const others = [...row.children]
                .filter((child) => child !== held && !child.hasAttribute('data-bar-spring'))
                .reduce((sum, child) => sum + widthOf(child), 0);
            const name = widthOf(held.querySelector('[data-book-language]'));
            const [long, short] = [...forms.children];
            setLabel(
                chooseBarLabel({
                    free: row.getBoundingClientRect().width - others - name,
                    long: widthOf(long),
                    short: widthOf(short),
                }),
            );
        };
        measure();
        const observer = new ResizeObserver(measure);
        observer.observe(row);
        [...forms.children].forEach((form) => observer.observe(form));
        const trigger = held.querySelector('[data-book-language]');
        if (trigger) observer.observe(trigger);
        return () => observer.disconnect();
    }, [bar, key]);

    return { label, group, rig };
}
