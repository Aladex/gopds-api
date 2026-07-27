import React from 'react';
import { render, renderHook, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useNavigate } from 'react-router-dom';

import useSearchOptions from '@/features/catalogue/hooks/useSearchOptions';

// The panel offers a third search mode — an author's own books — only while the
// reader is inside that author's list, and it selects and deselects that mode as
// they cross in and out. Getting the crossing wrong either strands the reader on
// a mode the list no longer offers, or silently resets a mode they chose.

const translate = (key: string) => key;
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

function renderAt(path: string, setSelectedSearch = vi.fn()) {
    const wrapper = ({ children }: { children: React.ReactNode }) => (
        <MemoryRouter initialEntries={[path]}>{children}</MemoryRouter>
    );
    const view = renderHook(() => useSearchOptions(setSelectedSearch), { wrapper });
    return { ...view, setSelectedSearch };
}

const values = (result: { current: Array<{ value: string }> }) =>
    result.current.map((option) => option.value);

describe('useSearchOptions', () => {
    it('offers title and author everywhere', () => {
        const { result } = renderAt('/books/page/1');

        expect(values(result)).toEqual(['title', 'author']);
    });

    it("adds the author's own books inside an author's list", () => {
        const { result } = renderAt('/books/find/author/42/1');

        expect(values(result)).toEqual(['title', 'author', 'authorsBookSearch']);
    });

    it("selects that mode on arriving in an author's list", () => {
        const { setSelectedSearch } = renderAt('/books/find/author/42/1');

        expect(setSelectedSearch).toHaveBeenCalledWith('authorsBookSearch');
    });

    it('leaves the chosen mode alone on an ordinary page', () => {
        // A reader who picked "by author" and paged through results must not
        // have it reset under them on every mount.
        const { setSelectedSearch } = renderAt('/books/page/1');

        expect(setSelectedSearch).not.toHaveBeenCalled();
    });

    it("falls back to title on leaving an author's list", async () => {
        const user = userEvent.setup();
        const setSelectedSearch = vi.fn();
        let options: Array<{ value: string }> = [];

        const Probe: React.FC = () => {
            options = useSearchOptions(setSelectedSearch);
            const navigate = useNavigate();
            return (
                <button type="button" onClick={() => navigate('/books/page/1')}>
                    away
                </button>
            );
        };

        render(
            <MemoryRouter initialEntries={['/books/find/author/42/1']}>
                <Probe />
            </MemoryRouter>,
        );
        expect(options.map((o) => o.value)).toContain('authorsBookSearch');
        setSelectedSearch.mockClear();

        await user.click(screen.getByRole('button', { name: 'away' }));

        expect(options.map((o) => o.value)).not.toContain('authorsBookSearch');
        expect(setSelectedSearch).toHaveBeenCalledWith('title');
    });

    it('does not offer the mode on the authors index', () => {
        const { result } = renderAt('/authors/Пришвин/1');

        expect(values(result)).toEqual(['title', 'author']);
    });
});
