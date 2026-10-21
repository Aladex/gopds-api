import { useEffect, useState } from 'react';
import { act, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router';
import { toast } from 'sonner';

import CenteredBox from '@/features/auth/CenteredBox';
import { AuthRoomProvider, ROOM_GRACE_MS } from '@/features/auth/scene/AuthRoom';
import createScene, { type SceneOptions } from '@/features/auth/scene/createScene';

/*
 * One reading room for every screen that shows it. Going from the sign-in
 * screen to the forgotten password, to registration and back — or to the
 * error page — must not tear the scene down and build it again: it is the
 * same room, and rebuilding it costs every texture and page canvas.
 *
 * The screens here are the real frame (CenteredBox) under the real host; only
 * the scene itself, which needs a GPU, is a stand-in that counts.
 */

vi.mock('@/features/auth/scene/createScene', () => ({ default: vi.fn() }));
const create = vi.mocked(createScene);

const translate = (key: string) => key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));
vi.mock('sonner', () => ({ toast: vi.fn() }));

interface FakeScene {
    dispose: ReturnType<typeof vi.fn>;
    setTorn: ReturnType<typeof vi.fn>;
}
let scenes: FakeScene[] = [];

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

function renderApp(start = '/login') {
    return render(
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
}

/** Let the lazy scene chunk and the scene's promise settle. */
async function settle() {
    await act(async () => {
        for (let i = 0; i < 20; i++) await Promise.resolve();
    });
}

async function navigate(path: string) {
    act(() => nav.go(path));
    await settle();
}

const canvas = () => document.querySelector('canvas');
const live = () => scenes.filter((s) => s.dispose.mock.calls.length === 0).length;

beforeEach(() => {
    scenes = [];
    create.mockReset();
    create.mockImplementation(async () => {
        const scene = { dispose: vi.fn(), setTorn: vi.fn() };
        scenes.push(scene);
        return scene;
    });
    vi.stubGlobal('WebGL2RenderingContext', class {});
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(
        () => ({ getExtension: () => ({ loseContext() {} }) }) as never,
    );
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
});

afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

describe('AuthRoomProvider: one room for every public screen', () => {
    it('builds the scene once and keeps it, on the same canvas, across every room screen', async () => {
        renderApp('/login');
        await settle();
        const first = canvas();
        expect(first).not.toBeNull();

        for (const path of [
            '/forgot-password',
            '/registration',
            '/login',
            '/change-password/abc',
            '/activate/xyz',
            '/no/such/page',
            '/login',
        ]) {
            await navigate(path);
            expect(canvas()).toBe(first);
        }
        // Even well past the grace: a room screen is still showing.
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS * 3));

        expect(create).toHaveBeenCalledTimes(1);
        expect(scenes[0].dispose).not.toHaveBeenCalled();
        expect(screen.getByText('login')).toBeInTheDocument();
    });

    it('takes the room down once, after the grace, when the reader enters the application', async () => {
        renderApp('/login');
        await settle();

        await navigate('/books');
        // Out of sight at once: the application must not show through to it.
        expect(document.querySelector('[data-auth-room]')).toHaveAttribute('hidden');
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS - 1));
        expect(scenes[0].dispose).not.toHaveBeenCalled();

        act(() => vi.advanceTimersByTime(1));
        expect(scenes[0].dispose).toHaveBeenCalledTimes(1);
        expect(canvas()).toBeNull();

        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS * 3));
        expect(scenes[0].dispose).toHaveBeenCalledTimes(1);
    });

    it('builds a new room when the reader comes back after it was taken down', async () => {
        renderApp('/login');
        await settle();
        await navigate('/books');
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS));

        await navigate('/login');

        expect(create).toHaveBeenCalledTimes(2);
        expect(live()).toBe(1);
    });

    it('keeps the room through a quick trip out and back inside the grace, without a second build', async () => {
        renderApp('/login');
        await settle();

        for (let i = 0; i < 4; i++) {
            await navigate('/books');
            act(() => vi.advanceTimersByTime(ROOM_GRACE_MS / 2));
            await navigate(i % 2 ? '/login' : '/registration');
            expect(document.querySelector('[data-auth-room]')).not.toHaveAttribute('hidden');
        }
        // The grace restarts with every exit: an old timer must not fire late.
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS * 3));

        expect(create).toHaveBeenCalledTimes(1);
        expect(live()).toBe(1);

        await navigate('/books');
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS));
        expect(live()).toBe(0);
        expect(scenes[0].dispose).toHaveBeenCalledTimes(1);
    });

    it('tears the book on the error page and mends it elsewhere, on the live scene', async () => {
        renderApp('/login');
        await settle();
        expect((create.mock.calls[0][0] as SceneOptions).torn).toBe(false);

        await navigate('/no/such/page');
        expect(scenes[0].setTorn).toHaveBeenLastCalledWith(true);

        await navigate('/login');
        expect(scenes[0].setTorn).toHaveBeenLastCalledWith(false);
        expect(create).toHaveBeenCalledTimes(1);
    });

    it('builds the torn book straight away when the error page is where the reader lands', async () => {
        renderApp('/no/such/page');
        await settle();

        expect((create.mock.calls[0][0] as SceneOptions).torn).toBe(true);
    });

    it('sends whoever catches the golden post to registration, and says why', async () => {
        renderApp('/login');
        await settle();

        await act(async () => (create.mock.calls[0][0] as SceneOptions).onGolden());

        expect(screen.getByText('registration')).toBeInTheDocument();
        expect(toast).toHaveBeenCalledWith('goldenPost');
    });

    it('stops waiting to take the room down when the whole host goes', async () => {
        const { unmount } = renderApp('/login');
        await settle();

        unmount();

        expect(scenes[0].dispose).toHaveBeenCalledTimes(1);
        expect(vi.getTimerCount()).toBe(0);
    });
});

describe('AuthRoomProvider under React’s development double-mount', () => {
    it('still shows one room and builds one scene for the screen that wants it', async () => {
        const { StrictMode } = await import('react');
        render(
            <StrictMode>
                <MemoryRouter initialEntries={['/login']}>
                    <AuthRoomProvider>
                        <Routes>
                            <Route path="/login" element={<Screen name="login" />} />
                        </Routes>
                    </AuthRoomProvider>
                </MemoryRouter>
            </StrictMode>,
        );
        await settle();

        expect(document.querySelector('[data-auth-room]')).not.toHaveAttribute('hidden');
        expect(live()).toBe(1);
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS * 3));
        expect(live()).toBe(1);
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

        act(() => dropFirst());
        await settle();
        act(() => vi.advanceTimersByTime(ROOM_GRACE_MS * 3));

        expect(document.querySelector('[data-auth-room]')).not.toHaveAttribute('hidden');
        expect(live()).toBe(1);
        expect(scenes[0].setTorn).toHaveBeenLastCalledWith(true);
    });
});
