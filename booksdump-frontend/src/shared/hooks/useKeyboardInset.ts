import { useEffect, useState } from 'react';

/**
 * useKeyboardInset says how much of the screen an on-screen keyboard covers, so
 * a full-screen panel can scroll its last row clear of it. With the viewport's
 * default interactive-widget the layout does not shrink for the keyboard, and
 * only the visual viewport knows.
 */
export function useKeyboardInset(enabled: boolean): number {
    const [inset, setInset] = useState(0);

    useEffect(() => {
        const viewport = window.visualViewport;
        if (!enabled || !viewport) {
            return;
        }
        const read = () =>
            setInset(
                Math.max(0, Math.round(window.innerHeight - viewport.height - viewport.offsetTop)),
            );
        viewport.addEventListener('resize', read);
        viewport.addEventListener('scroll', read);
        return () => {
            viewport.removeEventListener('resize', read);
            viewport.removeEventListener('scroll', read);
        };
    }, [enabled]);

    return enabled ? inset : 0;
}
