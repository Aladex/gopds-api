import React from 'react';
import { render, screen, act, waitFor } from '@testing-library/react';

import { ThemeProvider, useTheme } from '../ThemeContext';
import { fetchWithAuth } from '../../api/config';
import { useAuth } from '../AuthContext';

// The theme is the one thing a design-system change can silently break, so this
// pins the behaviour before Tailwind and shadcn arrive: where the initial mode
// comes from, when the preference is read from and written to the server, and
// the CSS custom properties the rest of the application reads.

vi.mock('../../api/config', () => ({
    fetchWithAuth: { get: vi.fn(), post: vi.fn() },
    API_URL: 'http://test',
}));

vi.mock('../AuthContext', () => ({
    useAuth: vi.fn(),
}));

const mockedGet = vi.mocked(fetchWithAuth.get);
const mockedPost = vi.mocked(fetchWithAuth.post);
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
    toggle = toggleTheme;
    return <span data-testid="mode">{mode}</span>;
};

function renderTheme() {
    return render(
        <ThemeProvider>
            <Probe />
        </ThemeProvider>,
    );
}

function cssVar(name: string) {
    return document.documentElement.style.getPropertyValue(name);
}

beforeEach(() => {
    vi.clearAllMocks();
    document.documentElement.removeAttribute('style');
    setSystemPrefersDark(false);
    setAuth({ isLoaded: true, isAuthenticated: false });
    mockedGet.mockResolvedValue({ data: {} } as never);
    mockedPost.mockResolvedValue({ data: {} } as never);
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
        mockedGet.mockResolvedValue({ data: { theme: 'dark' } } as never);

        renderTheme();

        await waitFor(() => expect(mockedGet).toHaveBeenCalledWith('/books/theme'));
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
        mockedGet.mockResolvedValue({ data: {} } as never);

        renderTheme();

        await waitFor(() => expect(mockedGet).toHaveBeenCalled());
        expect(screen.getByTestId('mode')).toHaveTextContent('dark');
    });

    it('persists a toggle for a signed-in user', async () => {
        setAuth({ isLoaded: true, isAuthenticated: true });
        renderTheme();
        await waitFor(() => expect(mockedGet).toHaveBeenCalled());

        act(() => toggle());

        await waitFor(() =>
            expect(mockedPost).toHaveBeenCalledWith('/books/theme', { theme: 'dark' }),
        );
        expect(screen.getByTestId('mode')).toHaveTextContent('dark');
    });

    it('toggles without contacting the server when signed out', async () => {
        setAuth({ isLoaded: true, isAuthenticated: false });
        renderTheme();

        act(() => toggle());

        await waitFor(() => expect(screen.getByTestId('mode')).toHaveTextContent('dark'));
        expect(mockedPost).not.toHaveBeenCalled();
    });

    it('publishes the palette as CSS custom properties', async () => {
        renderTheme();
        await screen.findByTestId('mode');

        const published = [
            '--app-bg-default',
            '--app-bg-paper',
            '--app-bg-muted',
            '--app-text-primary',
            '--app-text-secondary',
            '--app-divider',
            '--app-action-hover',
            '--app-secondary-main',
            '--app-secondary-contrast',
            '--app-secondary-dark',
        ];
        for (const name of published) {
            expect(cssVar(name), `${name} should be published`).not.toBe('');
        }
    });

    it('republishes the custom properties when the mode changes', async () => {
        renderTheme();
        await screen.findByTestId('mode');

        const light = {
            background: cssVar('--app-bg-default'),
            foreground: cssVar('--app-text-primary'),
        };

        act(() => toggle());
        await waitFor(() => expect(screen.getByTestId('mode')).toHaveTextContent('dark'));

        expect(cssVar('--app-bg-default')).not.toBe(light.background);
        expect(cssVar('--app-text-primary')).not.toBe(light.foreground);
    });
});
