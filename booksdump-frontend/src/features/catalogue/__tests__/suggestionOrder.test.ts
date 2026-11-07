import type { AutocompleteSuggestion } from '@/api/autocomplete';
import { orderSuggestions } from '@/features/catalogue/suggestionOrder';

// One field answers for books and authors together, so the order is the whole of
// what the reader is told about which one the words meant.

const author = (value: string, books_count: number): AutocompleteSuggestion => ({
    value,
    type: 'author',
    id: value,
    books_count,
});
const book = (value: string, books_count = 1): AutocompleteSuggestion => ({
    value,
    type: 'book',
    id: value,
    books_count,
});

const TITLES = [
    book('Кинг Конг', 3),
    book('Кингчесс', 2),
    book('Кингчесс (СИ)', 2),
    book('Кинг'),
    book('Кинг-Конг и все-все-все'),
    book('Кинг-Конг-Теория [litres]'),
    book('Кингсблад, потомок королей'),
    book('Кингсбридж'),
];
const names = (rows: AutocompleteSuggestion[]) => rows.map((row) => row.value);

describe('orderSuggestions', () => {
    it('leads with the author when the words are plainly that author', () => {
        const rows = orderSuggestions(
            [...TITLES, author('Стивен Кинг', 778), author('Уильям Кинг', 90)],
            'кинг',
        );

        expect(names(rows).slice(0, 3)).toEqual(['Стивен Кинг', 'Уильям Кинг', 'Кинг Конг']);
    });

    it('keeps the top two titles first otherwise, with the authors next', () => {
        const rows = orderSuggestions(
            [...TITLES, author('Анна Кингсли', 12), author('Джон Кингдон', 9)],
            'кинг',
        );

        expect(names(rows).slice(0, 4)).toEqual([
            'Кинг Конг',
            'Кингчесс',
            'Анна Кингсли',
            'Джон Кингдон',
        ]);
    });

    it.each([
        ['a hundred books', [author('Иван Кинг', 99), author('Пётр Кинг', 5)]],
        ['three times the next', [author('Иван Кинг', 300), author('Пётр Кинг', 101)]],
        ['the whole name or its last word', [author('Кингсли Эмис', 500)]],
    ])('does not lead with an author short of %s', (_, authors) => {
        const rows = orderSuggestions([...TITLES, ...authors], 'кинг');

        expect(rows[0].type).toBe('book');
    });

    it('takes the whole name as well as the last word', () => {
        const rows = orderSuggestions([...TITLES, author('Стивен Кинг', 778)], '  СТИВЕН КИНГ ');

        expect(rows[0].value).toBe('Стивен Кинг');
    });

    it('holds a prefix back: гарри is not Гарри Гаррисон', () => {
        const rows = orderSuggestions(
            [book('Гарри Поттер', 40), author('Гарри Гаррисон', 444), author('Иной', 112)],
            'гарри',
        );

        expect(rows[0].type).toBe('book');
    });

    it('takes at most three authors and seven titles', () => {
        const rows = orderSuggestions(
            [
                ...TITLES,
                author('А', 7),
                author('Б', 6),
                author('В', 5),
                author('Г', 4),
                author('Д', 3),
            ],
            'кинг',
        );

        expect(rows.filter((row) => row.type === 'author')).toHaveLength(3);
        expect(rows.filter((row) => row.type === 'book')).toHaveLength(7);
    });

    it('sorts by what is behind a row, keeping the server’s order among equals', () => {
        const rows = orderSuggestions(
            [book('Один'), book('Три', 3), book('Ещё один'), author('Мало', 1), author('Много', 9)],
            'нет такого',
        );

        expect(names(rows)).toEqual(['Три', 'Один', 'Много', 'Мало', 'Ещё один']);
    });

    it('does not reorder the list it was given', () => {
        const given = [book('Один'), book('Три', 3)];

        orderSuggestions(given, 'x');

        expect(names(given)).toEqual(['Один', 'Три']);
    });
});
