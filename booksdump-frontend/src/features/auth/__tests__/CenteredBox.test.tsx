import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

import CenteredBox from '@/features/auth/CenteredBox';

// t must keep a stable identity across renders: a mock that returns a fresh
// function every call spins any component with an effect keyed on t.
const translate = (key: string) => key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

function renderBox(children: React.ReactNode = <p>content</p>) {
    return render(
        <MemoryRouter>
            <CenteredBox>{children}</CenteredBox>
        </MemoryRouter>,
    );
}

describe('CenteredBox', () => {
    it('renders whatever screen it wraps', () => {
        renderBox(<p>content</p>);

        expect(screen.getByText('content')).toBeInTheDocument();
    });

    it('centres its card and stops it growing with the viewport', () => {
        const { container } = renderBox();

        // Pixels are the browser's business; what the wrapper owes the screens
        // is a centred card that stops widening at some point.
        const wrapper = container.firstElementChild as HTMLElement;
        expect(wrapper.className).toContain('items-center');
        expect(wrapper.className).toContain('justify-center');

        const card = container.querySelector('[data-slot="card"]') as HTMLElement;
        expect(card).not.toBeNull();
        expect(card.className).toMatch(/max-w-/);
    });

    it('keeps the registration shortcut reachable', () => {
        renderBox();

        // The decoration in the corner is the only route to registration from
        // any auth screen, so it has to carry a name and a destination.
        expect(screen.getByRole('link', { name: 'registration' })).toHaveAttribute(
            'href',
            '/registration',
        );
    });
});
