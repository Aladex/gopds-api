import { useEffect, useState } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router';
import { toast } from 'sonner';

import CenteredBox from '@/features/auth/CenteredBox';
import { AuthRoomProvider, ROOM_GRACE_MS } from '@/features/auth/room/AuthRoom';

/*
 * One reading room for every screen that shows it: one video element, playing
 * on, while the reader goes from the sign-in screen to the forgotten password,
 * to registration and back — or to the error page, whose torn book is another
 * recording on the same element. The screens here are the real frame
 * (CenteredBox) under the real host and the real video component.
 */

const translate = (key: string) => key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));
vi.mock('sonner', () => ({ toast: vi.fn() }));

/** Navigate from outside the tree, as a click on a link would. */
const nav = { go: (path: string): void => void path };
const Navigator = () => {
    const navigate = useNavigate();
    useEffect(() => {
        nav.go = navigate;
    }, [navigate]);
    return null;
};

const Screen = ({ name, torn }: { name: string; torn?: boolean }) => (
    <CenteredBox torn={torn}>
        <p>{name}</p>
    </CenteredBox>
);

/** Let the lazily loaded player arrive. */
const settle = () =>
    act(async () => {
        await vi.dynamicImportSettled();
    });

async function renderApp(start = '/login') {
    const result = render(
        <MemoryRouter initialEntries={[start]}>
            <AuthRoomProvider>
                <Navigator />
                <Routes>
                    <Route path="/login" element={<Screen name="login" />} />
                    <Route path="/forgot-password" element={<Screen name="forgot" />} />
                    <Route path="/registration" element={<Screen name="registration" />} />
                    <Route path="/change-password/:token" element={<Screen name="change" />} />
                    <Route path="/activate/:token" element={<Screen name="activate" />} />
                    {/* The application proper: no room. */}
                    <Route path="/books" element={<p>books</p>} />
                    <Route path="*" element={<Screen name="404" torn />} />
                </Routes>
            </AuthRoomProvider>
        </MemoryRouter>,
    );
    await settle();
    return result;
}

const navigate = (path: string) => act(() => nav.go(path));
const video = () => document.querySelector('video');

beforeEach(() => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {});
    vi.spyOn(console, 'info').mockImplementation(() => {});
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 900 });
});

afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
});

describe('AuthRoomProvider: one room for every public screen', () => {
    it('keeps one video element playing across every room screen, the error page included', async () => {
        await renderApp('/login');
        const first = video();
        expect(first).not.toBeNull();
        expect(first!.getAttribute('src')).toBe('/auth-room/desktop.mp4');

        for (const path of [
            '/forgot-password',
            '/registration',
            '/change-password/abc',
            '/activate/xyz',
            '/login',
        ]) {
            navigate(path);
            expect(video()).toBe(first);
        }

        // The error page: the torn book, on the same element.
        navigate('/no/such/page');
        expect(video()).toBe(first);
        expect(first!.getAttribute('src')).toBe('/auth-room/desktop-torn.mp4');

        navigate('/login');
        expect(video()).toBe(first);
        expect(first!.getAttribute('src')).toBe('/auth-room/desktop.mp4');
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS * 3));
        expect(video()).toBe(first);
        expect(screen.getByText('login')).toBeInTheDocument();
    });

    it('starts on the torn book when the error page is where the reader lands', async () => {
        await renderApp('/no/such/page');

        expect(video()!.getAttribute('src')).toBe('/auth-room/desktop-torn.mp4');
    });

    it('takes the room down once, after the grace, when the reader enters the application', async () => {
        await renderApp('/login');

        navigate('/books');
        // Out of sight at once: the application must not show through to it.
        expect(document.querySelector('[data-auth-room]')).toHaveAttribute('hidden');
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS - 1));
        expect(video()).not.toBeNull();

        act(() => vi.advanceTimersByTime(1));
        expect(video()).toBeNull();
    });

    it('keeps the same video through a quick trip out and back inside the grace', async () => {
        await renderApp('/login');
        const first = video();

        for (let i = 0; i < 4; i++) {
            navigate('/books');
            act(() => vi.advanceTimersByTime(ROOM_GRACE_MS / 2));
            navigate(i % 2 ? '/login' : '/registration');
            expect(video()).toBe(first);
        }
        // The grace restarts with every exit: an old timer must not fire late.
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS * 3));
        expect(video()).toBe(first);
    });

    it('builds a new room when the reader comes back after it was taken down', async () => {
        await renderApp('/login');
        const first = video();
        navigate('/books');
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS));

        navigate('/login');
        await settle();

        expect(video()).not.toBeNull();
        expect(video()).not.toBe(first);
    });

    it('sends whoever catches the golden post to registration, and says why', async () => {
        await renderApp('/login');

        fireEvent.click(document.querySelector('[data-golden-post]')!);

        expect(screen.getByText('registration')).toBeInTheDocument();
        expect(toast).toHaveBeenCalledWith('goldenPost');
    });

    it('stops waiting to take the room down when the whole host goes', async () => {
        const { unmount } = await renderApp('/login');

        unmount();

        expect(video()).toBeNull();
        expect(vi.getTimerCount()).toBe(0);
    });

    it('drops a grace already running when the whole host goes', async () => {
        const { unmount } = await renderApp('/login');
        navigate('/books');
        expect(vi.getTimerCount()).toBe(1);

        unmount();

        expect(vi.getTimerCount()).toBe(0);
    });
});

describe('AuthRoomProvider counting screens', () => {
    it('counts each screen on its own: the room stays while any of them is mounted', async () => {
        let dropFirst: () => void = () => {};
        const Two = () => {
            const [both, setBoth] = useState(true);
            useEffect(() => {
                dropFirst = () => setBoth(false);
            }, []);
            return (
                <>
                    {both && <Screen name="whole" />}
                    <Screen name="torn" torn />
                </>
            );
        };
        render(
            <MemoryRouter>
                <AuthRoomProvider>
                    <Two />
                </AuthRoomProvider>
            </MemoryRouter>,
        );
        await settle();
        // Either screen asking for the torn book is enough.
        expect(video()!.getAttribute('src')).toBe('/auth-room/desktop-torn.mp4');

        act(() => dropFirst());
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS * 3));

        expect(document.querySelector('[data-auth-room]')).not.toHaveAttribute('hidden');
        expect(video()!.getAttribute('src')).toBe('/auth-room/desktop-torn.mp4');
    });
});
