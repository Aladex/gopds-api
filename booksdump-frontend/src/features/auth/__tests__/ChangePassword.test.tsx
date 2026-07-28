import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { toast } from 'sonner';

import ChangePassword from '@/features/auth/ChangePassword';
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
vi.mock('react-router-dom', async () => {
    const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
    return { ...actual, useNavigate: () => navigate, useParams: () => ({ token: 'reset-token' }) };
});

vi.mock('@/api/auth', () => ({ activate: vi.fn(), changePassword: vi.fn() }));
const activate = vi.mocked(authApi.activate);
const changePassword = vi.mocked(authApi.changePassword);

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function renderScreen() {
    return render(
        <MemoryRouter>
            <ChangePassword />
        </MemoryRouter>,
    );
}

beforeEach(() => {
    vi.clearAllMocks();
    activate.mockResolvedValue(undefined);
    changePassword.mockResolvedValue(undefined);
});

describe('ChangePassword token check', () => {
    it('checks the emailed token before showing anything to type into', () => {
        const { container } = renderScreen();

        expect(screen.getByText('validatingToken')).toBeInTheDocument();
        expect(container.querySelector('form')).toBeNull();
        expect(activate).toHaveBeenCalledWith('reset-token');
    });

    it('sends a spent or unknown token to the not-found page', async () => {
        activate.mockRejectedValue(new ApiError('gone', 404));
        renderScreen();

        await waitFor(() => expect(navigate).toHaveBeenCalledWith('/404'));
    });
});

describe('ChangePassword form wiring', () => {
    it('is a real form asking for a new password', async () => {
        const { container } = renderScreen();

        const field = await screen.findByLabelText('newPassword');
        expect(container.querySelector('form')).not.toBeNull();
        expect(field).toHaveAttribute('type', 'password');
        // This screen never sees the old password — the emailed token stands in
        // for it — so new-password is the only token that applies.
        expect(field).toHaveAttribute('autocomplete', 'new-password');
        expect(screen.getByRole('button', { name: 'changePasswordButton' })).toHaveAttribute(
            'type',
            'submit',
        );
    });

    it('submits on Enter', async () => {
        renderScreen();

        await userEvent.type(await screen.findByLabelText('newPassword'), 'fresh{Enter}');

        await waitFor(() =>
            expect(changePassword).toHaveBeenCalledWith({
                token: 'reset-token',
                password: 'fresh',
            }),
        );
    });
});

describe('ChangePassword outcomes', () => {
    it('says the password changed and sends the reader back to sign in', async () => {
        renderScreen();

        await userEvent.type(await screen.findByLabelText('newPassword'), 'fresh{Enter}');

        await waitFor(() => expect(navigate).toHaveBeenCalledWith('/login'));
        // Landing back on the sign-in screen with no word said looks like the
        // attempt was thrown away.
        expect(toast.success).toHaveBeenCalledWith('passwordChanged');
    });

    it.each([
        ['bad_form', 'badForm'],
        ['invalid_user', 'invalidUser'],
        ['CSRF token invalid', 'csrfTokenInvalid'],
        ['something else entirely', 'changePasswordError'],
    ])('explains a rejection saying %s', async (error, expected) => {
        changePassword.mockRejectedValue(new ApiError('bad request', 400, { body: { error } }));
        renderScreen();

        await userEvent.type(await screen.findByLabelText('newPassword'), 'fresh{Enter}');

        expect(await screen.findByRole('alert')).toHaveTextContent(expected);
        expect(navigate).not.toHaveBeenCalled();
    });

    it('sends a token that expired mid-form to the not-found page', async () => {
        changePassword.mockRejectedValue(new ApiError('gone', 404));
        renderScreen();

        await userEvent.type(await screen.findByLabelText('newPassword'), 'fresh{Enter}');

        await waitFor(() => expect(navigate).toHaveBeenCalledWith('/404'));
    });

    it('offers a way back to the sign-in screen', async () => {
        renderScreen();
        await screen.findByLabelText('newPassword');

        await userEvent.click(screen.getByRole('button', { name: 'BackButton' }));

        expect(navigate).toHaveBeenCalledWith('/login');
    });
});
