import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import ErrorPage from '@/shared/components/ErrorPage';
import { AuthRoomProvider } from '@/features/auth/room/AuthRoom';

// t must keep a stable identity across renders: a mock that returns a fresh
// function every call spins any component with an effect keyed on t.
const translate = (key: string) => key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({ default: () => null }));

// The scene needs a GPU; what the page owes it is the right book.
const sceneProps = vi.fn();
vi.mock('@/features/auth/room/RoomVideo', () => ({
    default: (props: { torn?: boolean }) => {
        sceneProps(props);
        return <div data-testid="auth-scene" />;
    },
}));

const navigate = vi.fn();
vi.mock('react-router', async () => {
    const actual = await vi.importActual<typeof import('react-router')>('react-router');
    return { ...actual, useNavigate: () => navigate };
});

function renderPage() {
    return render(
        <MemoryRouter>
            <AuthRoomProvider>
                <ErrorPage />
            </AuthRoomProvider>
        </MemoryRouter>,
    );
}

beforeEach(() => vi.clearAllMocks());

describe('ErrorPage', () => {
    it('says the page does not exist, as the blog would', () => {
        renderPage();

        expect(screen.getByRole('heading', { name: 'errorPage.title' })).toBeInTheDocument();
        expect(screen.getByText('errorPage.message')).toBeInTheDocument();
    });

    it('sits in the reading room, with the book open on a torn-out page', () => {
        const { container } = renderPage();

        expect(screen.getByTestId('auth-scene')).toBeInTheDocument();
        expect(sceneProps).toHaveBeenCalledWith(expect.objectContaining({ torn: true }));
        // The same frame as the sign-in screens: dark, one card.
        expect(container.querySelector('.auth-room')).toHaveAttribute('data-theme', 'dark');
        expect(container.querySelectorAll('[data-slot="card"]')).toHaveLength(1);
    });

    it('takes the reader back to the start', async () => {
        renderPage();

        await userEvent.click(screen.getByRole('button', { name: 'errorPage.button' }));

        expect(navigate).toHaveBeenCalledWith('/');
    });

    it('draws no verse behind it any more', () => {
        const { container } = renderPage();

        expect(container.querySelector('[style*="--verse-height"]')).toBeNull();
    });
});
