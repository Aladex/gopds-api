import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import Registration from '@/features/auth/Registration';
import * as authApi from '@/api/auth';
import { ApiError } from '@/api/errors';

// t must keep a stable identity across renders: a mock that returns a fresh
// function every call spins any component with an effect keyed on t.
const translate = (key: string) => key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

// The corner language toggle reaches for the account context and is not what
// these screens are under test for; it has tests of its own.
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));

const navigate = vi.fn();
vi.mock('react-router', async () => {
    const actual = await vi.importActual<typeof import('react-router')>('react-router');
    return { ...actual, useNavigate: () => navigate };
});

vi.mock('@/api/auth', () => ({ register: vi.fn() }));
const register = vi.mocked(authApi.register);

function renderRegistration() {
    return render(
        <MemoryRouter>
            <Registration />
        </MemoryRouter>,
    );
}

async function fillForm() {
    await userEvent.type(screen.getByLabelText('usernameRegistration'), 'reader');
    await userEvent.type(screen.getByLabelText('email'), 'reader@example.com');
    await userEvent.type(screen.getByLabelText('password'), 'secret');
    await userEvent.type(screen.getByLabelText('inviteCode'), 'invite');
}

beforeEach(() => {
    vi.clearAllMocks();
    register.mockResolvedValue(undefined);
});

describe('Registration form wiring', () => {
    it('is a real form with a real submit button', () => {
        const { container } = renderRegistration();

        expect(container.querySelector('form')).not.toBeNull();
        expect(screen.getByRole('button', { name: 'registerButton' })).toHaveAttribute(
            'type',
            'submit',
        );
    });

    it('tells the browser this password is a new one', () => {
        renderRegistration();

        // new-password is what makes a manager offer to generate and then save
        // a password rather than trying to fill an existing one in.
        expect(screen.getByLabelText('usernameRegistration')).toHaveAttribute(
            'autocomplete',
            'username',
        );
        expect(screen.getByLabelText('email')).toHaveAttribute('autocomplete', 'email');
        expect(screen.getByLabelText('password')).toHaveAttribute('autocomplete', 'new-password');
    });

    it('submits on Enter once every field is filled', async () => {
        renderRegistration();
        await fillForm();

        await userEvent.type(screen.getByLabelText('usernameRegistration'), '{Enter}');

        await waitFor(() =>
            expect(register).toHaveBeenCalledWith({
                username: 'reader',
                email: 'reader@example.com',
                password: 'secret',
                invite: 'invite',
            }),
        );
    });

    it('will not submit a half-filled form', async () => {
        renderRegistration();
        await userEvent.type(screen.getByLabelText('usernameRegistration'), 'reader');

        expect(screen.getByRole('button', { name: 'registerButton' })).toBeDisabled();
    });
});

describe('Registration outcomes', () => {
    it('replaces the form with the check-your-mail notice', async () => {
        const { container } = renderRegistration();
        await fillForm();

        await userEvent.click(screen.getByRole('button', { name: 'registerButton' }));

        expect(await screen.findByText('registrationSuccess')).toBeInTheDocument();
        expect(container.querySelector('form')).toBeNull();
    });

    it.each([
        ['bad_invite', 'badInvite'],
        ['bad_form', 'badForm'],
        ['user_exists', 'userExists'],
        ['CSRF token invalid', 'csrfTokenInvalid'],
        ['something else entirely', 'registrationError'],
    ])('explains a rejection saying %s', async (error, expected) => {
        register.mockRejectedValue(new ApiError('bad request', 400, { body: { error } }));
        renderRegistration();
        await fillForm();

        await userEvent.click(screen.getByRole('button', { name: 'registerButton' }));

        expect(await screen.findByRole('alert')).toHaveTextContent(expected);
    });

    it('reads the reason out of message when the backend sends it there', async () => {
        register.mockRejectedValue(
            new ApiError('bad request', 400, { body: { message: 'user_exists' } }),
        );
        renderRegistration();
        await fillForm();

        await userEvent.click(screen.getByRole('button', { name: 'registerButton' }));

        expect(await screen.findByRole('alert')).toHaveTextContent('userExists');
    });

    it('offers a way back to the sign-in screen', async () => {
        renderRegistration();

        await userEvent.click(screen.getByRole('button', { name: 'BackButton' }));

        expect(navigate).toHaveBeenCalledWith('/login');
    });
});
