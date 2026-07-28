import React from 'react';
import { render, screen, act, waitFor } from '@testing-library/react';

import { ThemeProvider, useTheme } from '@/context/ThemeContext';
import * as booksApi from '@/api/books';
import { useAuth } from '@/context/AuthContext';

// The theme is the one thing a design-system change can silently break: where
// the initial mode comes from, when the preference is read from and written to
// the server, and how the rest of the application is told which one is on.
//
// The palettes themselves are two blocks of CSS keyed on data-theme, so what
// this provider owes everyone else is that attribute — not a set of colours.

vi.mock('@/api/books', () => ({
    getThemePreference: vi.fn(),
    setThemePreference: vi.fn(),
}));

vi.mock('@/context/AuthContext', () => ({
    useAuth: vi.fn(),
}));

const mockedGet = vi.mocked(booksApi.getThemePreference);
const mockedPost = vi.mocked(booksApi.setThemePreference);
const mockedUseAuth = vi.mocked(useAuth);

/** setSystemPrefersDark stubs the media query jsdom does not implement. */
function setSystemPrefersDark(prefersDark: boolean) {
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
        matches: query.includes('prefers-color-scheme: dark') ? prefersDark : false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
    })) as unknown as typeof window.matchMedia;
}

function setAuth(state: { isLoaded: boolean; isAuthenticated: boolean }) {
    mockedUseAuth.mockReturnValue(state as ReturnType<typeof useAuth>);
}

let toggle: () => void;

const Probe: React.FC = () => {
    const { mode, toggleTheme } = useTheme();
    // Captured in an effect rather than during render, which must stay pure.
    React.useEffect(() => {
        toggle = toggleTheme;
    }, [toggleTheme]);
    return <span data-testid="mode">{mode}</span>;
};

function renderTheme() {
    return render(
        <ThemeProvider>
            <Probe />
        </ThemeProvider>,
    );
}

beforeEach(() => {
    vi.clearAllMocks();
    document.documentElement.removeAttribute('style');
    setSystemPrefersDark(false);
    setAuth({ isLoaded: true, isAuthenticated: false });
    mockedGet.mockResolvedValue({});
    mockedPost.mockResolvedValue(undefined);
});

describe('ThemeContext', () => {
    it('starts from the system colour scheme', async () => {
        setSystemPrefersDark(true);
        renderTheme();

        expect(await screen.findByTestId('mode')).toHaveTextContent('dark');
    });

    it('falls back to light when the system prefers light', async () => {
        setSystemPrefersDark(false);
        renderTheme();

        expect(await screen.findByTestId('mode')).toHaveTextContent('light');
    });

    it('applies the preference stored on the server for a signed-in user', async () => {
        setAuth({ isLoaded: true, isAuthenticated: true });
        mockedGet.mockResolvedValue({ theme: 'dark' });

        renderTheme();

        await waitFor(() => expect(mockedGet).toHaveBeenCalled());
        await waitFor(() => expect(screen.getByTestId('mode')).toHaveTextContent('dark'));
    });

    it('does not ask the server for a preference when signed out', async () => {
        setAuth({ isLoaded: true, isAuthenticated: false });

        renderTheme();

        await waitFor(() => expect(screen.getByTestId('mode')).toBeInTheDocument());
        expect(mockedGet).not.toHaveBeenCalled();
    });

    it('keeps the current mode when the server has no preference stored', async () => {
        setSystemPrefersDark(true);
        setAuth({ isLoaded: true, isAuthenticated: true });
        mockedGet.mockResolvedValue({});

        renderTheme();

        await waitFor(() => expect(mockedGet).toHaveBeenCalled());
        expect(screen.getByTestId('mode')).toHaveTextContent('dark');
    });

    it('persists a toggle for a signed-in user', async () => {
        setAuth({ isLoaded: true, isAuthenticated: true });
        renderTheme();
        await waitFor(() => expect(mockedGet).toHaveBeenCalled());

        act(() => toggle());

        await waitFor(() => expect(mockedPost).toHaveBeenCalledWith('dark'));
        expect(screen.getByTestId('mode')).toHaveTextContent('dark');
    });

    it('toggles without contacting the server when signed out', async () => {
        setAuth({ isLoaded: true, isAuthenticated: false });
        renderTheme();

        act(() => toggle());

        await waitFor(() => expect(screen.getByTestId('mode')).toHaveTextContent('dark'));
        expect(mockedPost).not.toHaveBeenCalled();
    });

    it('marks the root with the mode, which is what Tailwind reads', async () => {
        // index.css binds the dark: variant to this attribute. Without it the
        // variant follows the operating system instead, and every shadcn
        // component styled for dark would disagree with the chosen theme.
        renderTheme();
        await screen.findByTestId('mode');

        expect(document.documentElement.getAttribute('data-theme')).toBe('light');

        act(() => toggle());
        await waitFor(() => expect(screen.getByTestId('mode')).toHaveTextContent('dark'));

        expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
    });

    it('tells the browser which way to paint what it still owns', async () => {
        // Scrollbars, native controls and the overscroll area are the
        // browser's, and colour-scheme is the only way to ask for the dark
        // ones. Getting this wrong leaves white scrollbars on a black page.
        renderTheme();
        await screen.findByTestId('mode');

        expect(document.documentElement.style.colorScheme).toBe('light');

        act(() => toggle());
        await waitFor(() => expect(screen.getByTestId('mode')).toHaveTextContent('dark'));

        expect(document.documentElement.style.colorScheme).toBe('dark');
    });
});
