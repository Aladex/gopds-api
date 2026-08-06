import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

import Header from '@/shared/layout/Header';

// The section mark is one bar that moves between links rather than a border
// that appears on one and vanishes from another. jsdom lays nothing out, so
// where the bar ends up is not checkable here — it was measured in a browser.
// What these hold is the shape that makes the movement possible at all: a
// single bar, kept out of the accessibility tree, and links that no longer
// carry the mark themselves.

const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/api/system', () => ({ getDonateMethods: vi.fn().mockResolvedValue([]) }));

const authState = {
    logout: vi.fn(),
    user: { username: 'reader', is_superuser: false },
};
vi.mock('@/context/AuthContext', () => ({ useAuth: () => authState }));

// jsdom has no viewport; the wide layout is the one that carries the nav, and
// narrow() switches to the other for the tests that are about it.
const narrow = { current: false };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => narrow.current,
    default: () => narrow.current,
}));

vi.mock('@/shared/layout/ThemeToggle', () => ({ default: () => null }));

const renderHeader = (path: string) =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <Header />
        </MemoryRouter>,
    );

/** The bar is the only element in the nav kept out of the accessibility tree. */
const underline = (container: HTMLElement) => container.querySelectorAll('nav span[aria-hidden]');

beforeEach(() => {
    vi.clearAllMocks();
    narrow.current = false;
    authState.user = { username: 'reader', is_superuser: false };
});

describe('Header navigation', () => {
    it('draws exactly one mark, however many links there are', async () => {
        const { container } = renderHeader('/books/page/1');

        await waitFor(() => expect(screen.getByRole('navigation')).toBeInTheDocument());
        expect(underline(container)).toHaveLength(1);
    });

    it('keeps the mark out of the accessibility tree', async () => {
        const { container } = renderHeader('/books/page/1');

        await waitFor(() => expect(screen.getByRole('navigation')).toBeInTheDocument());
        // Which link is current is said by the link, not by a decorative bar.
        expect(underline(container)[0]).toHaveAttribute('aria-hidden');
        expect(screen.getByRole('link', { name: /booksTab/ })).toHaveAttribute(
            'aria-current',
            'page',
        );
    });

    // The border used to be the mark, switching from transparent to white on
    // whichever link was current. Left in place it would fight the bar.
    it('no longer marks the current link with its own border', async () => {
        renderHeader('/collections');

        // This one is asked for with a fallback, which the stub above returns
        // in preference to the key.
        const current = await screen.findByRole('link', { name: /Подборки/ });
        expect(current).toHaveAttribute('aria-current', 'page');
        expect(current.className).not.toMatch(/border-white/);
        expect(current.className).toMatch(/transition-colors/);
    });

    it('marks the section a deep route belongs to, not just its landing page', async () => {
        renderHeader('/books/page/7');

        const current = await screen.findByRole('link', { name: /booksTab/ });
        expect(current).toHaveAttribute('aria-current', 'page');
    });

    // A route outside every section leaves nothing to mark, and the bar has to
    // be able to say so rather than sit under whichever link came first.
    it('marks nothing on a route belonging to no section', async () => {
        const { container } = renderHeader('/authors/tolkien/1');

        await waitFor(() => expect(screen.getByRole('navigation')).toBeInTheDocument());
        expect(screen.queryByRole('link', { current: 'page' })).not.toBeInTheDocument();
        expect(underline(container)[0].className).toMatch(/opacity-0/);
    });

    /*
     * The OPDS instructions are a page read once while setting up a reading
     * application, not a section of the library, and they now live in the
     * profile beside the Telegram bot. The route stays — it was in the
     * navigation for years and will be bookmarked — so the header has to go on
     * marking nothing rather than marking whichever link comes first.
     */
    it('offers no OPDS section, and marks nothing on its old route', async () => {
        const { container } = renderHeader('/catalog');

        await waitFor(() => expect(screen.getByRole('navigation')).toBeInTheDocument());
        expect(screen.queryByRole('link', { name: /opds/i })).not.toBeInTheDocument();
        expect(underline(container)[0].className).toMatch(/opacity-0/);
    });

    it('gives a superuser the admin link and an ordinary reader none', async () => {
        const { unmount } = renderHeader('/books/page/1');
        await waitFor(() => expect(screen.getByRole('navigation')).toBeInTheDocument());
        expect(screen.queryByRole('link', { name: /adminTab/ })).not.toBeInTheDocument();
        unmount();

        authState.user = { username: 'root', is_superuser: true };
        renderHeader('/admin');
        expect(await screen.findByRole('link', { name: /adminTab/ })).toHaveAttribute(
            'aria-current',
            'page',
        );
    });

    /*
     * The narrow header shows no section links at all, and the bar at the foot
     * no longer carries the admin one. For a while that left no way to reach the
     * admin panel from a phone except by typing the address — the section was
     * not hidden, it was cut off. So the narrow header names it as an icon.
     */
    describe('on a phone', () => {
        beforeEach(() => {
            narrow.current = true;
        });

        it('offers a superuser the admin panel, since nothing else does', async () => {
            authState.user = { username: 'root', is_superuser: true };
            renderHeader('/books/page/1');

            const link = await screen.findByRole('link', { name: /adminTab/ });
            expect(link).toHaveAttribute('href', '/admin');
        });

        it('offers an ordinary reader nothing of the sort', async () => {
            renderHeader('/books/page/1');

            await waitFor(() =>
                expect(screen.getByRole('link', { name: /booksTab/ })).toBeInTheDocument(),
            );
            expect(screen.queryByRole('link', { name: /adminTab/ })).not.toBeInTheDocument();
        });

        it('marks it while the reader is inside it', async () => {
            authState.user = { username: 'root', is_superuser: true };
            renderHeader('/admin/users');

            expect(await screen.findByRole('link', { name: /adminTab/ })).toHaveAttribute(
                'aria-current',
                'page',
            );
        });
    });
});
