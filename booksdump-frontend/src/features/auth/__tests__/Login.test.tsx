import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import Login from '@/features/auth/Login';
import * as authApi from '@/api/auth';
import { ApiError } from '@/api/errors';

// t must keep a stable identity across renders: a mock that returns a fresh
// function every call spins any component with an effect keyed on t.
const translate = (key: string) => key;
// i18n as well as t: the verse behind this screen picks its language
// from it, so a stub with only a translator leaves it undefined.
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

// The corner language toggle reaches for the account context and is not what
// these screens are under test for; it has tests of its own.
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));

// The router is real — the card renders a Link — but where the screen sends the
// reader is the assertion, so only useNavigate is stood in for.
const navigate = vi.fn();
vi.mock('react-router', async () => {
    const actual = await vi.importActual<typeof import('react-router')>('react-router');
    return { ...actual, useNavigate: () => navigate };
});

const auth = { setUser: vi.fn(), isAuthenticated: false, isLoaded: true };
vi.mock('@/context/AuthContext', () => ({ useAuth: () => auth }));

vi.mock('@/api/auth', () => ({ login: vi.fn() }));
const login = vi.mocked(authApi.login);

const account = {
    username: 'reader',
    first_name: 'Ada',
    last_name: 'Lovelace',
    is_superuser: false,
    books_lang: 'ru',
    have_favs: true,
};

function renderLogin() {
    return render(
        <MemoryRouter>
            <Login />
        </MemoryRouter>,
    );
}

beforeEach(() => {
    vi.clearAllMocks();
    auth.isAuthenticated = false;
    auth.isLoaded = true;
    login.mockResolvedValue(account);
});

describe('Login form wiring', () => {
    it('names its fields the way a password manager expects', () => {
        renderLogin();

        // Without these two tokens the browser has no idea which box holds the
        // account and which the secret, so it offers to fill neither.
        expect(screen.getByLabelText('username')).toHaveAttribute('autocomplete', 'username');
        expect(screen.getByLabelText('password')).toHaveAttribute(
            'autocomplete',
            'current-password',
        );
    });

    it('is a real form with a real submit button', () => {
        const { container } = renderLogin();

        expect(container.querySelector('form')).not.toBeNull();
        expect(screen.getByRole('button', { name: 'loginButton' })).toHaveAttribute(
            'type',
            'submit',
        );
    });

    it('submits on Enter from the username field, not only from the password', async () => {
        renderLogin();

        await userEvent.type(screen.getByLabelText('username'), 'reader');
        await userEvent.type(screen.getByLabelText('password'), 'secret');
        await userEvent.type(screen.getByLabelText('username'), '{Enter}');

        await waitFor(() =>
            expect(login).toHaveBeenCalledWith({
                username: 'reader',
                password: 'secret',
            }),
        );
    });

    it('keeps the username field controlled', async () => {
        renderLogin();

        await userEvent.type(screen.getByLabelText('username'), 'reader');

        expect(screen.getByLabelText('username')).toHaveValue('reader');
    });
});

describe('Login validation', () => {
    it('asks for both fields instead of sending an empty request', async () => {
        renderLogin();

        await userEvent.click(screen.getByRole('button', { name: 'loginButton' }));

        expect(login).not.toHaveBeenCalled();
        expect(await screen.findByRole('alert')).toHaveTextContent('usernameAndPasswordRequired');
    });
});

describe('Login outcomes', () => {
    it('takes the signed-in reader to the book list', async () => {
        renderLogin();

        await userEvent.type(screen.getByLabelText('username'), 'reader');
        await userEvent.type(screen.getByLabelText('password'), 'secret{Enter}');

        await waitFor(() =>
            expect(auth.setUser).toHaveBeenCalledWith({
                username: 'reader',
                first_name: 'Ada',
                last_name: 'Lovelace',
                is_superuser: false,
                books_lang: 'ru',
                have_favs: true,
            }),
        );
        expect(navigate).toHaveBeenCalledWith('/books/page/1');
    });

    it.each([
        ['bad_credentials', 'badCredentials'],
        ['user not active', 'userNotActive'],
        ['bad password', 'badPassword'],
        ['something else entirely', 'loginOrPasswordIncorrect'],
    ])('explains a 403 saying %s', async (message, expected) => {
        login.mockRejectedValue(new ApiError('forbidden', 403, { body: { message } }));
        renderLogin();

        await userEvent.type(screen.getByLabelText('username'), 'reader');
        await userEvent.type(screen.getByLabelText('password'), 'secret{Enter}');

        // The message is announced, not just drawn in red: a reader who cannot
        // see the colour still has to learn the attempt failed.
        expect(await screen.findByRole('alert')).toHaveTextContent(expected);
        expect(navigate).not.toHaveBeenCalled();
    });
});

describe('Login for someone already signed in', () => {
    it('redirects rather than offering the form again', () => {
        auth.isAuthenticated = true;
        const { container } = renderLogin();

        expect(navigate).toHaveBeenCalledWith('/books/page/1');
        expect(container.querySelector('form')).toBeNull();
    });

    it('shows nothing until the session is known', () => {
        auth.isLoaded = false;
        const { container } = renderLogin();

        expect(container.querySelector('form')).toBeNull();
        expect(navigate).not.toHaveBeenCalled();
    });
});
