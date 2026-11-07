import { NavigationType, useLocation, useNavigationType } from 'react-router';

import { LIST_COLUMN_SELECTOR } from '@/shared/layout/breakpoints';

/**
 * A page turned on a phone opens on its first card instead of on the chrome:
 * the reader asked for more of the list, and the header and the section row
 * above it are a scroll away if they want them.
 *
 * Only the turn does this. Everything else keeps the old road, scrollTo(0, 0),
 * after which the browser may restore a position of its own on Back or Forward.
 * "The page changed" is not the test: changing the language pushes page one as
 * well. The phone's pager says what it is doing in the router's state instead,
 * and only a push counts, because history hands that state back on a reload and
 * on Back.
 *
 * On a desktop the header is static, so landing would scroll the field away.
 * That is why only the phone's pager sends the state.
 */

/** The router state a phone's pager navigates with. */
export const PAGE_TURN = { pageTurn: true };

/** The header stands over the page on a phone, so the list lands below it. */
const PHONE_HEADER_HEIGHT = 48;

/** Whether the location the reader just arrived at is a turned page. */
export function usePageTurn(): boolean {
    const navigationType = useNavigationType();
    const { state } = useLocation();
    return (
        navigationType === NavigationType.Push &&
        (state as { pageTurn?: boolean } | null)?.pageTurn === true
    );
}

let landing = false;

/**
 * scrollToLoadedList puts a freshly loaded list where the reader should meet it.
 *
 * Ten skeleton cards keep the page tall while the data is on its way, so the
 * place is there to scroll to before the books are.
 */
export function scrollToLoadedList(turned: boolean): void {
    const column = turned ? document.querySelector(LIST_COLUMN_SELECTOR) : null;
    if (!column) {
        window.scrollTo(0, 0);
        return;
    }
    const y = Math.max(
        0,
        Math.round(column.getBoundingClientRect().top + window.scrollY - PHONE_HEADER_HEIGHT),
    );
    // A scroll to where the page already stands fires no event, and a mark left
    // behind would swallow the reader's next real one.
    landing = y !== Math.round(window.scrollY);
    window.scrollTo(0, y);
}

/**
 * takeLanding reports, once, that the scroll in progress was this and not the
 * reader. The header hides on a scroll down, and a landing can be one: it has
 * to stand over the first card all the same.
 */
export function takeLanding(): boolean {
    const was = landing;
    landing = false;
    return was;
}
