import { useEffect } from 'react';
import { act, render, screen } from '@testing-library/react';
import { MemoryRouter, useNavigate } from 'react-router';

import AppWrapper from '@/app/App';

/*
 * The application shell is where the one reading room lives: above the
 * routes, so the public screens share it. Everything around it that talks to
 * the server is stood in for; the routes and the screens are real.
 */

const translate = (key: string) => key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/context/AuthContext', () => ({
    useAuth: () => ({ isLoaded: true, isAuthenticated: false, setUser: vi.fn() }),
}));
const { passThrough } = vi.hoisted(() => ({
    passThrough: ({ children }: { children?: unknown }) => children,
}));
vi.mock('@/context/FavContext', () => ({ FavProvider: passThrough }));
vi.mock('@/context/AuthorContext', () => ({ AuthorProvider: passThrough }));
vi.mock('@/context/SearchBarContext', () => ({ SearchBarProvider: passThrough }));
vi.mock('@/context/BookConversionContext', () => ({ BookConversionProvider: passThrough }));
vi.mock('@/context/WebSocketContext', () => ({ WebSocketProvider: passThrough }));
vi.mock('@/context/InterfaceLanguageContext', () => ({ InterfaceLanguageProvider: passThrough }));
vi.mock('@/app/routes/privateRoutes', () => ({ default: null }));
vi.mock('@/app/routes/adminRoutes', () => ({ default: null }));
vi.mock('@/shared/ui/sonner', () => ({ Toaster: () => null }));
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));

let mounts = 0;
vi.mock('@/features/auth/scene/AuthScene', async () => {
    const { useEffect } = await vi.importActual<typeof import('react')>('react');
    function FakeScene() {
        useEffect(() => {
            mounts++;
        }, []);
        return <div data-testid="auth-scene" />;
    }
    return { default: FakeScene };
});

const nav = { go: (path: string): void => void path };
const Navigator = () => {
    const navigate = useNavigate();
    useEffect(() => {
        nav.go = navigate;
    }, [navigate]);
    return null;
};

it('keeps one reading room behind the public screens, from the application shell', () => {
    mounts = 0;
    render(
        <MemoryRouter initialEntries={['/login']}>
            <Navigator />
            <AppWrapper />
        </MemoryRouter>,
    );
    expect(screen.getByTestId('auth-scene')).toBeInTheDocument();

    act(() => nav.go('/forgot-password'));
    act(() => nav.go('/registration'));
    act(() => nav.go('/login'));

    expect(screen.getAllByTestId('auth-scene')).toHaveLength(1);
    expect(mounts).toBe(1);
});
