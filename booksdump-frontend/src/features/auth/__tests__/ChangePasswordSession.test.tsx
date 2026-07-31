import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';

import * as authApi from '@/api/auth';
import { AuthProvider } from '@/context/AuthContext';
import PublicRoute from '@/app/PublicRoute';
import ChangePassword from '@/features/auth/ChangePassword';

// What a reader following a password reset link actually gets, with the real
// AuthProvider rather than a stubbed one — because the answer turns on what
// that provider does on this particular path, and a stub would answer for it.

const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

// CenteredBox carries the interface-language toggle, which wants a provider of
// its own. Which language the chrome is in has no bearing on any of this.
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));

vi.mock('@/api/auth', () => ({
    logout: vi.fn().mockResolvedValue(undefined),
    activate: vi.fn().mockResolvedValue({}),
    changePassword: vi.fn().mockResolvedValue({}),
    getCsrfToken: vi.fn().mockResolvedValue({ csrf_token: 'csrf' }),
    getCurrentUser: vi.fn(),
    getInit: vi.fn(),
    refreshSession: vi.fn().mockResolvedValue(undefined),
    updateCurrentUser: vi.fn(),
}));

const reader = {
    username: 'reader',
    first_name: '',
    last_name: '',
    is_superuser: false,
    books_lang: 'ru',
};

const renderAt = (path: string) =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <AuthProvider>
                <Routes>
                    <Route element={<PublicRoute />}>
                        <Route path="/change-password/:token" element={<ChangePassword />} />
                    </Route>
                    <Route path="/books/page/1" element={<div>the catalogue</div>} />
                </Routes>
            </AuthProvider>
        </MemoryRouter>,
    );

beforeEach(() => {
    vi.clearAllMocks();
    window.history.replaceState({}, '', '/change-password/tok');
});

describe('following a password reset link', () => {
    // The provider skips loading the user on this path, so a browser holding a
    // session still arrives with none — which is what keeps the form reachable.
    it('shows the form even when the browser holds a session', async () => {
        vi.mocked(authApi.getInit).mockResolvedValue({ csrf_token: 'csrf', user: reader } as never);
        vi.mocked(authApi.getCurrentUser).mockResolvedValue(reader as never);

        renderAt('/change-password/tok');

        expect(await screen.findByLabelText(/newPassword/)).toBeInTheDocument();
        expect(screen.queryByText('the catalogue')).not.toBeInTheDocument();
    });

    // And it must not have asked who the reader is on the way.
    it('does not load the account on this path', async () => {
        vi.mocked(authApi.getInit).mockResolvedValue({ csrf_token: 'csrf', user: reader } as never);

        renderAt('/change-password/tok');

        await waitFor(() => expect(authApi.getCsrfToken).toHaveBeenCalled());
        expect(authApi.getInit).not.toHaveBeenCalled();
        expect(authApi.getCurrentUser).not.toHaveBeenCalled();
    });

    it('checks the token before offering the field', async () => {
        vi.mocked(authApi.getInit).mockResolvedValue({ csrf_token: 'csrf' } as never);

        renderAt('/change-password/tok');

        await waitFor(() => expect(authApi.activate).toHaveBeenCalledWith('tok'));
        expect(await screen.findByLabelText(/newPassword/)).toBeInTheDocument();
    });
});
