import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

import PrivateRoute from '@/app/PrivateRoute';
import * as systemApi from '@/api/system';

// The page ends in its own block on a phone and in the build line on a desktop,
// and both have to learn about donations from the one request.

vi.mock('@/context/AuthContext', () => ({
    useAuth: () => ({
        isAuthenticated: true,
        isLoaded: true,
        isLoading: false,
        user: { username: 'reader', is_superuser: false },
    }),
}));
vi.mock('@/api/system', () => ({ getDonateMethods: vi.fn() }));
vi.mock('@/shared/layout/Header', () => ({
    default: ({ donateMethods }: { donateMethods: unknown[] }) => (
        <div data-testid="header">{donateMethods.length}</div>
    ),
}));
vi.mock('@/shared/layout/Footer', () => ({ default: () => <div>footer</div> }));
vi.mock('@/shared/layout/PhoneFoot', () => ({
    default: ({ hasDonate }: { hasDonate: boolean }) => <div>foot:{String(hasDonate)}</div>,
}));
vi.mock('@/shared/layout/SectionRow', () => ({ default: () => <nav>sections</nav> }));
vi.mock('@/features/catalogue/LanguagePrompt', () => ({ default: () => null }));
vi.mock('@/features/catalogue/CatalogueSearchContext', () => ({
    CatalogueSearchProvider: ({ children }: { children: React.ReactNode }) => children,
}));
vi.mock('@/features/profile/ProfileDrawer', () => ({ default: () => null }));
vi.mock('@/features/profile/ProfileDialog', () => ({ default: () => null }));

const phone = { current: true };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => phone.current,
    default: () => phone.current,
}));

const getDonateMethods = vi.mocked(systemApi.getDonateMethods);
const method = { id: 'a', label: 'A', kind: 'link', value: 'x', qr: false } as const;

const renderRoute = () =>
    render(
        <MemoryRouter>
            <PrivateRoute>
                <div>page</div>
            </PrivateRoute>
        </MemoryRouter>,
    );

describe('PrivateRoute', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        phone.current = true;
        getDonateMethods.mockResolvedValue([method]);
    });

    it('ends a phone’s page in the foot, not the build line', async () => {
        renderRoute();

        await screen.findByText('foot:true');
        expect(screen.queryByText('footer')).toBeNull();
    });

    it('keeps the build line on a desktop', async () => {
        phone.current = false;
        renderRoute();

        await waitFor(() => expect(screen.getByTestId('header')).toHaveTextContent('1'));
        expect(screen.getByText('footer')).toBeInTheDocument();
        expect(screen.queryByText(/^foot:/)).toBeNull();
    });

    it('asks for the donate methods once for the header and the foot', async () => {
        renderRoute();

        await screen.findByText('foot:true');
        expect(screen.getByTestId('header')).toHaveTextContent('1');
        expect(getDonateMethods).toHaveBeenCalledTimes(1);
    });

    it('tells the foot when there is nothing to offer', async () => {
        getDonateMethods.mockResolvedValue([]);
        renderRoute();

        await waitFor(() => expect(getDonateMethods).toHaveBeenCalled());
        expect(screen.getByText('foot:false')).toBeInTheDocument();
    });

    it('holds the sections on every private page, in the page', () => {
        renderRoute();

        expect(screen.getByRole('main')).toContainElement(screen.getByText('sections'));
    });

    it('keeps no room at the foot for a bar that has gone', () => {
        renderRoute();

        expect(screen.getByRole('main').className).not.toContain('pb-20');
    });

    it('lets a short page end at the end of the screen', () => {
        // #root is already a screen tall. A second screen-tall box under the
        // header put a header's height of scroll on every short list.
        renderRoute();

        expect(screen.getByRole('main').className).not.toContain('min-h-screen');
    });
});
