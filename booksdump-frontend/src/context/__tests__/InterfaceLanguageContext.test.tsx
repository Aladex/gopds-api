import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
    InterfaceLanguageProvider,
    resetChosenThisSession,
    useInterfaceLanguage,
} from '@/context/InterfaceLanguageContext';
import type { User } from '@/api/auth';
import * as authApi from '@/api/auth';

const changeLanguage = vi.fn();
vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: (key: string) => key, i18n: { changeLanguage } }),
}));

const auth: { user: User | null; updateUser: (u: User) => void } = {
    user: null,
    updateUser: vi.fn(),
};
vi.mock('@/context/AuthContext', () => ({ useAuth: () => auth }));

vi.mock('@/api/auth', () => ({ updateCurrentUser: vi.fn() }));
const updateCurrentUser = vi.mocked(authApi.updateCurrentUser);

const account = (overrides: Partial<User> = {}): User => ({
    username: 'reader',
    first_name: '',
    last_name: '',
    is_superuser: false,
    ...overrides,
});

const Probe = () => {
    const { language, setLanguage } = useInterfaceLanguage();
    return (
        <>
            <span data-testid="current">{language}</span>
            <button type="button" onClick={() => setLanguage('ru')}>
                pick ru
            </button>
        </>
    );
};

function renderProbe() {
    return render(
        <InterfaceLanguageProvider>
            <Probe />
        </InterfaceLanguageProvider>,
    );
}

describe('InterfaceLanguageProvider', () => {
    beforeEach(() => {
        window.localStorage.clear();
        resetChosenThisSession();
        auth.user = null;
        vi.clearAllMocks();
        updateCurrentUser.mockResolvedValue(account());
        vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-GB');
    });

    it('falls back to the browser when nothing else is known', () => {
        renderProbe();
        expect(screen.getByTestId('current')).toHaveTextContent('en');
    });

    it('prefers what this browser remembers over the browser locale', () => {
        window.localStorage.setItem('interfaceLang', 'ru');
        renderProbe();
        expect(screen.getByTestId('current')).toHaveTextContent('ru');
    });

    it('adopts the account when the reader has not chosen this session', async () => {
        window.localStorage.setItem('interfaceLang', 'ru');
        auth.user = account({ interface_lang: 'en' });
        renderProbe();
        await waitFor(() => expect(screen.getByTestId('current')).toHaveTextContent('en'));
        expect(window.localStorage.getItem('interfaceLang')).toBe('en');
    });

    // The complaint this whole change exists for: picking a language by hand
    // and having it silently undone a moment later.
    it('keeps a choice made by hand and sends it to the account', async () => {
        auth.user = null;
        const { rerender } = renderProbe();

        await userEvent.click(screen.getByRole('button', { name: 'pick ru' }));
        expect(screen.getByTestId('current')).toHaveTextContent('ru');

        auth.user = account({ interface_lang: 'en' });
        rerender(
            <InterfaceLanguageProvider>
                <Probe />
            </InterfaceLanguageProvider>,
        );

        await waitFor(() =>
            expect(updateCurrentUser).toHaveBeenCalledWith({ interface_lang: 'ru' }),
        );
        expect(screen.getByTestId('current')).toHaveTextContent('ru');
    });

    // Nobody's interface should move on the day this ships.
    it('backfills an account that has no interface language yet', async () => {
        auth.user = account({ books_lang: 'ru' });
        renderProbe();
        await waitFor(() => expect(screen.getByTestId('current')).toHaveTextContent('ru'));
        expect(updateCurrentUser).toHaveBeenCalledWith({ interface_lang: 'ru' });
    });

    it('backfills a reader of Ukrainian books to English, as before', async () => {
        auth.user = account({ books_lang: 'uk' });
        renderProbe();
        await waitFor(() =>
            expect(updateCurrentUser).toHaveBeenCalledWith({ interface_lang: 'en' }),
        );
    });

    // The original complaint, stated directly: a reader with a Russian
    // interface goes looking for Ukrainian books and keeps their interface.
    it('leaves the interface alone when the books language changes', async () => {
        auth.user = account({ interface_lang: 'ru', books_lang: 'ru' });
        const { rerender } = renderProbe();
        await waitFor(() => expect(screen.getByTestId('current')).toHaveTextContent('ru'));

        auth.user = account({ interface_lang: 'ru', books_lang: 'uk' });
        rerender(
            <InterfaceLanguageProvider>
                <Probe />
            </InterfaceLanguageProvider>,
        );

        expect(screen.getByTestId('current')).toHaveTextContent('ru');
    });

    it('drives i18n and is the only thing that does', async () => {
        renderProbe();
        await userEvent.click(screen.getByRole('button', { name: 'pick ru' }));
        await waitFor(() => expect(changeLanguage).toHaveBeenCalledWith('ru'));
    });
});
