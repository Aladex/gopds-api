import React from 'react';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useNavigate } from 'react-router';

import Header from '@/shared/layout/Header';
import {
    CONTENT_COLUMN,
    HEADER_WIDE_QUERY,
    LIST_COLUMN_ATTR,
    PHONE_QUERY,
} from '@/shared/layout/breakpoints';
import { scrollToLoadedList } from '@/shared/layout/landOnList';
import type { DonateMethod } from '@/api/system';

const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const authState = {
    logout: vi.fn(),
    user: { username: 'reader', is_superuser: false },
};
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));

const search = { restoreWords: vi.fn() };
vi.mock('@/features/catalogue/CatalogueSearchContext', () => ({
    useCatalogueSearchContext: () => search,
}));

// The field stands in with what the header hands it. Its own behaviour has its
// own tests; what matters here is the screen it gets.
vi.mock('@/features/catalogue/HeaderSearchField', () => ({
    default: ({
        taking,
        onTake,
        onDone,
    }: {
        taking?: boolean;
        onTake?: () => void;
        onDone?: () => void;
    }) => (
        <div data-testid="field" data-taking={String(Boolean(taking))}>
            <button type="button" onClick={onTake}>
                focus-field
            </button>
            <button type="button" onClick={onDone}>
                finish-search
            </button>
        </div>
    ),
}));
vi.mock('@/shared/layout/BookLanguage', () => ({ default: () => <span>language</span> }));
const labelKeys = vi.hoisted(() => [] as string[]);
vi.mock('@/shared/layout/useBarLabel', () => ({
    useBarLabel: (_: unknown, key: string) => {
        labelKeys.push(key);
        return { label: 'long', group: { current: null }, rig: { current: null } };
    },
}));
vi.mock('@/shared/layout/ThemeToggle', () => ({ default: () => <button>theme</button> }));
vi.mock('@/shared/layout/DonateModal', () => ({ default: () => <div>donate-modal</div> }));

const media = { phone: true, wide: false };
const matches = (query: string) =>
    (query === PHONE_QUERY && media.phone) || (query === HEADER_WIDE_QUERY && media.wide);
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: (query: string) => matches(query),
    default: (query: string) => matches(query),
}));

const METHODS: DonateMethod[] = [{ id: 'a', label: 'A', kind: 'link', value: 'x', qr: false }];

const Go: React.FC = () => {
    const navigate = useNavigate();
    return (
        <button type="button" onClick={() => navigate('/books/page/2')}>
            go
        </button>
    );
};

const renderHeader = (path = '/books/page/1', donateMethods = METHODS) =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <Header donateMethods={donateMethods} onOpenProfile={profile} />
            <Go />
        </MemoryRouter>,
    );

const profile = vi.fn();
const band = () => screen.getByTestId('search-band');
const pill = () => screen.queryAllByRole('button', { name: /donate|Донат/ });
const before = (a: Element, b: Element) =>
    Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);

const scrollTo = (y: number) =>
    act(() => {
        Object.defineProperty(window, 'scrollY', { value: y, configurable: true });
        window.dispatchEvent(new Event('scroll'));
    });

beforeEach(() => {
    vi.clearAllMocks();
    media.phone = true;
    media.wide = false;
    Object.defineProperty(window, 'scrollY', { value: 0, configurable: true });
    window.scrollTo = vi.fn();
    document.body.style.overflow = '';
});

describe('Header on a phone', () => {
    it('holds no column: its two storeys are the phone’s own width', () => {
        renderHeader();

        expect(band().className).not.toContain('max-w');
        expect(band().parentElement?.className ?? '').not.toContain('max-w');
    });

    it('leaves the sections to the row under it', () => {
        renderHeader();

        expect(screen.queryByRole('navigation')).toBeNull();
    });

    it('holds the language where it applies, and the word where it does not', () => {
        const { unmount } = renderHeader('/books/page/1');
        expect(screen.getByText('language')).toBeInTheDocument();
        expect(screen.queryByText('booksdump')).toBeNull();
        unmount();

        renderHeader('/collections');
        expect(screen.queryByText('language')).toBeNull();
        expect(screen.getByText('booksdump')).toBeInTheDocument();
    });

    it('holds the language inside a collection, which is a list of books', () => {
        renderHeader('/collections/48/page/1');

        expect(screen.getByText('language')).toBeInTheDocument();
    });

    it('fits the label again when the pill arrives, after the first paint', () => {
        const tree = (methods: DonateMethod[]) => (
            <MemoryRouter>
                <Header donateMethods={methods} />
            </MemoryRouter>
        );
        const { rerender } = render(tree([]));
        const before = labelKeys.at(-1);

        rerender(tree(METHODS));

        expect(labelKeys.at(-1)).not.toBe(before);
    });

    it('opens the profile', async () => {
        renderHeader();

        await userEvent.click(screen.getByRole('button', { name: 'user' }));

        expect(profile).toHaveBeenCalledTimes(1);
    });

    it('opens the donate dialog from the pill', async () => {
        renderHeader();

        await userEvent.click(pill()[0]);

        expect(await screen.findByText('donate-modal')).toBeInTheDocument();
    });

    it('has no pill with nothing to offer', () => {
        renderHeader('/books/page/1', []);

        expect(pill()).toHaveLength(0);
    });

    it('keeps no theme and no admin link: those are in the foot and the profile', () => {
        authState.user = { username: 'root', is_superuser: true };
        renderHeader();

        expect(screen.queryByRole('button', { name: 'theme' })).toBeNull();
        expect(screen.queryByRole('link', { name: /adminTab/ })).toBeNull();
        authState.user = { username: 'reader', is_superuser: false };
    });
});

describe('Header band on a phone', () => {
    it('leaves as the reader goes down and comes back on the way up', () => {
        renderHeader();

        scrollTo(300);
        expect(band()).toHaveClass('-translate-y-full');

        scrollTo(200);
        expect(band()).not.toHaveClass('-translate-y-full');
    });

    it('stays out when the scroll down was a page landing, not the reader', () => {
        // Turning a page on a short list scrolls down to the first card, and the
        // band has to stand over it.
        renderHeader();
        const column = document.createElement('div');
        column.setAttribute(LIST_COLUMN_ATTR, '');
        column.getBoundingClientRect = () => ({ ...new DOMRect(), top: 344 });
        document.body.appendChild(column);

        scrollToLoadedList(true);
        scrollTo(300);
        column.remove();

        expect(band()).not.toHaveClass('-translate-y-full');

        // The reader's own next move counts again, from where the page landed.
        scrollTo(340);
        expect(band()).toHaveClass('-translate-y-full');
    });

    it('brings the pill and the profile with it once the bar has gone', () => {
        renderHeader();
        expect(band().querySelectorAll('a,button[aria-label="user"]')).toHaveLength(0);

        scrollTo(100);
        scrollTo(60);

        expect(band().querySelector('a[href="/books/page/1"]')).not.toBeNull();
        expect(band().querySelector('button[aria-label="user"]')).not.toBeNull();
    });

    it('puts nothing green to the right of the field', () => {
        renderHeader();
        scrollTo(100);
        scrollTo(60);

        const field = screen.getByTestId('field');
        const inBand = [...band().querySelectorAll('button')].find((button) =>
            /Донат/.test(button.textContent ?? ''),
        );
        expect(inBand).toBeDefined();
        expect(before(inBand as Element, field)).toBe(true);
    });

    it('comes back when the width crosses back to a phone', () => {
        const { rerender } = renderHeader();
        scrollTo(300);
        expect(band()).toHaveClass('-translate-y-full');

        media.phone = false;
        rerender(
            <MemoryRouter>
                <Header donateMethods={METHODS} />
            </MemoryRouter>,
        );
        media.phone = true;
        rerender(
            <MemoryRouter>
                <Header donateMethods={METHODS} />
            </MemoryRouter>,
        );

        expect(band()).not.toHaveClass('-translate-y-full');
    });
});

describe('Header takeover', () => {
    const take = () => userEvent.click(screen.getByRole('button', { name: 'focus-field' }));

    it('gives the field the screen, without the bar or the pill', async () => {
        renderHeader();

        await take();

        expect(screen.getByTestId('field')).toHaveAttribute('data-taking', 'true');
        expect(band()).toHaveClass('fixed');
        // Hidden, not removed: the bar holds the page where it was.
        expect(pill()[0].parentElement).toHaveClass('invisible');
    });

    it('stands open even where the band had gone', async () => {
        renderHeader();
        scrollTo(300);

        await take();

        expect(band()).not.toHaveClass('-translate-y-full');
    });

    it('holds the page still under it', async () => {
        renderHeader();

        await take();
        expect(document.body.style.overflow).toBe('hidden');

        await userEvent.click(screen.getByRole('button', { name: 'finish-search' }));
        expect(document.body.style.overflow).toBe('');
    });

    it('has one way back, and that puts the page’s own words back in the box', async () => {
        renderHeader();
        await take();

        await userEvent.click(screen.getByRole('button', { name: 'search.close' }));

        expect(screen.getByTestId('field')).toHaveAttribute('data-taking', 'false');
        expect(search.restoreWords).toHaveBeenCalledTimes(1);
    });

    it('keeps the words of a finished search', async () => {
        renderHeader();
        await take();

        await userEvent.click(screen.getByRole('button', { name: 'finish-search' }));

        expect(search.restoreWords).not.toHaveBeenCalled();
    });

    it('ends at any arrival somewhere else', async () => {
        renderHeader();
        await take();

        await userEvent.click(screen.getByRole('button', { name: 'go' }));

        expect(screen.getByTestId('field')).toHaveAttribute('data-taking', 'false');
    });
});

describe('Header takeover across the phone boundary', () => {
    const tree = () => (
        <MemoryRouter>
            <Header donateMethods={METHODS} />
        </MemoryRouter>
    );

    const openOnPhone = async () => {
        const view = render(tree());
        await userEvent.click(screen.getByRole('button', { name: 'focus-field' }));
        expect(document.body.style.overflow).toBe('hidden');
        return view;
    };

    it('gives the page back when the width leaves the phone', async () => {
        // Rotating a phone with search open lands on a header with no arrow to
        // close it, so the lock has to go with the layout.
        const { rerender } = await openOnPhone();

        media.phone = false;
        rerender(tree());

        expect(document.body.style.overflow).toBe('');
        expect(screen.getByTestId('field')).toHaveAttribute('data-taking', 'false');
    });

    it('does not come back open when the width returns to the phone', async () => {
        const { rerender } = await openOnPhone();

        media.phone = false;
        rerender(tree());
        media.phone = true;
        rerender(tree());

        expect(screen.getByTestId('field')).toHaveAttribute('data-taking', 'false');
        expect(band()).not.toHaveClass('fixed');
        expect(document.body.style.overflow).toBe('');
    });

    it('keeps the words typed across the crossing', async () => {
        // A rotation is not a change of mind, and the box still holds the draft.
        const { rerender } = await openOnPhone();

        media.phone = false;
        rerender(tree());

        expect(search.restoreWords).not.toHaveBeenCalled();
    });

    it('can be opened again afterwards', async () => {
        const { rerender } = await openOnPhone();
        media.phone = false;
        rerender(tree());
        media.phone = true;
        rerender(tree());

        await userEvent.click(screen.getByRole('button', { name: 'focus-field' }));

        expect(band()).toHaveClass('fixed');
        expect(document.body.style.overflow).toBe('hidden');
    });
});

describe('Header from 601 up', () => {
    beforeEach(() => {
        media.phone = false;
    });

    it('keeps its row in the list column, on a band as wide as the screen', () => {
        renderHeader();

        const column = screen.getByTestId('field').parentElement as HTMLElement;
        expect(column).toHaveClass(...CONTENT_COLUMN.split(' '));
        for (const item of [
            screen.getByRole('link', { name: 'booksTab' }),
            screen.getByRole('button', { name: 'reader' }),
            screen.getByRole('button', { name: 'Выход' }),
            pill()[0],
        ]) {
            expect(column).toContainElement(item);
        }
        const band = screen.getByRole('banner');
        expect(band).toHaveClass('bg-chrome', 'w-full');
        expect(band.className).not.toContain('max-w');
    });

    it('holds the theme, the name and the way out', () => {
        renderHeader();

        expect(screen.getByRole('button', { name: 'theme' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'reader' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Выход' })).toBeInTheDocument();
    });

    it('never gives the field the screen', () => {
        renderHeader();

        expect(screen.getByTestId('field')).toHaveAttribute('data-taking', 'false');
        expect(screen.queryByTestId('search-band')).toBeNull();
    });

    it('keeps the pill left of the field until the field stops short', () => {
        media.wide = false;
        const { unmount } = renderHeader();
        expect(before(pill()[0], screen.getByTestId('field'))).toBe(true);
        unmount();

        media.wide = true;
        renderHeader();
        expect(before(screen.getByTestId('field'), pill()[0])).toBe(true);
    });

    it('has no pill with nothing to offer', () => {
        renderHeader('/books/page/1', []);

        expect(pill()).toHaveLength(0);
    });
});
