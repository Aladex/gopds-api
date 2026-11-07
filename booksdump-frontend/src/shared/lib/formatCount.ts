/**
 * formatCount writes a page or book count the way the reader's language does:
 * `48 159` in Russian, `48,159` in English. The Russian separator is a no-break
 * space, so a number cannot wrap across two lines of a narrow row.
 */
export function formatCount(count: number, language: string): string {
    try {
        return new Intl.NumberFormat(language).format(count);
    } catch {
        // An invalid tag throws, and a stored locale is not ours to trust.
        return String(count);
    }
}
