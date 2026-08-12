// jest-dom adds custom jest matchers for asserting on DOM nodes.
// allows you to do things like:
// expect(element).toHaveTextContent(/react/i)
// learn more: https://github.com/testing-library/jest-dom
import '@testing-library/jest-dom';

// jsdom implements no media queries at all, so window.matchMedia is missing.
// Components that ask about the viewport would otherwise take the whole tree
// down under test while working perfectly in a browser. Everything reports as
// not matching, which puts tests on the wide layout unless one says otherwise.
if (typeof window !== 'undefined' && typeof window.matchMedia !== 'function') {
    window.matchMedia = (query: string) =>
        ({
            matches: false,
            media: query,
            onchange: null,
            addListener: () => {},
            removeListener: () => {},
            addEventListener: () => {},
            removeEventListener: () => {},
            dispatchEvent: () => false,
        }) as unknown as MediaQueryList;
}

// window.localStorage is missing for a subtler reason than matchMedia: jsdom
// implements it perfectly well, but Node ships a localStorage global of its own
// that is undefined unless the process was started with --localstorage-file,
// and it lands on the window last and wins. So the property exists, and reading
// it yields nothing, in an environment that otherwise looks like a browser.
if (typeof window !== 'undefined' && !window.localStorage) {
    const store = new Map<string, string>();
    Object.defineProperty(window, 'localStorage', {
        configurable: true,
        value: {
            getItem: (key: string) => store.get(key) ?? null,
            setItem: (key: string, value: string) => void store.set(key, String(value)),
            removeItem: (key: string) => void store.delete(key),
            clear: () => store.clear(),
            key: (index: number) => Array.from(store.keys())[index] ?? null,
            get length() {
                return store.size;
            },
        } satisfies Storage,
    });
}

// jsdom implements no layout, and so no ResizeObserver either. Anything that
// measures itself to place something — the header's section underline — would
// take the tree down under test while working in a browser. The stub accepts
// observers and never reports, which is honest: nothing is ever resized here,
// because nothing is ever laid out.
if (typeof globalThis.ResizeObserver === 'undefined') {
    globalThis.ResizeObserver = class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
}

// Same gap, same shape: jsdom has no layout, so no element can be scrolled
// into view and the method is simply absent. Code that scrolls to an anchor
// would take the tree down here while working in every browser. Tests that
// care about the scrolling install their own spy over this and assert on it;
// this stub is only so that the others do not have to know the reader can
// scroll at all.
if (typeof Element.prototype.scrollIntoView !== 'function') {
    Element.prototype.scrollIntoView = function scrollIntoView() {};
}
