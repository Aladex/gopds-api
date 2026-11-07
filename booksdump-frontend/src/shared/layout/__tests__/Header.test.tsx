import React from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import Header from '@/shared/layout/Header';
import { LIST_COLUMN_ATTR } from '@/shared/layout/breakpoints';
import { scrollToLoadedList } from '@/shared/layout/landOnList';

const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

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
            <Header donateMethods={[]} />
        </MemoryRouter>,
    );

beforeEach(() => {
    vi.clearAllMocks();
    narrow.current = false;
    authState.user = { username: 'reader', is_superuser: false };
});

describe('Header', () => {
    it('leaves the sections to the row under it', () => {
        renderHeader('/books/page/1');

        expect(screen.queryByRole('navigation')).toBeNull();
        expect(screen.queryByRole('link', { name: /Подборки/ })).toBeNull();
        expect(screen.getByRole('link', { name: 'booksTab' })).toHaveAttribute(
            'href',
            '/books/page/1',
        );
    });

    /*
     * The bar at the foot used to hold the profile, and it was the only way to
     * it on a phone. It has to stay one tap away, with the only logout behind it.
     */
    describe('on a phone', () => {
        beforeEach(() => {
            narrow.current = true;
        });

        it('opens the profile', async () => {
            const open = vi.fn();
            render(
                <MemoryRouter>
                    <Header donateMethods={[]} onOpenProfile={open} />
                </MemoryRouter>,
            );

            await userEvent.click(screen.getByRole('button', { name: 'user' }));

            expect(open).toHaveBeenCalledTimes(1);
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

describe('Header and a landed page', () => {
    const scrollTo = (y: number) =>
        act(() => {
            Object.defineProperty(window, 'scrollY', { value: y, configurable: true });
            window.dispatchEvent(new Event('scroll'));
        });

    beforeEach(() => {
        Object.defineProperty(window, 'scrollY', { value: 0, configurable: true });
        window.scrollTo = vi.fn();
    });

    afterEach(() => {
        document.querySelectorAll(`[${LIST_COLUMN_ATTR}]`).forEach((el) => el.remove());
    });

    it('hides as the reader scrolls down', () => {
        narrow.current = true;
        renderHeader('/books/page/1');

        scrollTo(300);

        expect(screen.getByRole('banner')).toHaveClass('-translate-y-full');
    });

    it('stays out when the scroll down was a page landing, not the reader', () => {
        // Turning a page on a short list scrolls down to the first card, and the
        // header has to stand over it.
        narrow.current = true;
        renderHeader('/books/page/1');
        const column = document.createElement('div');
        column.setAttribute(LIST_COLUMN_ATTR, '');
        column.getBoundingClientRect = () => ({ ...new DOMRect(), top: 348 });
        document.body.appendChild(column);

        scrollToLoadedList(true);
        scrollTo(300);

        expect(screen.getByRole('banner')).not.toHaveClass('-translate-y-full');

        // The reader's own next move counts again, from where the page landed.
        scrollTo(340);
        expect(screen.getByRole('banner')).toHaveClass('-translate-y-full');
    });
});
