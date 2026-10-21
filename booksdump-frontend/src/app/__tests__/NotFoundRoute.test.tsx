import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes } from 'react-router';

import notFoundRoutes from '@/app/routes/notFoundRoutes';
import { AuthRoomProvider } from '@/features/auth/scene/AuthRoom';

const translate = (key: string) => key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));
vi.mock('@/features/auth/scene/AuthScene', () => ({
    default: () => <div data-testid="auth-scene" />,
}));

describe('NotFoundRoute', () => {
    it('answers any unknown path with the error page in the reading room, titled as an error', async () => {
        render(
            <MemoryRouter initialEntries={['/no/such/page']}>
                <AuthRoomProvider>
                    <Routes>{notFoundRoutes}</Routes>
                </AuthRoomProvider>
            </MemoryRouter>,
        );

        expect(screen.getByRole('heading', { name: 'errorPage.title' })).toBeInTheDocument();
        expect(screen.getByTestId('auth-scene')).toBeInTheDocument();
        await waitFor(() => expect(document.title).toBe('routeTitles.error'));
    });
});
