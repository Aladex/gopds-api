/**
 * Base URLs.
 *
 * The JSON transport lives in http.ts and the resource clients beside it; this
 * file only holds the two addresses that other transports need — downloads and
 * WebSockets, which are not JSON and have their own lifecycles.
 */

/** Empty in production, so requests are same-origin against the Go binary. */
const API_URL = import.meta.env.VITE_API_URL ?? '';

// Derived from the current page location so the binary works both locally and
// in production without a rebuild.
const WS_URL = (() => {
    const loc = window.location;
    const wsProto = loc.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${wsProto}//${loc.host}`;
})();

export { API_URL, WS_URL };
