import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';

import { RootRedirect } from '@/app/App';
import PrivateRoute from '@/app/PrivateRoute';

// The Telegram bot's «Открыть профиль» buttons (unlinked bot, expired or used
// link) all send the reader to the site's root with ?profile=telegram — the
// exact path, after the site's own address, that telegram/link.go builds. It
// must survive the redirect from the root and open Profile → Connections →
// Telegram, then leave the address clean.
const BOT_PROFILE_PATH = '/?profile=telegram';

vi.mock('@/context/AuthContext', () => ({
    useAuth: () => ({
        isAuthenticated: true,
        isLoaded: true,
        isLoading: false,
        user: { username: 'reader', is_superuser: false },
    }),
}));
vi.mock('@/shared/layout/Header', () => ({ default: () => null }));
vi.mock('@/shared/layout/Footer', () => ({ default: () => null }));
vi.mock('@/shared/layout/BottomNavigation', () => ({ default: () => null }));
vi.mock('@/features/catalogue/LanguagePrompt', () => ({ default: () => null }));
vi.mock('@/features/profile/ProfileDrawer', () => ({ default: () => null }));
vi.mock('@/features/profile/ProfileDialog', () => ({
    default: ({ open, section }: { open: boolean; section?: string }) => (
        <div data-testid="profile-dialog">{open ? `open:${section}` : 'closed'}</div>
    ),
}));

const Where: React.FC = () => {
    const location = useLocation();
    return <div data-testid="where">{location.pathname + location.search}</div>;
};

const renderAt = (path: string) =>
    render(
        <MemoryRouter initialEntries={[path]}>
            <Routes>
                <Route path="/" element={<RootRedirect />} />
                <Route
                    path="/books/page/:page"
                    element={
                        <PrivateRoute>
                            <Where />
                        </PrivateRoute>
                    }
                />
            </Routes>
        </MemoryRouter>,
    );

it('opens the Telegram connection from the link the bot sends', async () => {
    renderAt(BOT_PROFILE_PATH);

    await waitFor(() =>
        expect(screen.getByTestId('profile-dialog')).toHaveTextContent('open:telegram'),
    );
    await waitFor(() =>
        expect(screen.getByTestId('where')).toHaveTextContent(/^\/books\/page\/1$/),
    );
});

it('keeps any other query through the redirect from the root', () => {
    renderAt('/?q=war');
    expect(screen.getByTestId('where')).toHaveTextContent('/books/page/1?q=war');
    expect(screen.getByTestId('profile-dialog')).toHaveTextContent('closed');
});
