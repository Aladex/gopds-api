import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { toast } from 'sonner';

import ForgotPassword from '@/features/auth/ForgotPassword';
import * as authApi from '@/api/auth';
import { ApiError } from '@/api/errors';

// t must keep a stable identity across renders: a mock that returns a fresh
// function every call spins any component with an effect keyed on t.
const translate = (key: string) => key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const navigate = vi.fn();
vi.mock('react-router-dom', async () => {
    const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
    return { ...actual, useNavigate: () => navigate };
});

vi.mock('@/api/auth', () => ({ requestPasswordChange: vi.fn() }));
const requestPasswordChange = vi.mocked(authApi.requestPasswordChange);

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function renderScreen() {
    return render(
        <MemoryRouter>
            <ForgotPassword />
        </MemoryRouter>,
    );
}

beforeEach(() => {
    vi.clearAllMocks();
    requestPasswordChange.mockResolvedValue(undefined);
});

describe('ForgotPassword form wiring', () => {
    it('is a real form with a real submit button', () => {
        const { container } = renderScreen();

        expect(container.querySelector('form')).not.toBeNull();
        expect(screen.getByRole('button', { name: 'resetPasswordButton' })).toHaveAttribute(
            'type',
            'submit',
        );
    });

    it('asks for an address and says so to the browser', () => {
        renderScreen();

        const field = screen.getByLabelText('email');
        expect(field).toHaveAttribute('type', 'email');
        expect(field).toHaveAttribute('autocomplete', 'email');
    });

    it('submits on Enter', async () => {
        renderScreen();

        await userEvent.type(screen.getByLabelText('email'), 'reader@example.com{Enter}');

        await waitFor(() =>
            expect(requestPasswordChange).toHaveBeenCalledWith('reader@example.com'),
        );
    });
});

describe('ForgotPassword outcomes', () => {
    it('confirms the mail is on its way before returning to sign in', async () => {
        renderScreen();

        await userEvent.type(screen.getByLabelText('email'), 'reader@example.com{Enter}');

        await waitFor(() => expect(navigate).toHaveBeenCalledWith('/login'));
        // Otherwise the screen simply vanishes and the reader has no idea
        // whether anything was sent.
        expect(toast.success).toHaveBeenCalledWith('resetLinkSent');
    });

    it.each([
        ['bad_form', 'badForm'],
        ['invalid_user', 'invalidUser'],
        ['CSRF token invalid', 'csrfTokenInvalid'],
        ['something else entirely', 'resetError'],
    ])('explains a rejection saying %s', async (error, expected) => {
        requestPasswordChange.mockRejectedValue(
            new ApiError('bad request', 400, { body: { error } }),
        );
        renderScreen();

        await userEvent.type(screen.getByLabelText('email'), 'reader@example.com{Enter}');

        expect(await screen.findByRole('alert')).toHaveTextContent(expected);
        expect(navigate).not.toHaveBeenCalled();
    });

    it('offers a way back to the sign-in screen', async () => {
        renderScreen();

        await userEvent.click(screen.getByRole('button', { name: 'BackButton' }));

        expect(navigate).toHaveBeenCalledWith('/login');
    });
});
