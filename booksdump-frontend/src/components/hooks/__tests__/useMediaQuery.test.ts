import { renderHook, act } from '@testing-library/react';

import { useMediaQuery } from '../useMediaQuery';

// The setup file installs a matchMedia that never matches, so each test here
// installs its own stub to control what the viewport claims to be.

type Listener = (event: MediaQueryListEvent) => void;

function installMatchMedia(matches: boolean) {
    const listeners: Listener[] = [];
    const removed: Listener[] = [];

    window.matchMedia = ((query: string) => ({
        matches,
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: (_: string, listener: Listener) => listeners.push(listener),
        removeEventListener: (_: string, listener: Listener) => removed.push(listener),
        dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;

    return {
        listeners,
        removed,
        emit: (next: boolean) =>
            act(() => {
                listeners.forEach((listener) => listener({ matches: next } as MediaQueryListEvent));
            }),
    };
}

const original = window.matchMedia;
afterEach(() => {
    window.matchMedia = original;
});

describe('useMediaQuery', () => {
    it('reports what the viewport says on the first render', () => {
        installMatchMedia(true);

        const { result } = renderHook(() => useMediaQuery('(max-width: 779px)'));

        expect(result.current).toBe(true);
    });

    it('follows the viewport as it changes', () => {
        const media = installMatchMedia(false);
        const { result } = renderHook(() => useMediaQuery('(max-width: 779px)'));
        expect(result.current).toBe(false);

        media.emit(true);

        expect(result.current).toBe(true);
    });

    it('lets go of the listener when it unmounts', () => {
        const media = installMatchMedia(false);
        const { unmount } = renderHook(() => useMediaQuery('(max-width: 779px)'));

        unmount();

        expect(media.removed).toHaveLength(1);
        expect(media.removed[0]).toBe(media.listeners[0]);
    });

    it('takes nothing down when the environment has no media queries', () => {
        // jsdom is one such environment; a component asking about the viewport
        // must not crash the page it is part of.
        (window as { matchMedia?: unknown }).matchMedia = undefined;

        const { result } = renderHook(() => useMediaQuery('(max-width: 779px)'));

        expect(result.current).toBe(false);
    });
});
