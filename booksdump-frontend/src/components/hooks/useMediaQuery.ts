import { useCallback, useSyncExternalStore } from 'react';

/**
 * useMediaQuery reports whether a CSS media query currently matches, and keeps
 * reporting as the viewport changes.
 *
 * Layout belongs in CSS wherever it can go there. This is for the cases where a
 * component has to make a different decision rather than a different style — the
 * pager, for instance, shows fewer page numbers on a narrow screen, and there is
 * no way to un-render an element from a media query.
 *
 * The viewport is state owned by the browser, not by React, so it is read
 * through useSyncExternalStore. Mirroring it into useState would mean rendering
 * once with a guess and again with the answer.
 */

/**
 * matchMediaOrNull returns null where the environment has no matchMedia at all.
 * A missing optional API must not take a whole page down with it, so callers
 * fall back to the wide layout rather than throwing. jsdom is one such
 * environment.
 */
const matchMediaOrNull = (query: string): MediaQueryList | null =>
    typeof window === 'undefined' || typeof window.matchMedia !== 'function'
        ? null
        : window.matchMedia(query);

export function useMediaQuery(query: string): boolean {
    const subscribe = useCallback(
        (onChange: () => void) => {
            const list = matchMediaOrNull(query);
            if (!list) {
                return () => {};
            }
            list.addEventListener('change', onChange);
            return () => list.removeEventListener('change', onChange);
        },
        [query],
    );

    const getSnapshot = useCallback(() => matchMediaOrNull(query)?.matches ?? false, [query]);

    // The server has no viewport; assume the wide layout, as the CSS does.
    const getServerSnapshot = useCallback(() => false, []);

    return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

export default useMediaQuery;
