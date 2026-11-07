import React from 'react';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useNavigate } from 'react-router';

import { LIST_COLUMN_ATTR } from '@/shared/layout/breakpoints';
import {
    PAGE_TURN,
    scrollToLoadedList,
    takeLanding,
    usePageTurn,
} from '@/shared/layout/landOnList';

// A turned page opens on its first card; every other arrival opens at the top.
// jsdom has no layout, so the column's position is lent to it.

const column = (top: number) => {
    const el = document.createElement('div');
    el.setAttribute(LIST_COLUMN_ATTR, '');
    el.getBoundingClientRect = () => ({ ...new DOMRect(), top });
    document.body.appendChild(el);
    return el;
};

beforeEach(() => {
    window.scrollTo = vi.fn();
    Object.defineProperty(window, 'scrollY', { value: 0, configurable: true, writable: true });
    takeLanding();
});

afterEach(() => {
    document.querySelectorAll(`[${LIST_COLUMN_ATTR}]`).forEach((el) => el.remove());
});

describe('scrollToLoadedList', () => {
    it('opens at the top on anything but a page turn', () => {
        column(500);

        scrollToLoadedList(false);

        expect(window.scrollTo).toHaveBeenCalledWith(0, 0);
        expect(takeLanding()).toBe(false);
    });

    it('puts the list under the header on a page turn', () => {
        column(500);

        scrollToLoadedList(true);

        expect(window.scrollTo).toHaveBeenCalledWith(0, 452);
    });

    it('measures from where the page stands, not from the top of the screen', () => {
        (window as { scrollY: number }).scrollY = 3000;
        column(-2500);

        scrollToLoadedList(true);

        expect(window.scrollTo).toHaveBeenCalledWith(0, 452);
    });

    it('never asks for a place above the page', () => {
        column(20);

        scrollToLoadedList(true);

        expect(window.scrollTo).toHaveBeenCalledWith(0, 0);
    });

    it('opens at the top when there is no list to land on', () => {
        scrollToLoadedList(true);

        expect(window.scrollTo).toHaveBeenCalledWith(0, 0);
    });

    it('tells the header once that this scroll was not the reader', () => {
        column(500);

        scrollToLoadedList(true);

        expect(takeLanding()).toBe(true);
        expect(takeLanding()).toBe(false);
    });

    it('owes the header nothing when the page is already there', () => {
        // No scroll event follows, so the mark would swallow the reader's next one.
        (window as { scrollY: number }).scrollY = 452;
        column(48);

        scrollToLoadedList(true);

        expect(takeLanding()).toBe(false);
    });
});

describe('usePageTurn', () => {
    let turned = false;
    const Probe: React.FC = () => {
        turned = usePageTurn();
        const navigate = useNavigate();
        return (
            <>
                <button type="button" onClick={() => navigate('/b', { state: PAGE_TURN })}>
                    turn
                </button>
                <button type="button" onClick={() => navigate('/c')}>
                    plain
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
    const click = (name: string) => userEvent.click(screen.getByRole('button', { name }));

    it('is not a cold open', () => {
        render(
            <MemoryRouter initialEntries={[{ pathname: '/a', state: PAGE_TURN }]}>
                <Probe />
            </MemoryRouter>,
        );

        // A reload brings the state back from history as a pop.
        expect(turned).toBe(false);
    });

    it('is a push that says so', async () => {
        render(
            <MemoryRouter initialEntries={['/a']}>
                <Probe />
            </MemoryRouter>,
        );

        await click('turn');
        expect(turned).toBe(true);

        await click('plain');
        expect(turned).toBe(false);
    });

    it('is neither Back nor Forward, whatever the history holds', async () => {
        render(
            <MemoryRouter initialEntries={['/a']}>
                <Probe />
            </MemoryRouter>,
        );

        await click('turn');
        await click('plain');
        await click('back');
        expect(turned).toBe(false);

        await act(async () => {});
        await click('forward');
        expect(turned).toBe(false);
    });
});
