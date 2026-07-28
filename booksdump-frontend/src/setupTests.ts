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
