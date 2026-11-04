// utils.ts
import i18n from 'i18next';

export const formatDate = (dateString: string): string => {
    const options: Intl.DateTimeFormatOptions = {
        year: 'numeric',
        month: 'long',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
    };
    const date = new Date(dateString);
    const locale = i18n.language;
    return date.toLocaleDateString(locale, options);
};

/**
 * formatCalendarDay shows the day a value names, as dd.MM.yyyy and nothing
 * more: for a book's publication date, which is a day, not a moment.
 *
 * It reads the leading YYYY-MM-DD as written and never goes through a Date.
 * The source dates arrive as "2026-10-30", "2026-10-30 17:18" or a UTC
 * midnight, and a Date shifts the last into the reader's zone: the card used
 * to print «Опубликовано 30.10.2026, 02:00» for a book published on the 30th
 * — a time nobody stated — and a reader west of UTC got the 29th.
 *
 * A value with no leading date is shown as it is; an empty one is null, so
 * the caller can leave the line out.
 */
export const formatCalendarDay = (value: string): string | null => {
    const trimmed = value.trim();
    if (trimmed === '') return null;
    const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(trimmed);
    if (!match) return trimmed;
    const [, year, month, day] = match;
    return `${day}.${month}.${year}`;
};
