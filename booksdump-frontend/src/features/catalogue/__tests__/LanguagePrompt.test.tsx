import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import LanguagePrompt from '@/features/catalogue/LanguagePrompt';
import type { User } from '@/api/auth';

vi.mock('react-i18next', () => ({
    useTranslation: () => ({ t: (key: string) => key }),
}));

const auth: { user: User | null; updateLang: (l: string) => void } = {
    user: null,
    updateLang: vi.fn(),
};
vi.mock('@/context/AuthContext', () => ({ useAuth: () => auth }));

const searchBar: {
    languages: string[];
    languagesLoaded: boolean;
    setSelectedLanguage: (l: string) => void;
} = { languages: [], languagesLoaded: false, setSelectedLanguage: vi.fn() };
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchBar }));

const account = (overrides: Partial<User> = {}): User => ({
    username: 'reader',
    first_name: '',
    last_name: '',
    is_superuser: false,
    ...overrides,
});

describe('LanguagePrompt', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        auth.user = null;
        searchBar.languages = [];
        searchBar.languagesLoaded = false;
    });

    it('says nothing to a reader who already has a language', () => {
        auth.user = account({ books_lang: 'ru' });
        searchBar.languages = ['ru', 'en'];
        searchBar.languagesLoaded = true;
        render(<LanguagePrompt />);
        expect(screen.queryByRole('dialog')).toBeNull();
    });

    it('waits rather than guessing while the languages are still coming', () => {
        auth.user = account();
        searchBar.languagesLoaded = false;
        render(<LanguagePrompt />);
        expect(screen.queryByRole('dialog')).toBeNull();
        expect(auth.updateLang).not.toHaveBeenCalled();
    });

    it("asks which of the library's languages to show", () => {
        auth.user = account();
        searchBar.languages = ['ru', 'en'];
        searchBar.languagesLoaded = true;
        render(<LanguagePrompt />);
        expect(screen.getByRole('dialog')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /Русский/ })).toBeInTheDocument();
    });

    it('records the answer against the account', async () => {
        auth.user = account();
        searchBar.languages = ['ru', 'en'];
        searchBar.languagesLoaded = true;
        render(<LanguagePrompt />);

        await userEvent.click(screen.getByRole('button', { name: /Русский/ }));
        expect(auth.updateLang).toHaveBeenCalledWith('ru');
        expect(searchBar.setSelectedLanguage).toHaveBeenCalledWith('ru');
    });

    // A library with no books has nothing to ask about. This is the case the
    // first draft could not reach: it read an empty list as "not loaded yet".
    it('settles on English for an empty library without asking', async () => {
        auth.user = account();
        searchBar.languages = [];
        searchBar.languagesLoaded = true;
        render(<LanguagePrompt />);

        await waitFor(() => expect(auth.updateLang).toHaveBeenCalledWith('en'));
        expect(screen.queryByRole('dialog')).toBeNull();
    });
});
