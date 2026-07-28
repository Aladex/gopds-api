import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import EditBookDialog from '@/features/catalogue/EditBookDialog';
import * as adminApi from '@/api/admin';
import { ApiError } from '@/api/errors';
import type { Book } from '@/api/books';

// The dialog is the only way a superuser rewrites a book's metadata, and a
// mistake here is silent: a field dropped from the payload does not fail, it
// just quietly reverts on the server. So what is asserted is the request — which
// fields go, in what shape — plus the two things a reader would notice going
// wrong: a save that fails and closes anyway, and a validation error that lets
// an empty title through.

vi.mock('@/api/admin', () => ({
    saveBook: vi.fn(),
    uploadBookCover: vi.fn(),
    searchAuthors: vi.fn(),
    searchSeries: vi.fn(),
}));

// A stable t: a fresh function each render would loop the debounce effects.
const translate = (key: string, options?: unknown) => {
    if (typeof options === 'string') return options;
    if (options && typeof options === 'object') {
        const values = Object.values(options as Record<string, unknown>);
        if (values.length > 0) return `${key} ${values.join(' ')}`;
    }
    return key;
};
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const saveBook = vi.mocked(adminApi.saveBook);
const uploadBookCover = vi.mocked(adminApi.uploadBookCover);
const searchAuthors = vi.mocked(adminApi.searchAuthors);
const searchSeries = vi.mocked(adminApi.searchSeries);

function makeBook(over: Partial<Book> = {}): Book {
    return {
        id: 42,
        title: 'Дозор',
        authors: [{ id: 7, full_name: 'Лукьяненко Сергей' }],
        series: [],
        genres: [],
        annotation: 'Городское фэнтези.',
        filename: 'dozor',
        cover: true,
        registerdate: '2026-07-07T20:18:00Z',
        docdate: '1998',
        lang: 'ru',
        fav: false,
        approved: true,
        path: 'fb2-1-2.zip',
        format: 'fb2',
        favorite_count: 0,
        ...over,
    };
}

function setup(over: Partial<Book> = {}) {
    const onClose = vi.fn();
    const onBookUpdated = vi.fn();
    const book = makeBook(over);
    const view = render(
        <EditBookDialog open book={book} onClose={onClose} onBookUpdated={onBookUpdated} />,
    );
    return { ...view, book, onClose, onBookUpdated };
}

const payloadOf = () => saveBook.mock.calls[0][1] as unknown as Book;

beforeEach(() => {
    saveBook.mockReset();
    uploadBookCover.mockReset();
    searchAuthors.mockReset();
    searchSeries.mockReset();
    saveBook.mockResolvedValue({ result: makeBook({ title: 'Ночной Дозор' }) });
    uploadBookCover.mockResolvedValue({ result: makeBook() });
    searchAuthors.mockResolvedValue({ authors: [] });
    searchSeries.mockResolvedValue({ series: [] });
});

describe('EditBookDialog', () => {
    it('sends every edited field, trimmed, and hands the saved book back', async () => {
        const user = userEvent.setup();
        const { onBookUpdated, onClose } = setup();

        await user.clear(screen.getByLabelText('title'));
        await user.type(screen.getByLabelText('title'), '  Ночной Дозор  ');
        await user.clear(screen.getByLabelText('language'));
        await user.type(screen.getByLabelText('language'), ' en ');
        await user.clear(screen.getByLabelText('publicationDate'));
        await user.type(screen.getByLabelText('publicationDate'), ' 1999 ');

        await user.click(screen.getByRole('button', { name: 'save' }));

        await waitFor(() => expect(saveBook).toHaveBeenCalledTimes(1));
        expect(saveBook.mock.calls[0][0]).toBe(42);
        expect(payloadOf()).toMatchObject({
            id: 42,
            title: 'Ночной Дозор',
            lang: 'en',
            docdate: '1999',
            annotation: 'Городское фэнтези.',
            authors: [{ id: 7, full_name: 'Лукьяненко Сергей' }],
            series: [],
        });

        // The endpoint answers either bare or wrapped in { result }.
        expect(onBookUpdated).toHaveBeenCalledWith(
            expect.objectContaining({ title: 'Ночной Дозор' }),
        );
        await waitFor(() => expect(onClose).toHaveBeenCalled(), { timeout: 3000 });
    });

    it('unwraps a book the endpoint answered with bare', async () => {
        const user = userEvent.setup();
        saveBook.mockResolvedValue(makeBook({ title: 'Голое тело' }));
        const { onBookUpdated } = setup();

        await user.click(screen.getByRole('button', { name: 'save' }));

        await waitFor(() =>
            expect(onBookUpdated).toHaveBeenCalledWith(
                expect.objectContaining({ title: 'Голое тело' }),
            ),
        );
    });

    it('refuses to save a book with no title', async () => {
        const user = userEvent.setup();
        const { onClose } = setup();

        await user.clear(screen.getByLabelText('title'));
        await user.click(screen.getByRole('button', { name: 'save' }));

        expect(saveBook).not.toHaveBeenCalled();
        expect(screen.getByText('titleRequired')).toBeInTheDocument();
        expect(screen.getByLabelText('title')).toHaveAttribute('aria-invalid', 'true');
        expect(onClose).not.toHaveBeenCalled();
    });

    it('keeps the dialog open and shows the backend reason when a save fails', async () => {
        const user = userEvent.setup();
        vi.spyOn(console, 'error').mockImplementation(() => {});
        saveBook.mockRejectedValue(
            new ApiError('conflict', 409, { body: { detail: 'Такая книга уже есть' } }),
        );
        const { onClose, onBookUpdated } = setup();

        await user.click(screen.getByRole('button', { name: 'save' }));

        expect(await screen.findByText('Такая книга уже есть')).toBeInTheDocument();
        expect(onBookUpdated).not.toHaveBeenCalled();
        expect(onClose).not.toHaveBeenCalled();
        // Still editable, so the moderator can fix and try again.
        expect(screen.getByRole('button', { name: 'save' })).toBeEnabled();
    });

    it('falls back to a generic message when the failure carries no detail', async () => {
        const user = userEvent.setup();
        vi.spyOn(console, 'error').mockImplementation(() => {});
        saveBook.mockRejectedValue(new Error('boom'));
        setup();

        await user.click(screen.getByRole('button', { name: 'save' }));

        expect(await screen.findByText('errorUpdatingBook')).toBeInTheDocument();
    });

    it('asks for authors only once there are two characters to ask about', async () => {
        const user = userEvent.setup();
        setup();

        await user.type(screen.getByRole('combobox', { name: 'authors' }), 'Л');
        await new Promise((resolve) => setTimeout(resolve, 500));

        expect(searchAuthors).not.toHaveBeenCalled();
    });

    it('collapses a burst of typing into one author lookup', async () => {
        const user = userEvent.setup();
        setup();

        await user.type(screen.getByRole('combobox', { name: 'authors' }), 'Пелевин');

        await waitFor(() => expect(searchAuthors).toHaveBeenCalledWith('Пелевин'));
        await new Promise((resolve) => setTimeout(resolve, 400));
        expect(searchAuthors).toHaveBeenCalledTimes(1);
    });

    it('adds a picked author and sends it with the book', async () => {
        const user = userEvent.setup();
        searchAuthors.mockResolvedValue({ authors: [{ id: 9, full_name: 'Пелевин Виктор' }] });
        setup();

        await user.type(screen.getByRole('combobox', { name: 'authors' }), 'Пелевин');
        await user.click(await screen.findByRole('option', { name: 'Пелевин Виктор' }));

        await user.click(screen.getByRole('button', { name: 'save' }));
        await waitFor(() => expect(saveBook).toHaveBeenCalled());
        expect(payloadOf().authors).toEqual([
            { id: 7, full_name: 'Лукьяненко Сергей' },
            { id: 9, full_name: 'Пелевин Виктор' },
        ]);
    });

    it('creates an author out of free text, as an id-less one', async () => {
        const user = userEvent.setup();
        setup();

        await user.type(
            screen.getByRole('combobox', { name: 'authors' }),
            'Некто Безымянный{Enter}',
        );

        await user.click(screen.getByRole('button', { name: 'save' }));
        await waitFor(() => expect(saveBook).toHaveBeenCalled());
        // id 0 is how the backend is told to create the author.
        expect(payloadOf().authors).toEqual([
            { id: 7, full_name: 'Лукьяненко Сергей' },
            { id: 0, full_name: 'Некто Безымянный' },
        ]);
    });

    it('never offers an author the book already has', async () => {
        const user = userEvent.setup();
        searchAuthors.mockResolvedValue({
            authors: [
                { id: 7, full_name: 'Лукьяненко Сергей' },
                { id: 9, full_name: 'Пелевин Виктор' },
            ],
        });
        setup();

        await user.type(screen.getByRole('combobox', { name: 'authors' }), 'Пелевин');

        expect(await screen.findByRole('option', { name: 'Пелевин Виктор' })).toBeInTheDocument();
        expect(screen.queryByRole('option', { name: 'Лукьяненко Сергей' })).not.toBeInTheDocument();
    });

    it('drops an author whose chip is dismissed', async () => {
        const user = userEvent.setup();
        setup();

        await user.click(screen.getByRole('button', { name: 'removeItem Лукьяненко Сергей' }));

        await user.click(screen.getByRole('button', { name: 'save' }));
        await waitFor(() => expect(saveBook).toHaveBeenCalled());
        expect(payloadOf().authors).toEqual([]);
    });

    it('sends a series number as a number, not the typed text', async () => {
        const user = userEvent.setup();
        setup({ series: [{ id: 5, ser: 'Дозоры', ser_no: 0 }] });

        await user.type(screen.getByLabelText('Дозоры seriesNumber'), '3');

        await user.click(screen.getByRole('button', { name: 'save' }));
        await waitFor(() => expect(saveBook).toHaveBeenCalled());
        expect(payloadOf().series).toEqual([{ id: 5, ser: 'Дозоры', ser_no: 3 }]);
    });

    it('posts the chosen cover as multipart and reports what came back', async () => {
        const user = userEvent.setup();
        const { onBookUpdated } = setup();
        const file = new File(['jpeg'], 'cover.jpg', { type: 'image/jpeg' });

        await user.upload(screen.getByLabelText('chooseCover'), file);
        expect(screen.getByText('cover.jpg')).toBeInTheDocument();

        await user.click(screen.getByRole('button', { name: 'uploadCover' }));

        await waitFor(() => expect(uploadBookCover).toHaveBeenCalledTimes(1));
        expect(uploadBookCover.mock.calls[0][0]).toBe(42);
        const form = uploadBookCover.mock.calls[0][1];
        expect(form.get('cover')).toBe(file);
        expect(onBookUpdated).toHaveBeenCalled();
        expect(await screen.findByText('coverUploadSuccess')).toBeInTheDocument();
    });

    it('shows why a cover was rejected', async () => {
        const user = userEvent.setup();
        uploadBookCover.mockRejectedValue(
            new ApiError('too big', 413, { body: { detail: 'Файл слишком большой' } }),
        );
        setup();

        await user.upload(
            screen.getByLabelText('chooseCover'),
            new File(['jpeg'], 'cover.jpg', { type: 'image/jpeg' }),
        );
        await user.click(screen.getByRole('button', { name: 'uploadCover' }));

        expect(await screen.findByText('Файл слишком большой')).toBeInTheDocument();
    });

    it('closes on cancel without writing anything', async () => {
        const user = userEvent.setup();
        const { onClose } = setup();

        await user.click(screen.getByRole('button', { name: 'cancel' }));

        expect(onClose).toHaveBeenCalledTimes(1);
        expect(saveBook).not.toHaveBeenCalled();
    });
});
