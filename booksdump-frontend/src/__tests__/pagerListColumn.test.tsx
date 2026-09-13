import fs from 'node:fs';
import path from 'node:path';

import React from 'react';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';

import { discoverImporters } from '@/__tests__/discoverImporters';
import { LIST_COLUMN_SELECTOR } from '@/shared/layout/breakpoints';
import BooksList from '@/features/catalogue/BooksList';
import AuthorSearch from '@/features/catalogue/AuthorSearch';
import CollectionsList from '@/features/collections/CollectionsList';
import UsersTable from '@/features/admin/UsersTable';
import CuratedCollectionsList from '@/features/admin/CuratedCollections/CuratedCollectionsList';
import * as booksApi from '@/api/books';
import * as authApi from '@/api/auth';
import * as collectionsApi from '@/api/collections';
import * as adminApi from '@/api/admin';
import * as curatedApi from '@/features/admin/CuratedCollections/api';

/*
 * BookPagination spreads its row to the whole of its parent and fits its window
 * to what that row measures. That only tells the reader the truth if the parent
 * is the box the list above it occupies — put the pager beside that box instead
 * of inside it and the row measures a wider parent, which is the defect the
 * whole measuring machinery exists to remove.
 *
 * The contract is therefore structural, not a class name: one element per list
 * page carries `data-list-column`, and both the list and its pager live inside
 * that one element.
 *
 * The call sites are found in the source rather than listed here. A list that
 * has to be kept up to date by hand is a list that goes stale, and the point of
 * this suite is to catch the caller nobody thought to tell it about. So it
 * reads them off the tree, holds every one it finds to the static half of the
 * contract, and fails if one of them has no rendered case below.
 */

/** The package root, so a caller outside src/ is not invisible either. */
const ROOT = path.resolve(import.meta.dirname, '../..');
const PAGER = path.resolve(ROOT, 'src/features/catalogue/BookPagination.tsx');

/** Every non-test source file that reaches the pager module, by any route. */
const discoverCallSites = () => discoverImporters(ROOT, PAGER);

const translate = (key: string, options?: Record<string, unknown> | string) => {
    if (options && typeof options === 'object' && 'count' in options) {
        return `${key}:${String(options.count)}`;
    }
    return typeof options === 'string' ? options : key;
};
const translation = { t: translate, i18n: { language: 'en' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

// Every page below is checked at its desktop layout, where the column is wider
// than the row needs and a pager outside it is at its most visible.
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => false,
    default: () => false,
}));

vi.mock('@/api/books', () => ({
    listAuthors: vi.fn(),
    listBooks: vi.fn(),
    toggleFavourite: vi.fn(),
}));
vi.mock('@/api/auth', () => ({ getCurrentUser: vi.fn() }));
vi.mock('@/api/collections', () => ({ listPublicCollections: vi.fn() }));
vi.mock('@/api/admin', () => ({
    listUsers: vi.fn(),
    changeUser: vi.fn(),
    deleteUser: vi.fn(),
    updateBook: vi.fn(),
}));
vi.mock('@/features/admin/CuratedCollections/api', () => ({
    listCuratedCollections: vi.fn(),
    deleteCuratedCollection: vi.fn(),
}));
vi.mock('@/api/preview', async () => {
    const actual = await vi.importActual<typeof import('@/api/preview')>('@/api/preview');
    return {
        ...actual,
        previewClient: { getPreview: vi.fn(), getChunk: vi.fn(), getImage: vi.fn() },
    };
});

// The import panels above the curated table and the conversion backdrop over
// the catalogue each talk to the network of their own accord; neither is what
// is being measured here.
vi.mock('@/features/admin/CuratedCollections/ImportForm', () => ({ default: () => null }));
vi.mock('@/features/admin/CuratedCollections/BatchImportForm', () => ({ default: () => null }));
vi.mock('@/features/catalogue/ConversionBackdrop', () => ({ default: () => null }));
vi.mock('@/shared/lib/downloadViaIframe', () => ({ downloadViaIframe: vi.fn() }));

// Each context hands back the same object every render. A fresh one each time
// is a new dependency each time, and an effect keyed on it never stops running
// — which is a heap exhaustion rather than a failing assertion.
vi.mock('@/context/AuthContext', () => {
    const value = { user: { books_lang: 'ru', is_superuser: false } };
    return { useAuth: () => value };
});
vi.mock('@/context/AuthorContext', () => {
    const value = { authorId: '', setAuthorId: vi.fn(), setAuthorName: vi.fn() };
    return { useAuthor: () => value };
});
vi.mock('@/context/FavContext', () => {
    const value = { fav: false, favEnabled: true, setFavEnabled: vi.fn() };
    return { useFav: () => value };
});
vi.mock('@/context/SearchBarContext', () => {
    const value = {
        searchItem: '',
        setSearchItem: vi.fn(),
        selectedSearch: 'title',
        setSelectedSearch: vi.fn(),
        languages: ['ru'],
        selectedLanguage: 'ru',
        setSelectedLanguage: vi.fn(),
        scopeName: '',
        setScopeName: vi.fn(),
    };
    return { useSearchBar: () => value };
});
vi.mock('@/context/BookConversionContext', () => {
    const value = {
        state: { convertingBooks: [], conversionErrors: [] },
        dispatch: vi.fn(),
    };
    return { useBookConversion: () => value };
});
vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));

const listAuthors = vi.mocked(booksApi.listAuthors);
const listBooks = vi.mocked(booksApi.listBooks);
const getCurrentUser = vi.mocked(authApi.getCurrentUser);
const listPublicCollections = vi.mocked(collectionsApi.listPublicCollections);
const listUsers = vi.mocked(adminApi.listUsers);
const listCuratedCollections = vi.mocked(curatedApi.listCuratedCollections);

/**
 * expectSharedColumn asserts the structural contract: the list and its pager
 * are inside the same marked column element, not merely two boxes that happen
 * to be styled alike.
 */
async function expectSharedColumn(findSomethingInTheList: () => Promise<HTMLElement>) {
    const insideTheList = await findSomethingInTheList();
    const nav = screen.getByRole('navigation');
    const pagerColumn = nav.closest(LIST_COLUMN_SELECTOR);
    const listColumn = insideTheList.closest(LIST_COLUMN_SELECTOR);

    expect(pagerColumn).not.toBeNull();
    expect(listColumn).not.toBeNull();
    expect(pagerColumn).toBe(listColumn);
}

const at = (path: string, route: string, element: React.ReactElement) =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <Routes>
                <Route path={route} element={element} />
            </Routes>
        </MemoryRouter>,
    );

/**
 * One case per call site, keyed by the source file discoverCallSites() reports.
 * A caller that turns up in the tree and not in here fails the coverage test
 * below rather than going quietly unchecked.
 */
const SCREENS: Record<string, { name: string; run: () => Promise<void> }> = {
    'src/features/catalogue/BooksList.tsx': {
        name: 'the book cards',
        run: async () => {
            listBooks.mockResolvedValue({
                books: [
                    {
                        id: 1,
                        title: 'Заклятые в любви',
                        authors: [{ id: 7, full_name: 'Райнер Анастасия' }],
                        series: [],
                        genres: [{ id: 3, genre: 'love_contemporary' }],
                        annotation: 'Атмосфера студенческой жизни.',
                        filename: 'book-1',
                        cover: false,
                        // Both derived from now, like every other date in this
                        // repository's fixtures: a literal is in the future
                        // until the calendar reaches it, and then the test is
                        // quietly exercising a different branch.
                        registerdate: new Date(Date.now() - 86_400_000).toISOString(),
                        docdate: new Date(Date.now() - 365 * 86_400_000).toISOString().slice(0, 10),
                        lang: 'ru',
                        fav: false,
                        approved: true,
                        path: 'fb2-1-2.zip',
                        format: 'fb2',
                        favorite_count: 0,
                    },
                ],
                length: 44500,
            });
            getCurrentUser.mockResolvedValue({
                username: 'reader',
                first_name: '',
                last_name: '',
                is_superuser: false,
            });
            at('/books/page/22000', '/books/page/:page', <BooksList />);
            await expectSharedColumn(() => screen.findByText('Заклятые в любви'));
        },
    },
    'src/features/catalogue/AuthorSearch.tsx': {
        name: 'the author rows',
        run: async () => {
            // The author rows sit in a centred 1200px column while the pager
            // used to sit outside it, in the page's full-width root — so on a
            // wide viewport the pager measured the viewport and its arrows
            // landed a couple of hundred pixels clear of the rows above them.
            listAuthors.mockResolvedValue({
                authors: [{ id: 7, full_name: 'Толстой Лев', books_count: 184 }],
                length: 9,
            });
            at(
                '/books/find/authors/tolstoy/1',
                '/books/find/authors/:author/:page',
                <AuthorSearch />,
            );
            await expectSharedColumn(() => screen.findByText('Толстой Лев'));
        },
    },
    'src/features/collections/CollectionsList.tsx': {
        name: 'the collection tiles',
        run: async () => {
            listPublicCollections.mockResolvedValue({
                rows: [{ id: 1, name: 'Antiutopias' }],
                total: 40,
                page: 1,
                page_size: 12,
            });
            at('/collections/page/1', '/collections/page/:page', <CollectionsList />);
            await expectSharedColumn(() => screen.findByText('Antiutopias'));
        },
    },
    'src/features/admin/UsersTable.tsx': {
        name: 'the user table',
        run: async () => {
            // This list is full-width by design — the admin panel gives it a
            // 1400px column, not the catalogue's 1200 — which is exactly why
            // the contract is "the column the list is in" and not a width.
            listUsers.mockResolvedValue({
                users: [
                    {
                        id: 1,
                        username: 'reader',
                        email: 'reader@example.org',
                        is_superuser: false,
                        active: true,
                        date_joined: new Date(Date.now() - 86_400_000).toISOString(),
                    },
                ],
                length: 9,
            });
            at('/admin/users/1', '/admin/users/:page', <UsersTable />);
            await expectSharedColumn(() => screen.findByText('reader'));
        },
    },
    'src/features/admin/CuratedCollections/CuratedCollectionsList.tsx': {
        name: 'the curated table',
        run: async () => {
            listCuratedCollections.mockResolvedValue({
                rows: [{ id: 1, name: 'Best of the year', is_public: true, is_curated: true }],
                total: 200,
                page: 1,
                page_size: 25,
            });
            at(
                '/admin/collections/page/1',
                '/admin/collections/page/:page',
                <CuratedCollectionsList />,
            );
            await expectSharedColumn(() => screen.findByText('Best of the year'));
        },
    },
};

describe('every page that renders the pager', () => {
    const callSites = discoverCallSites();

    it('is found in the source rather than listed by hand', () => {
        // A discovery that finds nothing would make every assertion below
        // vacuous, so the discovery itself is what is checked first.
        expect(callSites.length).toBeGreaterThan(0);
        expect(callSites).toContain('src/features/catalogue/BooksList.tsx');
    });

    it('marks a column of its own', () => {
        // The static half of the contract, and the half that catches a caller
        // nobody has written a render case for: a file that renders the pager
        // has to establish the column it is to be measured against.
        const unmarked = callSites.filter(
            (file) => !fs.readFileSync(path.resolve(ROOT, file), 'utf8').includes('listColumn'),
        );

        expect(unmarked).toEqual([]);
    });

    it('has a case below that renders it', () => {
        const uncovered = callSites.filter((file) => !(file in SCREENS));

        expect(uncovered).toEqual([]);
    });

    for (const [file, screen_] of Object.entries(SCREENS)) {
        it(`keeps the pager in the column of ${screen_.name} — ${file}`, screen_.run);
    }
});
