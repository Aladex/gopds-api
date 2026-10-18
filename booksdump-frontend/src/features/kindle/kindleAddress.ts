/**
 * What the server accepts as a Send-to-Kindle address, checked before asking
 * it: an address at kindle.com, or at free.kindle.com, which Amazon gave
 * Wi-Fi-only delivery. The server checks again and is the one that decides;
 * this only spares the reader a round trip to learn about a typo.
 */
const KINDLE_ADDRESS = /^[a-z0-9!#$%&'*+/=?^_`{|}~.-]+@(free\.)?kindle\.com$/;

/** normalizeKindleAddress is the address as it will be stored. */
export const normalizeKindleAddress = (value: string) => value.trim().toLowerCase();

export const isKindleAddress = (value: string) =>
    KINDLE_ADDRESS.test(normalizeKindleAddress(value));
