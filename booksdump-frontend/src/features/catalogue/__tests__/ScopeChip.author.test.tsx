import React from 'react';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

import ListLine from '@/features/catalogue/ListLine';
import ScopeChip from '@/features/catalogue/ScopeChip';
import type { SearchScope } from '@/features/catalogue/hooks/useSearchScope';

// The author scope names its author as every entity is named — the short name
// and a namesake's label — and the list line beside it adds the full name
// once, smaller, where the short name dropped a patronymic.

const translate = (key: string, options?: { count: number }) =>
    options ? `${key}:${options.count}` : key;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translate }) }));
vi.mock('@/shared/layout/BookLanguage', () => ({ default: () => null }));

const phone = { current: false };
vi.mock('@/shared/hooks/useMediaQuery', () => ({ useMediaQuery: () => phone.current }));

const scope: SearchScope = {
    kind: 'author',
    id: '01a168d0-0000-7000-8000-000000000001',
    active: true,
    firstPagePath: '/author/01a168d0-0000-7000-8000-000000000001',
    label: 'searchWithinAuthor',
    author: {
        full_name: 'Михаил Павлович Игнатов',
        short_name: 'Михаил Игнатов',
        label: 'Фантастика',
    },
    release: vi.fn(),
    reclaim: vi.fn(),
};
const context = { current: { scope, releaseScope: vi.fn() } };
vi.mock('@/features/catalogue/CatalogueSearchContext', () => ({
    useCatalogueSearchContext: () => context.current,
}));

const renderAt = (node: React.ReactNode) =>
    render(<MemoryRouter initialEntries={[scope.firstPagePath]}>{node}</MemoryRouter>);

beforeEach(() => {
    phone.current = false;
    context.current = { scope, releaseScope: vi.fn() };
});

describe('the author scope', () => {
    it('names the author by the short name and the label, on and released', () => {
        const { container, rerender } = renderAt(<ScopeChip />);
        expect(container).toHaveTextContent(/^searchWithinAuthorLead Михаил Игнатов · Фантастика$/);

        context.current = { scope: { ...scope, active: false }, releaseScope: vi.fn() };
        rerender(
            <MemoryRouter>
                <ScopeChip />
            </MemoryRouter>,
        );
        expect(container).toHaveTextContent(/^searchWithinAuthorLead Михаил Игнатов · Фантастика$/);
    });

    it('says the plain label until the name is known', () => {
        context.current = {
            scope: { ...scope, author: null, label: 'searchWithinThisAuthor' },
            releaseScope: vi.fn(),
        };
        const { container } = renderAt(<ScopeChip />);

        expect(container).toHaveTextContent(/^searchWithinThisAuthor$/);
    });

    it('adds the full name beside the chip, once, where it differs', () => {
        renderAt(<ListLine />);

        expect(screen.getByTestId('scope-full-name')).toHaveTextContent(
            /^Михаил Павлович Игнатов$/,
        );
        expect(screen.getAllByText(/Михаил Павлович Игнатов/)).toHaveLength(1);
    });

    it('adds nothing where the short name is the full name', () => {
        context.current = {
            scope: { ...scope, author: { full_name: 'Стивен Кинг', short_name: 'Стивен Кинг' } },
            releaseScope: vi.fn(),
        };
        renderAt(<ListLine />);

        expect(screen.queryByTestId('scope-full-name')).toBeNull();
    });
});
