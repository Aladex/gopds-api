import {
    IDIOT_LINE,
    TAGLINES,
    loginErrorKey,
    pickTagline,
    replaceExclamations,
} from '@/features/auth/leprosorium';

describe('replaceExclamations', () => {
    it('turns fifteen exclamation marks into the confession', () => {
        expect(replaceExclamations('a' + '!'.repeat(15))).toBe('a' + IDIOT_LINE);
    });

    it('swallows a longer run whole rather than leaving a remainder', () => {
        expect(replaceExclamations('!'.repeat(40))).toBe(IDIOT_LINE);
    });

    it('leaves fourteen alone: the threshold is the joke', () => {
        const fourteen = 'x' + '!'.repeat(14);
        expect(replaceExclamations(fourteen)).toBe(fourteen);
    });

    it('is stable on its own output, so typing on does not re-trigger it', () => {
        const once = replaceExclamations('!'.repeat(15));
        expect(replaceExclamations(once)).toBe(once);
    });
});

describe('pickTagline', () => {
    it('picks from the Russian set for a Russian reader', () => {
        expect(pickTagline('ru', () => 0)).toBe(TAGLINES.ru[0]);
        expect(pickTagline('ru-RU', () => 0.999)).toBe(TAGLINES.ru[TAGLINES.ru.length - 1]);
    });

    it('picks from the English set for anyone else', () => {
        expect(pickTagline('en', () => 0)).toBe(TAGLINES.en[0]);
        expect(pickTagline('de', () => 0.5)).toBe(
            TAGLINES.en[Math.floor(0.5 * TAGLINES.en.length)],
        );
    });

    it('keeps %username% literal in every line: that is the joke', () => {
        for (const line of [...TAGLINES.ru, ...TAGLINES.en]) expect(line).toContain('%username%');
    });

    it('has the same number of lines in both languages', () => {
        expect(TAGLINES.en).toHaveLength(TAGLINES.ru.length);
    });
});

describe('loginErrorKey', () => {
    it.each([
        ['bad_credentials', 'wrongNameOrPassword'],
        ['bad password', 'wrongNameOrPassword'],
        ['user not active', 'userNotActive'],
        ['something else entirely', 'loginOrPasswordIncorrect'],
        [undefined, 'loginOrPasswordIncorrect'],
    ])('maps %s to %s', (message, key) => {
        expect(loginErrorKey(message)).toBe(key);
    });
});
