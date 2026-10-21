import { memo, useEffect } from 'react';
import { act, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router';

import CenteredBox from '@/features/auth/CenteredBox';
import { AuthRoomProvider } from '@/features/auth/room/AuthRoom';

/*
 * The host re-renders whenever the room is wanted or not — a trip into the
 * application and back inside the grace is enough. The room under it must not
 * re-render for that: its props have to stay the same objects, so a memoised
 * room skips the render entirely.
 */

const translate = (key: string) => key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));
vi.mock('sonner', () => ({ toast: vi.fn() }));

const renders = vi.hoisted(() => ({ count: 0 }));
vi.mock('@/features/auth/room/RoomVideo', async () => {
    const { memo: memoise } = await vi.importActual<typeof import('react')>('react');
    // Memoised like the real one, counting the renders it does not skip.
    const Scene = memoise(function Scene() {
        renders.count++;
        return <div data-testid="auth-scene" />;
    });
    return { default: Scene };
});

const nav = { go: (path: string): void => void path };
const Navigator = () => {
    const navigate = useNavigate();
    useEffect(() => {
        nav.go = navigate;
    }, [navigate]);
    return null;
};
const Screen = memo(({ name }: { name: string }) => (
    <CenteredBox>
        <p>{name}</p>
    </CenteredBox>
));

it('does not re-render the room when the reader moves between screens', async () => {
    renders.count = 0;
    render(
        <MemoryRouter initialEntries={['/login']}>
            <AuthRoomProvider>
                <Navigator />
                <Routes>
                    <Route path="/login" element={<Screen name="login" />} />
                    <Route path="/forgot-password" element={<Screen name="forgot" />} />
                    <Route path="/registration" element={<Screen name="registration" />} />
                    <Route path="/books" element={<p>books</p>} />
                </Routes>
            </AuthRoomProvider>
        </MemoryRouter>,
    );
    await screen.findByTestId('auth-scene');
    const first = renders.count;
    expect(first).toBeGreaterThan(0);

    act(() => nav.go('/forgot-password'));
    act(() => nav.go('/registration'));
    act(() => nav.go('/login'));
    // Out to the application and straight back: the host hides the room and
    // shows it again, re-rendering twice.
    act(() => nav.go('/books'));
    act(() => nav.go('/login'));

    expect(renders.count).toBe(first);
});
