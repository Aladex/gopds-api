import {
    authorInitial,
    findMatch,
    isUnexplained,
    nameParts,
} from '@/features/catalogue/authorMatch';

describe('nameParts', () => {
    it('sets apart the part of the name the query is', () => {
        expect(nameParts('Стивен Кинг', 'кинг')).toEqual({
            before: 'Стивен ',
            hit: 'Кинг',
            after: '',
        });
    });

    it('takes ё and е for the same letter', () => {
        expect(nameParts('Пётр Семёнов', 'семенов')?.hit).toBe('Семёнов');
    });

    it('finds a Cyrillic query in a Latin name', () => {
        expect(nameParts('Laurie King', 'кинг')?.hit).toBe('King');
    });

    // Стругацкие for «стругацкий»: the stem matches, and the whole word recedes.
    it('takes the whole word when only the stem of a long query is there', () => {
        expect(nameParts('Братья Стругацкие', 'стругацкий')).toEqual({
            before: 'Братья ',
            hit: 'Стругацкие',
            after: '',
        });
    });

    it('does not stem a short query', () => {
        expect(nameParts('Кира Лем', 'кинг')).toBeNull();
    });

    it('ignores the spaces around a query', () => {
        expect(nameParts('Стивен Кинг', '  кинг ')?.hit).toBe('Кинг');
    });

    // Muting everything would leave a row with nothing to read by.
    it('leaves a name the query covers whole alone', () => {
        expect(nameParts('Лем', 'лем')).toBeNull();
        expect(nameParts('Стивен Кинг', 'стивен кинг')).toBeNull();
    });

    it('leaves a name alone when lower-casing would shift the letters', () => {
        expect(nameParts('İpek Oğuz', 'oğuz')).toBeNull();
    });
});

describe('findMatch', () => {
    it('finds nothing for an empty query', () => {
        expect(findMatch('Стивен Кинг', '   ')).toBeNull();
    });
});

describe('isUnexplained', () => {
    it.each([
        ['Станислав Лем', 'александр', true],
        ['Одоевский Владимир', 'достоевский', true],
        ['Дафна Дюморье', 'дюма', false],
        ['Laurie King', 'кинг', false],
        ['Георгий Иванович Чулков', 'иванов', false],
        ['Стивен Кинг', 'кинг', false],
        ['Лем', 'лем', false],
    ])('%s for «%s» is %s', (name, query, want) => {
        expect(isUnexplained(name, query)).toBe(want);
    });
});

describe('authorInitial', () => {
    it.each([
        ['Стивен Кинг', 'К'],
        ['Достоевская.', 'Д'],
        ['Николай Дмитриевич Толстой-Милославский', 'Т'],
        ['лем', 'Л'],
        ['Кинг С.', 'К'],
        ['😀 Смит', 'С'],
    ])('%s gives %s', (name, want) => {
        expect(authorInitial(name)).toBe(want);
    });

    it('gives nothing for a name without a letter', () => {
        expect(authorInitial('...')).toBe('');
    });
});
