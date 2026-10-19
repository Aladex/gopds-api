import React, { useState } from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import TelegramSendConfirm from '@/features/send/TelegramSendConfirm';
import type { Book } from '@/api/books';

// The question before a book goes to Telegram: the Kindle question's shell —
// a dialog on a desktop, a bottom sheet on a phone — with the bot and the
// format where Kindle names its address.

const translation = { t: (key: string) => key, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const matches = { current: false };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => matches.current,
}));

beforeAll(() => {
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
    Element.prototype.hasPointerCapture ??= () => false;
});

const book: Book = {
    id: 4,
    title: 'Понедельник начинается в субботу',
    authors: [{ id: 1, full_name: 'Аркадий Стругацкий' }],
    authors_display: [{ name: 'Аркадий и Борис Стругацкие' }],
    series: [],
    genres: [],
    annotation: '',
    filename: 'monday',
    cover: false,
    registerdate: '',
    docdate: '',
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'lib/monday',
    format: 'fb2',
    favorite_count: 0,
};

const onConfirm = vi.fn();

/** A card button that asks, as the catalogue's does. */
const Harness = () => {
    const [open, setOpen] = useState(false);
    return (
        <>
            <button type="button" onClick={() => setOpen(true)}>
                card
            </button>
            <TelegramSendConfirm
                open={open}
                book={book}
                botUsername="reader_library_bot"
                format="epub"
                onConfirm={() => {
                    setOpen(false);
                    onConfirm();
                }}
                onCancel={() => setOpen(false)}
            />
        </>
    );
};

const ask = async () => {
    render(<Harness />);
    const card = screen.getByRole('button', { name: 'card' });
    await userEvent.click(card);
    return { card, question: await screen.findByRole('dialog') };
};

beforeEach(() => {
    vi.clearAllMocks();
    matches.current = false;
});

describe.each([
    ['a desktop', false, 'dialog-content', ['cancel', 'sendToTelegram.confirm']],
    ['a phone', true, 'drawer-content', ['sendToTelegram.confirm', 'cancel']],
] as const)('on %s', (_, mobile, slot, order) => {
    beforeEach(() => {
        matches.current = mobile;
    });

    it('asks in the shared shell, with the book, its authors, the bot and the format', async () => {
        const { question } = await ask();

        expect(question).toHaveAttribute('data-slot', slot);
        expect(question).toHaveAccessibleName('sendToTelegram.confirmTitle');
        // The author line the card shows, not the catalogue's first author.
        expect(question).toHaveAccessibleDescription(`${book.title}Аркадий и Борис Стругацкие`);
        expect(within(question).getByTestId('telegram-confirm-bot')).toHaveTextContent(
            '@reader_library_bot',
        );
        expect(within(question).getByTestId('telegram-confirm-format')).toHaveTextContent('EPUB');
        const actions = within(question)
            .getAllByRole('button')
            .map((button) => button.textContent)
            .filter((name) => name === 'cancel' || name === 'sendToTelegram.confirm');
        expect(actions).toEqual(order);
    });

    it('sends on Enter and gives the focus back to the card', async () => {
        const { card, question } = await ask();

        await waitFor(() =>
            expect(
                within(question).getByRole('button', { name: 'sendToTelegram.confirm' }),
            ).toHaveFocus(),
        );
        await userEvent.keyboard('{Enter}');

        expect(onConfirm).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        await waitFor(() => expect(card).toHaveFocus());
    });

    it('cancels on Escape without sending, and gives the focus back', async () => {
        const { card } = await ask();

        await userEvent.keyboard('{Escape}');

        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        expect(onConfirm).not.toHaveBeenCalled();
        await waitFor(() => expect(card).toHaveFocus());
    });
});
