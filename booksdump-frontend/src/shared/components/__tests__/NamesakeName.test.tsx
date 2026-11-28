import React from 'react';
import { render, screen } from '@testing-library/react';

import NamesakeName from '@/shared/components/NamesakeName';

// Every place that names an author entity writes it one way: the short name,
// then a muted «· label» for a namesake.

const translate = (key: string, options?: { count: number }) =>
    options ? `${key}:${options.count}` : key;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translate }) }));

const igor = { full_name: 'Михаил Павлович Игнатов', short_name: 'Михаил Игнатов' };

describe('NamesakeName', () => {
    it('writes the short name, then the genre label, muted', () => {
        const { container } = render(<NamesakeName author={{ ...igor, label: 'Фантастика' }} />);

        expect(container).toHaveTextContent(/^Михаил Игнатов · Фантастика$/);
        expect(screen.getByTestId('namesake-label')).toHaveClass('text-muted-foreground');
    });

    it('writes the book count where no genre tells the namesakes apart', () => {
        const { container } = render(<NamesakeName author={{ ...igor, label_books: 12 }} />);

        expect(container).toHaveTextContent(/^Михаил Игнатов · bookCount:12$/);
    });

    it('leaves the count out where the row says it already', () => {
        const { container } = render(
            <NamesakeName author={{ ...igor, label_books: 12 }} countAsLabel={false} />,
        );

        expect(container).toHaveTextContent(/^Михаил Игнатов$/);
    });

    it('writes no label for an entity without a namesake, and the full name without a short one', () => {
        const { container } = render(<NamesakeName author={{ full_name: 'Стивен Кинг' }} />);

        expect(container).toHaveTextContent(/^Стивен Кинг$/);
        expect(screen.queryByTestId('namesake-label')).toBeNull();
    });

    it('draws the caller’s markup for the name', () => {
        const { container } = render(
            <NamesakeName author={{ ...igor, label: 'Фантастика' }}>
                <mark>Михаил</mark> Игнатов
            </NamesakeName>,
        );

        expect(container.querySelector('mark')).toHaveTextContent('Михаил');
        expect(container).toHaveTextContent(/^Михаил Игнатов · Фантастика$/);
    });
});
