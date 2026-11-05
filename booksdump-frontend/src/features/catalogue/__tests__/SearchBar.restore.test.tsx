import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation, useNavigate } from 'react-router';

import { SearchBarProvider, useSearchBar } from '@/context/SearchBarContext';
import SearchBar from '@/features/catalogue/SearchBar';

// The panel restored from an address, through the real context and the real
// scope hook: what is on trial is how the seeding and the scope's own entry
// into title mode meet, and a mocked context would hide that.

const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/context/FavContext', () => ({
    useFav: () => ({ fav: false, favEnabled: true, setFavEnabled: vi.fn() }),
}));

const authorState = {
    authorId: '',
    authorName: '',
    setAuthorId: vi.fn(),
    setAuthorName: vi.fn(),
    clearAuthorId: vi.fn(),
};
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

const authState = { user: null, isAuthenticated: false, updateLang: vi.fn() };
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));

vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => false,
    default: () => false,
}));

vi.mock('@/shared/layout/LanguageSwitcher', () => ({ default: () => null }));

vi.mock('@/features/catalogue/AutocompleteSearch', () => ({
    default: ({
        value,
        onChange,
        onEnterPressed,
    }: {
        value: string;
        onChange: (v: string) => void;
        onEnterPressed: () => void;
    }) => (
        <input
            aria-label="searchItem"
            value={value}
            onChange={(event) => onChange(event.target.value)}
            onKeyDown={(event) => {
                if (event.key === 'Enter') onEnterPressed();
            }}
        />
    ),
}));

const NAME = 'кинг';
const AUTHORS = `/search?q=${encodeURIComponent(NAME)}&type=authors`;

/** What the author list and the browser do around the panel. */
const Journey: React.FC = () => {
    const navigate = useNavigate();
    const { pathname, search } = useLocation();
    const { setSearchItem } = useSearchBar();
    return (
        <>
            <span data-testid="address">{pathname + search}</span>
            <button
                type="button"
                onClick={() => {
                    // A card clears the box on its way through.
                    setSearchItem('');
                    navigate('/author/42');
                }}
            >
                follow-card
            </button>
            <button type="button" onClick={() => navigate(`${AUTHORS}&page=2`)}>
                next-page
            </button>
            <button type="button" onClick={() => navigate('/author/42')}>
                plain-link
            </button>
            <button type="button" onClick={() => navigate('/books/find/genre/7/1')}>
                genre-link
            </button>
            <button type="button" onClick={() => navigate('/author/42?title=x')}>
                filtered-author
            </button>
            <button type="button" onClick={() => navigate(-1)}>
                back
            </button>
            <button type="button" onClick={() => navigate(1)}>
                forward
            </button>
        </>
    );
};

const openAt = (...entries: string[]) =>
    render(
        <MemoryRouter initialEntries={entries} initialIndex={entries.length - 1}>
            <SearchBarProvider>
                <SearchBar />
                <Journey />
            </SearchBarProvider>
        </MemoryRouter>,
    );

const box = () => screen.getByLabelText('searchItem');
const mode = () => screen.getByRole('combobox', { name: 'searchWhat' });
const press = (name: string) => userEvent.click(screen.getByRole('button', { name }));

const expectAuthorsMode = async (name: string) => {
    await waitFor(() => expect(mode()).toHaveTextContent('byAuthor'));
    expect(box()).toHaveValue(name);
};

const expectTitleMode = async (text: string) => {
    await waitFor(() => expect(mode()).toHaveTextContent('byTitle'));
    expect(box()).toHaveValue(text);
};

describe('SearchBar restored from the address', () => {
    it('shows the authors mode and the name on a direct open', async () => {
        openAt(AUTHORS);

        await expectAuthorsMode(NAME);
    });

    it('reads a name made of punctuation back whole', async () => {
        const name = 'а/б?в%г+д';
        openAt(`/search?q=${encodeURIComponent(name)}&type=authors`);

        await expectAuthorsMode(name);
    });

    it('sends the next search to authors, as after typing', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await userEvent.clear(box());
        await userEvent.type(box(), 'толстой{Enter}');

        expect(screen.getByTestId('address')).toHaveTextContent(
            `/search?q=${encodeURIComponent('толстой')}&type=authors`,
        );
    });

    it('leaves a book search in title mode', async () => {
        openAt(`/search?q=${encodeURIComponent('дюна')}&type=books`);

        await expectTitleMode('дюна');
    });

    it('leaves an open on an author in title mode, with the scope on and an empty box', async () => {
        openAt('/author/42');

        await expectTitleMode('');
        expect(screen.getByText('searchWithinThisAuthor')).toBeInTheDocument();
    });

    it('leaves the reader their own words until the address changes', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await userEvent.clear(box());
        await userEvent.type(box(), 'новое');

        expect(box()).toHaveValue('новое');
    });

    it('stays on the name while paging', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await press('next-page');

        await expectAuthorsMode(NAME);
    });
});

describe('SearchBar on the way through an author', () => {
    it('enters the author in title mode with an empty box', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await press('follow-card');

        await expectTitleMode('');
        expect(screen.getByText('searchWithinThisAuthor')).toBeInTheDocument();
    });

    it('brings the name back on Back', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);
        await press('follow-card');
        await expectTitleMode('');

        await press('back');

        await expectAuthorsMode(NAME);
    });

    it('does not carry the name back in as a title on Forward', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);
        await press('follow-card');
        await expectTitleMode('');
        await press('back');
        await expectAuthorsMode(NAME);

        await press('forward');

        await expectTitleMode('');
        expect(screen.getByText('searchWithinThisAuthor')).toBeInTheDocument();
    });

    it('drops the name on a link into an author that did not clear the box', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await press('plain-link');

        await expectTitleMode('');
    });

    it('keeps the words of a book search on the way into an author, as before', async () => {
        openAt(`/search?q=${encodeURIComponent('дюна')}&type=books`);
        await expectTitleMode('дюна');

        await press('plain-link');

        await expectTitleMode('дюна');
    });

    it('keeps a title the address of the author carries', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await press('filtered-author');

        await expectTitleMode('x');
    });

    it('keeps the words on the way into another kind of list, as before', async () => {
        openAt(AUTHORS);
        await expectAuthorsMode(NAME);

        await press('genre-link');

        await expectTitleMode(NAME);
    });
});
