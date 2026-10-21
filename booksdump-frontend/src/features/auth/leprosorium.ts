/**
 * The Leprosorium references on the way in: the lines a reader of that blog
 * recognises before they have read the whole sentence.
 *
 * They are quotations, not copy. The taglines are the ones the blog greeted
 * its readers with, addressed to `%username%`, and the placeholder is left
 * unfilled on purpose — a greeting that knew the reader's name would have
 * missed the point of it. Several of the originals were already English, so
 * the English set is the same voice rather than a translation of it.
 */

export const TAGLINES = {
    ru: [
        'Здравствуйте, %username%.',
        'Попасть сюда сложно, %username%. Выход не могут найти даже старожилы.',
        'Вы не ошиблись дверью, %username%?',
        'Cпасибо, что зашли, %username%.',
        'Don’t panic, %username%!',
        'Тут никого нет, %username%! Никого! Никого, кроме тебя!',
        'Мы верим в вас, %username%. Нет, правда.',
        'Не верьте тому, что пишут перед вашим псевдонимом, %username%.',
        'Сегодня вас ждёт приятная неожиданность, %username%!',
        'F5, %username%.',
    ],
    en: [
        'Hello, %username%.',
        'Getting in is hard, %username%. Even the old-timers cannot find the way out.',
        'Are you sure this is the right door, %username%?',
        'Thanks for dropping by, %username%.',
        'Don’t panic, %username%!',
        'There is nobody here, %username%! Nobody! Nobody but you!',
        'We believe in you, %username%. No, really.',
        'Do not trust what they write in front of your nickname, %username%.',
        'A pleasant surprise awaits you today, %username%!',
        'F5, %username%.',
    ],
} as const;

/**
 * One tagline for this visit. The language is the interface's, and anything
 * that is not Russian gets the English set: those are the two the interface
 * speaks.
 */
export function pickTagline(language: string, random: () => number = Math.random): string {
    const lines = language.startsWith('ru') ? TAGLINES.ru : TAGLINES.en;
    return lines[Math.min(lines.length - 1, Math.floor(random() * lines.length))];
}

/** What the blog's parser made of fifteen exclamation marks in a row. */
export const IDIOT_LINE = '! я идиот! убейте меня, кто-нибудь!';

/**
 * The parser's most famous replacement, applied to the login field as the
 * reader types. The replacement holds no run of its own, so it cannot trigger
 * itself on the next keystroke.
 */
export function replaceExclamations(value: string): string {
    return value.replace(/!{15,}/g, IDIOT_LINE);
}

/**
 * The translation key for a refused sign-in.
 *
 * An unknown account and a wrong password are one message on purpose. The
 * server tells them apart, the screen does not: saying which half was wrong
 * only tells a stranger which names exist, and the blog's own line already
 * covers both. An inactive account keeps its real meaning — the reader typed
 * everything right and needs to know what to do next.
 */
export function loginErrorKey(message: string | undefined): string {
    switch (message) {
        case 'bad_credentials':
        case 'bad password':
            return 'wrongNameOrPassword';
        case 'user not active':
            return 'userNotActive';
        default:
            return 'loginOrPasswordIncorrect';
    }
}
