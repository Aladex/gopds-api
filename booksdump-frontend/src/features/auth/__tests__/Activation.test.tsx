import { act, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

import Activation from '@/features/auth/Activation';
import * as authApi from '@/api/auth';
import { ApiError } from '@/api/errors';

// Activation has no form: it is what the link in the registration mail lands
// on. Its whole behaviour is three states — working, activated, refused — and
// where each one sends the reader.

// t must keep a stable identity across renders: a mock that returns a fresh
// function every call spins any component with an effect keyed on t.
const translate = (key: string) => key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const navigate = vi.fn();
vi.mock('react-router-dom', async () => {
    const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
    return { ...actual, useNavigate: () => navigate, useParams: () => ({ token: 'mail-token' }) };
});

vi.mock('@/api/auth', () => ({ changePassword: vi.fn() }));
const changePassword = vi.mocked(authApi.changePassword);

function renderScreen() {
    return render(
        <MemoryRouter>
            <Activation />
        </MemoryRouter>,
    );
}

beforeEach(() => {
    vi.clearAllMocks();
    changePassword.mockResolvedValue(undefined);
});

afterEach(() => {
    vi.useRealTimers();
});

describe('Activation', () => {
    it('confirms the token from the link', () => {
        // An empty password is how this backend spells "confirm the token and
        // change nothing else".
        renderScreen();

        expect(changePassword).toHaveBeenCalledWith({ token: 'mail-token', password: '' });
    });

    it('says it is working before it knows the answer', () => {
        changePassword.mockReturnValue(new Promise(() => {}));
        renderScreen();

        expect(screen.getByText('activating')).toBeInTheDocument();
        // Claiming success while the request is still out is a lie often enough
        // to matter: the token may well be spent.
        expect(screen.queryByText('activationSuccess')).not.toBeInTheDocument();
    });

    it('announces the account is live and counts down to the sign-in screen', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        renderScreen();

        expect(await screen.findByText('activationSuccess')).toBeInTheDocument();
        expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '0');

        await act(async () => {
            await vi.advanceTimersByTimeAsync(5000);
        });

        expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '100');
        expect(navigate).toHaveBeenCalledWith('/login');
    });

    it('sends a spent or unknown token to the not-found page', async () => {
        changePassword.mockRejectedValue(new ApiError('gone', 404));
        renderScreen();

        await waitFor(() => expect(navigate).toHaveBeenCalledWith('/404'));
    });

    it('reports a failure that is not a dead token instead of blaming the link', async () => {
        changePassword.mockRejectedValue(new ApiError('offline', 0));
        renderScreen();

        expect(await screen.findByRole('alert')).toHaveTextContent('activationError');
        expect(navigate).not.toHaveBeenCalled();
    });
});
