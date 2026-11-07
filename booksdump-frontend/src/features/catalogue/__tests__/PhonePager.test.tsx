import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router';

import BookPagination from '@/features/catalogue/BookPagination';
import { PHONE_QUERY, listColumn } from '@/shared/layout/breakpoints';

// The phone's pager: one row, no numbers. What a reader needs from it is where
// they are, the next and the previous page as real addresses, and a way to any
// other page through the page text.

const translate = (key: string, options?: Record<string, unknown>) =>
    options ? `${key}:${JSON.stringify(options)}` : key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: (query: string) => query === PHONE_QUERY,
    default: (query: string) => query === PHONE_QUERY,
}));

beforeAll(() => {
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
    Element.prototype.hasPointerCapture ??= () => false;
});

let seen = { path: '', state: null as unknown };
const LocationProbe: React.FC = () => {
    const { pathname, search, state } = useLocation();
    React.useEffect(() => {
        seen = { path: pathname + search, state };
    }, [pathname, search, state]);
    return null;
};

function renderPager(props: Partial<React.ComponentProps<typeof BookPagination>> = {}) {
    const baseUrl = props.baseUrl ?? '/books/page/3';
    return render(
        <MemoryRouter initialEntries={[baseUrl]}>
            <LocationProbe />
            <div {...listColumn}>
                <BookPagination totalPages={48159} currentPage={3} baseUrl={baseUrl} {...props} />
            </div>
        </MemoryRouter>,
    );
}

const pageText = () => screen.getByRole('button', { name: /^pager\.whereLabel/ });

describe('the phone pager', () => {
    it('draws nothing for a list of one page', () => {
        const { container } = renderPager({ totalPages: 1, currentPage: 1 });

        expect(container.querySelector('nav')).toBeNull();
    });

    it('says where the reader is, with no page numbers to tap', () => {
        renderPager();

        expect(pageText()).toHaveTextContent('pager.where');
        expect(pageText().textContent).toContain('"page":"3"');
        expect(pageText().textContent).toContain('48 159');
        expect(screen.queryByRole('link', { name: /goToPage/ })).toBeNull();
        expect(document.querySelector('[data-slot="pagination-probe"]')).toBeNull();
    });

    it('steps by real addresses', () => {
        renderPager();

        expect(screen.getByRole('link', { name: 'previousPage' })).toHaveAttribute(
            'href',
            '/books/page/2',
        );
        expect(screen.getByRole('link', { name: 'nextPage' })).toHaveAttribute(
            'href',
            '/books/page/4',
        );
    });

    it('keeps the search on the addresses that page in the query', () => {
        renderPager({ baseUrl: '/search?q=%D0%BA%D0%B8%D0%BD%D0%B3&type=books&page=3' });

        expect(screen.getByRole('link', { name: 'nextPage' })).toHaveAttribute(
            'href',
            '/search?q=%D0%BA%D0%B8%D0%BD%D0%B3&type=books&page=4',
        );
    });

    it('has no way back from the first page and no way on from the last', () => {
        const { unmount } = renderPager({ currentPage: 1, baseUrl: '/books/page/1' });
        const back = screen.getByLabelText('previousPage');
        expect(back).toHaveAttribute('aria-disabled', 'true');
        expect(back).not.toHaveAttribute('href');
        unmount();

        renderPager({ currentPage: 48159, baseUrl: '/books/page/48159' });
        const on = screen.getByLabelText('nextPage');
        expect(on).toHaveAttribute('aria-disabled', 'true');
        expect(on).not.toHaveAttribute('href');
    });

    it('marks a step as a page turn, so the list can land under the header', async () => {
        renderPager();

        await userEvent.click(screen.getByRole('link', { name: 'nextPage' }));

        expect(seen.path).toBe('/books/page/4');
        expect(seen.state).toEqual({ pageTurn: true });
    });

    describe('a click that means somewhere else', () => {
        /** Clicks and reports whether the click reached the document uncancelled. */
        const click = (init: MouseEventInit) => {
            const link = screen.getByRole('link', { name: 'nextPage' });
            let cancelled: boolean | undefined;
            // Read after React has had it, then cancelled, because jsdom has no
            // navigation to perform and says so on the console.
            document.addEventListener(
                'click',
                (event) => {
                    cancelled = event.defaultPrevented;
                    event.preventDefault();
                },
                { once: true },
            );
            fireEvent.click(link, init);
            return cancelled;
        };

        it.each([
            ['Ctrl', { ctrlKey: true }],
            ['Cmd', { metaKey: true }],
            ['Shift', { shiftKey: true }],
            ['Alt', { altKey: true }],
            ['the middle button', { button: 1 }],
        ])('leaves %s to the browser', (_, init) => {
            renderPager();

            expect(click(init)).toBe(false);
            expect(seen.path).toBe('/books/page/3');
        });

        it('takes a plain click from the browser', () => {
            renderPager();

            expect(click({})).toBe(true);
            expect(seen.path).toBe('/books/page/4');
        });
    });
});

describe('the jump sheet', () => {
    const open = async () => {
        await userEvent.click(pageText());
        return screen.findByRole('dialog');
    };

    it('opens from the page text and asks for a number', async () => {
        renderPager();

        const sheet = await open();

        expect(within(sheet).getByText('pager.jumpTitle')).toBeInTheDocument();
        expect(sheet.textContent).toContain('pager.jumpRange');
        expect(within(sheet).getByRole('textbox')).toHaveAttribute('inputmode', 'numeric');
    });

    it('puts the caret in the field', async () => {
        renderPager();

        const sheet = await open();

        expect(within(sheet).getByRole('textbox')).toHaveFocus();
    });

    it('goes to the page from the button, as a page turn', async () => {
        renderPager();
        const sheet = await open();

        await userEvent.type(within(sheet).getByRole('textbox'), '1234');
        await userEvent.click(within(sheet).getByRole('button', { name: 'pager.jumpGo' }));

        expect(seen.path).toBe('/books/page/1234');
        expect(seen.state).toEqual({ pageTurn: true });
    });

    it('goes to the page from Enter', async () => {
        renderPager();
        const sheet = await open();

        await userEvent.type(within(sheet).getByRole('textbox'), '77{Enter}');

        expect(seen.path).toBe('/books/page/77');
    });

    it('holds a number past the end to the last page', async () => {
        renderPager();
        const sheet = await open();

        await userEvent.type(within(sheet).getByRole('textbox'), '99999{Enter}');

        expect(seen.path).toBe('/books/page/48159');
    });

    it('stays put on an empty field', async () => {
        renderPager();
        const sheet = await open();

        await userEvent.type(within(sheet).getByRole('textbox'), '{Enter}');

        expect(seen.path).toBe('/books/page/3');
        expect(screen.getByRole('dialog')).toBeInTheDocument();
    });

    it('closes without moving when asked for the page already open', async () => {
        renderPager();
        const sheet = await open();

        await userEvent.type(within(sheet).getByRole('textbox'), '3{Enter}');

        expect(seen.state).toBeNull();
        expect(screen.queryByRole('dialog')).toBeNull();
    });

    it('hands the keyboard back to the page text when it closes', async () => {
        // Left to the sheet, focus lands on <body>: measured in Chrome, on both
        // shells, which puts a keyboard reader at the top of the list.
        renderPager();
        const opener = pageText();
        await open();

        await userEvent.keyboard('{Escape}');

        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        expect(opener).toHaveFocus();
    });

    it('does not open while the total is still on its way', async () => {
        renderPager({ pending: true });

        expect(pageText()).toBeDisabled();
    });
});
