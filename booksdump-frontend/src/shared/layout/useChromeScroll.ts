import { useEffect, useRef, useState } from 'react';

import { takeLanding } from '@/shared/layout/landOnList';

/** Near the top the band always shows. */
const HIDE_FROM = 50;
/** The bar above the band has scrolled away, so the band stands on its own. */
const DOCKED_FROM = 40;

/**
 * useChromeScroll follows the page on a phone: the bar goes with it, the band
 * under it docks at the top of the screen and leaves as the reader goes down a
 * long list, which gives back the space, and comes back on the way up.
 */
export function useChromeScroll(enabled: boolean) {
    const [hidden, setHidden] = useState(false);
    const [docked, setDocked] = useState(false);
    const lastY = useRef(0);

    // Coming back to the narrow layout brings the band back. Only the scroll
    // handler hides it, and it only runs while narrow, so a value left over from
    // an earlier narrow session would otherwise keep it hidden until the reader
    // happened to scroll.
    const [wasEnabled, setWasEnabled] = useState(enabled);
    if (wasEnabled !== enabled) {
        setWasEnabled(enabled);
        setHidden(false);
    }

    useEffect(() => {
        if (!enabled) {
            return;
        }

        const handleScroll = () => {
            const y = window.scrollY;
            const previous = lastY.current;
            lastY.current = y;

            setDocked(y >= DOCKED_FROM);

            // A turned page landing on its first card is not the reader going
            // down the list, and the band stands over that card.
            if (takeLanding()) {
                setHidden(false);
                return;
            }

            setHidden(y >= HIDE_FROM && y > previous);
        };

        window.addEventListener('scroll', handleScroll, { passive: true });
        return () => window.removeEventListener('scroll', handleScroll);
    }, [enabled]);

    return { hidden, docked };
}
