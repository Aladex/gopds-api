import { activeNavItem, type NavItem } from '@/shared/layout/navItems';

// The header and the bottom bar both highlight the current section from this
// function. Getting it wrong means the interface tells the reader they are
// somewhere they are not.

const items: NavItem[] = [
    { id: 'books', label: 'Книги', path: '/books/page/1', regex: /^\/books\/page\/\d+/ },
    { id: 'collections', label: 'Подборки', path: '/collections', regex: /^\/collections/ },
    { id: 'opds', label: 'OPDS', path: '/catalog', regex: /^\/catalog/ },
    { id: 'admin', label: 'Админ', path: '/admin', regex: /^\/admin/ },
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
});
