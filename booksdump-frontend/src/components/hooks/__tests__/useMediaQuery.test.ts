import { renderHook, act } from '@testing-library/react';

import { useMediaQuery } from '../useMediaQuery';

// The setup file installs a matchMedia that never matches, so each test here
// installs its own stub. The stub behaves as a real MediaQueryList does: its
// `matches` property reflects the viewport now, and listeners are told when it
// changes — the hook reads the property rather than trusting the event.

type Listener = () => void;

function installMatchMedia(initial: boolean) {
    let matches = initial;
    const listeners = new Set<Listener>();
    const removed: Listener[] = [];

    window.matchMedia = ((query: string) => ({
        get matches() {
            return matches;
        },
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: (_: string, listener: Listener) => listeners.add(listener),
        removeEventListener: (_: string, listener: Listener) => {
            removed.push(listener);
            listeners.delete(listener);
        },
        dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;

    return {
        get listenerCount() {
            return listeners.size;
        },
        removed,
        resize: (next: boolean) =>
            act(() => {
                matches = next;
                listeners.forEach((listener) => listener());
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

        media.resize(true);

        expect(result.current).toBe(true);
    });

    it('lets go of the listener when it unmounts', () => {
        const media = installMatchMedia(false);
        const { unmount } = renderHook(() => useMediaQuery('(max-width: 779px)'));
        expect(media.listenerCount).toBe(1);

        unmount();

        expect(media.listenerCount).toBe(0);
        expect(media.removed).toHaveLength(1);
    });

    it('takes nothing down when the environment has no media queries', () => {
        // jsdom is one such environment; a component asking about the viewport
        // must not crash the page it is part of.
        (window as { matchMedia?: unknown }).matchMedia = undefined;

        const { result } = renderHook(() => useMediaQuery('(max-width: 779px)'));

        expect(result.current).toBe(false);
    });
});
