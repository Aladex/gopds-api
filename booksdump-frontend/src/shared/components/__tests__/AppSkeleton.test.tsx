import React from 'react';
import { render } from '@testing-library/react';

import AppSkeleton from '@/shared/components/AppSkeleton';

// AppSkeleton stands in for the whole application while the session and the
// language load, so it renders before i18n exists and must carry no text of its
// own — and nothing for a screen reader to read out either.
describe('AppSkeleton', () => {
    it('renders a placeholder for the top bar and the list', () => {
        const { container } = render(<AppSkeleton />);

        expect(container.firstChild).toBeInTheDocument();
        expect(container.querySelectorAll('.animate-pulse').length).toBeGreaterThan(0);
    });

    it('is hidden from assistive technology', () => {
        const { container } = render(<AppSkeleton />);

        expect(container.firstElementChild).toHaveAttribute('aria-hidden', 'true');
    });

    it('holds the room the line over the list takes, so the cards do not jump', () => {
        // ListLine is an 8px gap on a phone and 40px from 601 up. jsdom lays
        // nothing out; the browser check measured both against the real list.
        const { container } = render(<AppSkeleton />);

        const cards = container.querySelector('[data-testid="skeleton-list"]');
        expect(cards).toHaveClass('pt-2');
        expect(cards).toHaveClass('min-[601px]:pt-10');
    });

    it('needs no translation to render', () => {
        const { container } = render(<AppSkeleton />);

        expect(container.textContent).toBe('');
    });
});
