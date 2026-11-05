import { describe, expect, it } from 'vitest';

import { thumbURL } from './coverUrl';

describe('thumbURL', () => {
    it('sanitises the path and the name as the cover routes do', () => {
        expect(thumbURL({ path: '/fb2-001.zip', filename: 'book.fb2' })).toBe(
            '/books-thumbs/fb2-001-zip/book-fb2.jpg',
        );
    });
});
