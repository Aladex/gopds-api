import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

import CenteredBox from '@/features/auth/CenteredBox';
import { AuthRoomProvider } from '@/features/auth/scene/AuthRoom';

// t must keep a stable identity across renders: a mock that returns a fresh
// function every call spins any component with an effect keyed on t.
const translate = (key: string) => key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

// Stood in for rather than mocked away: this box is where the toggle lives, so
// the test that it is here belongs here, while what it does belongs to its own.
vi.mock('@/shared/layout/InterfaceLanguageToggle', () => ({
    default: () => <div data-testid="interface-language-toggle" />,
}));

// The scene needs a GPU. The frame no longer builds it — the room host above
// the routes does, and its own tests cover that — but the frame is what asks
// for it, and for which book.
let torn: boolean | undefined;
vi.mock('@/features/auth/scene/AuthScene', () => ({
    default: ({ torn: t }: { torn?: boolean }) => {
        torn = t;
        return <div data-testid="auth-scene" />;
    },
}));

vi.mock('sonner', () => ({ toast: vi.fn() }));

beforeEach(() => {
    torn = undefined;
    vi.clearAllMocks();
});

function renderBox(children: React.ReactNode = <p>content</p>) {
    return render(
        <MemoryRouter>
            <AuthRoomProvider>
                <CenteredBox>{children}</CenteredBox>
            </AuthRoomProvider>
        </MemoryRouter>,
    );
}

/** The frame itself, not the room drawn beside it. */
const frame = (container: HTMLElement) => container.querySelector('.auth-room') as HTMLElement;

describe('CenteredBox', () => {
    it('renders whatever screen it wraps', () => {
        renderBox(<p>content</p>);

        expect(screen.getByText('content')).toBeInTheDocument();
    });

    it('centres its card and stops it growing with the viewport', () => {
        const { container } = renderBox();

        // Pixels are the browser's business; what the wrapper owes the screens
        // is a centred card that stops widening at some point.
        const wrapper = frame(container);
        expect(wrapper.className).toContain('items-center');
        expect(wrapper.className).toContain('justify-center');

        const card = container.querySelector('[data-slot="card"]') as HTMLElement;
        expect(card).not.toBeNull();
        expect(card.className).toMatch(/max-w-/);
    });

    it('offers the interface language before anyone has signed in', () => {
        renderBox();

        // The header only exists behind authentication, so without this there
        // is no way to change the interface language on the way in.
        expect(screen.getByTestId('interface-language-toggle')).toBeInTheDocument();
    });

    it('puts the reading room behind every screen in the frame', () => {
        renderBox();

        expect(screen.getByTestId('auth-scene')).toBeInTheDocument();
    });

    it('keeps the book whole behind the sign-in screens: only the error page tears it', () => {
        renderBox();

        expect(torn).toBe(false);
    });

    it('is dark whatever the theme: the room is at night', () => {
        const { container } = renderBox();

        expect(frame(container)).toHaveAttribute('data-theme', 'dark');
    });

    it('lets the pointer through to the room, except over the card and the toggle', () => {
        const { container } = renderBox();

        // The canvas lies under the frame now, not inside it: without this the
        // frame would swallow every hover and click meant for the golden post.
        expect(frame(container).className).toContain('pointer-events-none');
        expect(container.querySelector('[data-slot="card"]')!.className).toContain(
            'pointer-events-auto',
        );
    });

    it('no longer pins a picture to the corner', () => {
        const { container } = renderBox();

        expect(container.querySelector('img')).toBeNull();
    });
});
