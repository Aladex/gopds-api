import React from 'react';
import { act, render, screen } from '@testing-library/react';
import {
    createRoutesFromElements,
    matchRoutes,
    MemoryRouter,
    Route,
    Routes,
    useLocation,
    useNavigate,
    useSearchParams,
} from 'react-router';

import legacyRoutes from '@/app/routes/legacyRoutes';
import privateRoutes from '@/app/routes/privateRoutes';

// The old addresses live on in emails, bot messages, bookmarks and other
// people's chats. These run a real router over them: what counts is where the
// reader lands and what the page there reads back, not what the redirect
// meant to write.

let current = '';
let goBack: () => void = () => {};

const Landing: React.FC = () => {
    const location = useLocation();
    const navigate = useNavigate();
    const [params] = useSearchParams();
    // Recorded in an effect: a write to the outside world during render is
    // what the rules of React forbid.
    React.useEffect(() => {
        current = location.pathname + location.search + location.hash;
        goBack = () => navigate(-1);
    }, [location, navigate]);
    return <div data-testid="query">{params.get('q')}</div>;
};

const renderAt = (entries: string[], index = entries.length - 1) =>
    render(
        <MemoryRouter initialEntries={entries} initialIndex={index}>
            <Routes>
                {legacyRoutes}
                <Route path="*" element={<Landing />} />
            </Routes>
        </MemoryRouter>,
    );

const ENTITY = '01a168d0-a113-741a-b69d-53b91f3c7eb9';
const enc = encodeURIComponent;

describe('old addresses', () => {
    it.each([
        ['/authors/кинг/2', `/search?q=${enc('кинг')}&type=authors&page=2`],
        [`/authors/${enc('кинг')}/1`, `/search?q=${enc('кинг')}&type=authors`],
        [`/books/find/title/${enc('война')}/1`, `/search?q=${enc('война')}&type=books`],
        [`/books/find/title/${enc('война')}/4`, `/search?q=${enc('война')}&type=books&page=4`],
        ['/books/find/author/42/1', '/author/42'],
        ['/books/find/author/42/3', '/author/42?page=3'],
        [`/books/find/author/${ENTITY}/1`, `/author/${ENTITY}`],
        [`/books/find/author/${ENTITY}/2`, `/author/${ENTITY}?page=2`],
        [`/books/find/author/42/1?title=${enc('х')}`, `/author/42?title=${enc('х')}`],
        [`/books/find/author/42/3?title=${enc('х')}`, `/author/42?title=${enc('х')}&page=3`],
        ['/books/find/author/42/1?book_id=5', '/author/42?book_id=5'],
        [
            `/books/find/title/${enc('дюна')}/1?book_id=555`,
            `/search?book_id=555&q=${enc('дюна')}&type=books`,
        ],
    ])('%s lands on %s', (from, to) => {
        renderAt([from]);

        expect(current).toBe(to);
    });

    it('keeps the fragment of the old address', () => {
        renderAt(['/books/find/author/42/1#top']);

        expect(current).toBe('/author/42#top');
    });

    it.each(['а/б', '100%', 'что?', 'c++', 'a b', 'а&б', '%D0%B0', 'кинг'])(
        'reads %s back from an old title link and an old author link',
        (query) => {
            const titles = renderAt([`/books/find/title/${enc(query)}/1`]);
            expect(screen.getByTestId('query').textContent).toBe(query);
            titles.unmount();

            renderAt([`/authors/${enc(query)}/2`]);
            expect(screen.getByTestId('query').textContent).toBe(query);
        },
    );

    it('leaves series and genres where they are', () => {
        renderAt(['/books/find/category/9/2']);
        expect(current).toBe('/books/find/category/9/2');

        renderAt(['/books/find/genre/9/2?title=x']);
        expect(current).toBe('/books/find/genre/9/2?title=x');
    });

    it('takes the redirect out of the history, so back does not bounce', () => {
        renderAt(['/books/page/1', '/books/find/author/42/3']);
        expect(current).toBe('/author/42?page=3');

        act(() => goBack());

        expect(current).toBe('/books/page/1');
    });
});

describe('the route table', () => {
    const table = createRoutesFromElements(
        <>
            {privateRoutes}
            {legacyRoutes}
        </>,
    );
    const elementsFor = (path: string) => matchRoutes(table, path)?.map((m) => m.route.path);

    it.each(['/search', `/author/42`, `/author/${ENTITY}`])('serves %s', (path) => {
        expect(elementsFor(path)).toBeDefined();
    });

    it.each([
        '/authors/x/1',
        '/books/find/title/x/1',
        '/books/find/author/42/1',
        '/books/find/category/9/1',
        '/books/find/genre/9/1',
    ])('still answers %s', (path) => {
        expect(elementsFor(path)).toBeDefined();
    });
});
