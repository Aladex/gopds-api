import type { Book } from '@/api/books';
import { authorsOf } from '@/features/send/SendConfirmSheet';

// The send sheet names a book's authors as its card does: a Russian book by
// the entities' short names, any other language by the book's own spelling,
// a name of several people as the book writes it.

const book = (lang: string): Book =>
    ({
        lang,
        authors: [{ id: 1, full_name: 'Михайлов Владимир' }],
        authors_display: [
            {
                name: 'Владимир Дмитриевич Михайлов',
                short_name: 'Владимир Михайлов',
                entities: [
                    {
                        id: 'm',
                        full_name: 'Владимир Дмитриевич Михайлов',
                        short_name: 'Владимир Михайлов',
                    },
                ],
            },
            {
                name: 'Аркадий и Борис Стругацкие',
                entities: [
                    { id: 'a', full_name: 'Аркадий Стругацкий' },
                    { id: 'b', full_name: 'Борис Стругацкий' },
                ],
            },
        ],
    }) as Book;

describe('authorsOf', () => {
    it('names a Russian book’s authors by their short names', () => {
        expect(authorsOf(book('ru'))).toBe('Владимир Михайлов, Аркадий и Борис Стругацкие');
        expect(authorsOf(book(''))).toBe('Владимир Михайлов, Аркадий и Борис Стругацкие');
    });

    it('keeps the book’s spelling in another language', () => {
        expect(authorsOf(book('uk'))).toBe(
            'Владимир Дмитриевич Михайлов, Аркадий и Борис Стругацкие',
        );
    });

    it('falls back to the catalogue’s authors', () => {
        expect(authorsOf({ ...book('ru'), authors_display: undefined })).toBe('Михайлов Владимир');
    });
});
