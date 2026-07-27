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