import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import EditBookDialog from '@/features/catalogue/EditBookDialog';
import type { Book } from '@/api/books';
import en from '@/locales/en/translation.json';
import ru from '@/locales/ru/translation.json';

// The hint under the metadata editor's Authors field, in the real words of both
// interface languages: a key missing from a locale shows here as the key.

vi.mock('@/api/admin', () => ({
    saveBook: vi.fn(),
    uploadBookCover: vi.fn(),
    searchAuthors: vi.fn().mockResolvedValue({ authors: [] }),
    searchSeries: vi.fn().mockResolvedValue({ series: [] }),
}));

const locale = vi.hoisted(() => ({ current: {} as Record<string, string> }));
const translate = (key: string, fallback?: unknown) =>
    locale.current[key] ?? (typeof fallback === 'string' ? fallback : key);
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const book = {
    id: 42,
    title: 'Дозор',
    authors: [],
    series: [],
    genres: [],
    annotation: '',
    filename: 'dozor',
    cover: false,
    registerdate: '',
    docdate: '',
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'p',
    format: 'fb2',
    favorite_count: 0,
} as Book;

describe('EditBookDialog', () => {
    it.each([
        ['ru', ru, 'Введите минимум 3 символа для поиска'],
        ['en', en, 'Type at least 3 characters to search'],
    ])('asks for more letters in %s', async (_, words, hint) => {
        locale.current = words;
        render(<EditBookDialog open book={book} onClose={vi.fn()} onBookUpdated={vi.fn()} />);

        const [authors] = screen.getAllByRole('combobox');
        await userEvent.type(authors, 'к');

        expect(await screen.findByText(hint)).toBeInTheDocument();
    });
});
