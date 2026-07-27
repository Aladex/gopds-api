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

    it('needs no translation to render', () => {
        const { container } = render(<AppSkeleton />);

        expect(container.textContent).toBe('');
    });
});
