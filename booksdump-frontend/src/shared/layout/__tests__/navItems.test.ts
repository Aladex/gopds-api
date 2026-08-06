import { activeNavItem, onSurface, type NavItem } from '@/shared/layout/navItems';

// The header and the bottom bar both highlight the current section from this
// function. Getting it wrong means the interface tells the reader they are
// somewhere they are not.

const items: NavItem[] = [
    { id: 'books', label: 'Книги', path: '/books/page/1', regex: /^\/books\/page\/\d+/ },
    { id: 'collections', label: 'Подборки', path: '/collections', regex: /^\/collections/ },
    { id: 'admin', label: 'Админ', path: '/admin', regex: /^\/admin/, surfaces: ['header'] },
];

describe('activeNavItem', () => {
    it('matches a section from any page inside it', () => {
        expect(activeNavItem(items, '/books/page/1')?.id).toBe('books');
        expect(activeNavItem(items, '/books/page/4713')?.id).toBe('books');
    });

    it('matches a section from its sub-routes', () => {
        expect(activeNavItem(items, '/collections/25')?.id).toBe('collections');
        expect(activeNavItem(items, '/admin/users')?.id).toBe('admin');
    });

    it('reports nothing outside the sections', () => {
        // Filtered lists live off the paged books route and highlight nothing,
        // which is the behaviour the old header had.
        expect(activeNavItem(items, '/books/find/author/42/1')).toBeNull();
        expect(activeNavItem(items, '/login')).toBeNull();
    });

    it('does not confuse a section with one whose name starts the same', () => {
        expect(activeNavItem(items, '/collections-archive')?.id).toBe('collections');
        expect(activeNavItem(items, '/bookshelf')).toBeNull();
    });

    /*
     * The two surfaces show different subsets, but both must go on recognising
     * every route. Matching against a surface's own subset instead would answer
     * "none of these" in the admin panel, and the bottom bar would then fall
     * back to lighting up Books while the reader is somewhere else entirely.
     */
    it('goes on matching a section the surface does not offer', () => {
        expect(onSurface(items, 'bottom').map((item) => item.id)).not.toContain('admin');
        expect(activeNavItem(items, '/admin/users')?.id).toBe('admin');
    });
});

describe('onSurface', () => {
    it('keeps a section that names no surface on both', () => {
        for (const surface of ['header', 'bottom'] as const) {
            expect(onSurface(items, surface).map((item) => item.id)).toContain('books');
        }
    });

    it('gives a section that names one only to that one', () => {
        expect(onSurface(items, 'header').map((item) => item.id)).toContain('admin');
        expect(onSurface(items, 'bottom').map((item) => item.id)).not.toContain('admin');
    });

    it('leaves the order alone', () => {
        expect(onSurface(items, 'header').map((item) => item.id)).toEqual([
            'books',
            'collections',
            'admin',
        ]);
    });
});
