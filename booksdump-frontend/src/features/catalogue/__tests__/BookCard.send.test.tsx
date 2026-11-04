import React from 'react';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import BookCard, { type BookCardProps } from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';

// The send buttons: each shown or hidden by the reader's switch, each either
// sending or — muted — leading to its setup. Which of them a card offers, and
// in what shape, is the send rule in BookCard.redesign; here is how each one
// behaves once it is there, in the wide card's rail and on a phone's row.

const translate = (key: string, fallback?: string) => fallback ?? key;
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName: vi.fn() }) }));

const book: Book = {
    id: 9,
    title: 'Пикник на обочине',
    authors: [{ id: 1, full_name: 'Аркадий Стругацкий' }],
    series: [],
    genres: [],
    annotation: '',
    filename: 'picnic',
    cover: false,
    registerdate: '',
    docdate: '',
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'lib/picnic',
    format: 'fb2',
    favorite_count: 0,
};

const renderCard = (props: Partial<BookCardProps> = {}) =>
    render(
        <MemoryRouter>
            <BookCard
                book={book}
                isWide
                showLanguage={false}
                isSuperuser={false}
                formatDate={(value) => value}
                isBookConverting={() => false}
                onDownload={vi.fn()}
                onPreview={vi.fn()}
                onEpubRequest={vi.fn()}
                onMobiRequest={vi.fn()}
                onToggleFavourite={vi.fn()}
                onToggleApproved={vi.fn()}
                onRescan={vi.fn()}
                onEdit={vi.fn()}
                {...props}
            />
        </MemoryRouter>,
    );

const kindleButton = () =>
    screen.queryByRole('button', { name: /sendToKindle\.(send|setup|sending)/ });
const telegramButton = () =>
    screen.queryByRole('button', { name: /sendToTelegram\.(send|setup|sending)/ });

describe('the read button', () => {
    // There are no non-FB2 books: every format is converted from FB2, so the
    // reader is offered on every card, at both sizes.
    it.each([
        [true, 'bookReadOnline'],
        [false, 'bookRead'],
    ])('opens the book (wide %s, %s)', async (isWide, name) => {
        const onPreview = vi.fn();
        renderCard({ isWide, onPreview });

        await userEvent.click(
            within(screen.getByTestId('book-actions')).getByRole('button', { name }),
        );
        expect(onPreview).toHaveBeenCalledWith(book);
    });
});

describe('the rail puts Kindle and Telegram side by side', () => {
    it('in one row of two equal cells', () => {
        renderCard({ showKindleButton: true, showTelegramButton: true });
        const block = screen.getByTestId('book-send');

        expect(block.className.split(/\s+/)).toEqual(
            expect.arrayContaining(['grid', 'grid-cols-2']),
        );
        expect(block.children).toHaveLength(2);
    });

    it('and gives a single one the whole row', () => {
        renderCard({ showKindleButton: true });
        const block = screen.getByTestId('book-send');

        expect(block.className).not.toMatch(/grid-cols-2/);
        expect(block.children).toHaveLength(1);
    });
});

/*
 * The rail says each target's state in words, not only in its name for a
 * screen reader and its tooltip: a muted «Kindle» alone did not tell a reader
 * that a press opens the setup, nor that a book was on its way.
 */
describe('the rail says the state of each target', () => {
    const rail = () => screen.getByTestId('book-send');

    describe('with one target, across the whole row', () => {
        it.each([
            [
                'a Kindle not set up',
                { showKindleButton: true, kindleConfigured: false },
                ['sendToKindle.railLabel', 'sendToKindle.setupShort'],
                [],
            ],
            [
                'a Kindle ready',
                { showKindleButton: true, kindleConfigured: true },
                ['sendToKindle.railLabel'],
                ['sendToKindle.setupShort', 'sendToKindle.sending'],
            ],
            [
                'a book on its way to the Kindle',
                { showKindleButton: true, kindleConfigured: true, isSendingToKindle: true },
                ['sendToKindle.sending'],
                ['sendToKindle.setupShort'],
            ],
            [
                'a Telegram not connected',
                { showTelegramButton: true, telegramConnected: false },
                ['sendToTelegram.railLabel', 'sendToTelegram.setupShort'],
                [],
            ],
            [
                'a book on its way to Telegram',
                { showTelegramButton: true, telegramConnected: true, isSendingToTelegram: true },
                ['sendToTelegram.sending'],
                ['sendToTelegram.setupShort'],
            ],
        ] as const)('%s', (_, props, shown, absent) => {
            renderCard(props);
            const button = within(rail()).getByRole('button');

            for (const text of shown) expect(button).toHaveTextContent(text);
            for (const text of absent) expect(button).not.toHaveTextContent(text);
        });
    });

    describe('with two targets side by side', () => {
        /** The second line under a target's name, or null when it has none. */
        const stateLine = (button: HTMLElement) =>
            button.querySelector<HTMLElement>('[data-send-state]');

        it('puts the state on a line of its own under the name', () => {
            renderCard({
                showKindleButton: true,
                showTelegramButton: true,
                kindleConfigured: false,
                telegramConnected: true,
                isSendingToTelegram: true,
            });
            const [kindle, telegram] = within(rail()).getAllByRole('button');

            expect(kindle).toHaveTextContent('sendToKindle.button');
            expect(stateLine(kindle)).toHaveTextContent(/^sendToKindle\.setupShort$/);
            expect(telegram).toHaveTextContent('sendToTelegram.button');
            expect(stateLine(telegram)).toHaveTextContent(/^sendToTelegram\.sendingShort$/);
        });

        it('says nothing more for a target that is ready', () => {
            renderCard({
                showKindleButton: true,
                showTelegramButton: true,
                kindleConfigured: true,
                telegramConnected: false,
            });
            const [kindle, telegram] = within(rail()).getAllByRole('button');

            expect(stateLine(kindle)).toBeNull();
            expect(kindle).toHaveTextContent(/^sendToKindle\.button$/);
            expect(stateLine(telegram)).toHaveTextContent(/^sendToTelegram\.setupShort$/);
        });
    });
});

describe.each([true, false])('the Telegram button (wide %s)', (isWide) => {
    it('looks muted and still answers when the bot is not connected', async () => {
        const onSendToTelegram = vi.fn();
        renderCard({
            isWide,
            showTelegramButton: true,
            telegramConnected: false,
            onSendToTelegram,
        });
        const button = telegramButton()!;

        expect(button).toHaveAccessibleName('sendToTelegram.setup');
        expect(button.className).toMatch(/text-muted-foreground/);
        expect(button).not.toBeDisabled();

        await userEvent.click(button);
        expect(onSendToTelegram).toHaveBeenCalledWith(book);
    });

    it('asks to send the book when the bot is connected', async () => {
        const onSendToTelegram = vi.fn();
        renderCard({ isWide, showTelegramButton: true, telegramConnected: true, onSendToTelegram });
        const button = telegramButton()!;

        expect(button).toHaveAccessibleName('sendToTelegram.send');
        expect(button.className).not.toMatch(/text-muted-foreground/);
        await userEvent.click(button);
        expect(onSendToTelegram).toHaveBeenCalledWith(book);
    });

    it('says what it does on a Tab onto it and on a hover', async () => {
        const user = userEvent.setup();
        renderCard({ isWide, showTelegramButton: true, telegramConnected: true });
        const button = telegramButton()!;

        while (document.activeElement !== button) await user.tab();
        expect(await screen.findByRole('tooltip')).toHaveTextContent('sendToTelegram.send');

        await user.click(document.body);
        await user.hover(button);
        expect(await screen.findByRole('tooltip')).toHaveTextContent('sendToTelegram.send');
    });

    it('shows that the book is on its way', () => {
        renderCard({
            isWide,
            showTelegramButton: true,
            telegramConnected: true,
            isSendingToTelegram: true,
        });
        const button = telegramButton()!;

        expect(button).toHaveAccessibleName('sendToTelegram.sending');
        expect(button).toHaveAttribute('aria-busy', 'true');
        expect(button.querySelector('svg')?.getAttribute('class')).toMatch(/animate-spin/);
    });
});

describe.each([true, false])('the Kindle button (wide %s)', (isWide) => {
    it('looks muted and still answers when there is no Kindle yet', async () => {
        const onSendToKindle = vi.fn();
        renderCard({ isWide, showKindleButton: true, kindleConfigured: false, onSendToKindle });
        const button = kindleButton()!;

        expect(button).toHaveAccessibleName('sendToKindle.setup');
        expect(button.className).toMatch(/text-muted-foreground/);
        expect(button).not.toBeDisabled();

        await userEvent.click(button);
        expect(onSendToKindle).toHaveBeenCalledWith(book);
    });

    it('asks to send the book when there is a Kindle to send to', async () => {
        const onSendToKindle = vi.fn();
        renderCard({ isWide, showKindleButton: true, kindleConfigured: true, onSendToKindle });
        const button = kindleButton()!;

        expect(button).toHaveAccessibleName('sendToKindle.send');
        expect(button.className).not.toMatch(/text-muted-foreground/);
        await userEvent.click(button);
        expect(onSendToKindle).toHaveBeenCalledWith(book);
    });

    // A lone target has the rail's whole row and says «В Kindle»; the
    // phone's row has a third of the width and says «Kindle».
    const word = isWide ? 'sendToKindle.railLabel' : 'sendToKindle.button';

    it('says «Kindle» beside its icon', () => {
        renderCard({ isWide, showKindleButton: true, kindleConfigured: true });
        const button = kindleButton()!;

        expect(button).toHaveTextContent(word);
        // The visible word is in the name, for voice control.
        expect(button).toHaveAccessibleName('sendToKindle.send');
        expect(button.querySelector('svg')).toBeInTheDocument();
    });

    it('keeps the word, muted, when there is no Kindle yet', () => {
        renderCard({ isWide, showKindleButton: true, kindleConfigured: false });

        expect(kindleButton()).toHaveTextContent(word);
        expect(kindleButton()!.className).toMatch(/text-muted-foreground/);
    });

    it('shows that the book is on its way', () => {
        renderCard({
            isWide,
            showKindleButton: true,
            kindleConfigured: true,
            isSendingToKindle: true,
        });
        const button = kindleButton()!;

        expect(button).toHaveAccessibleName('sendToKindle.sending');
        expect(button).toHaveAttribute('aria-busy', 'true');
        expect(button.querySelector('svg')?.getAttribute('class')).toMatch(/animate-spin/);
    });
});

describe('the «Отправить» button on a phone', () => {
    beforeAll(() => {
        Element.prototype.setPointerCapture ??= () => {};
        Element.prototype.releasePointerCapture ??= () => {};
        Element.prototype.hasPointerCapture ??= () => false;
    });

    it('shows a book on its way to either target', () => {
        renderCard({
            isWide: false,
            showKindleButton: true,
            showTelegramButton: true,
            isSendingToTelegram: true,
        });
        const button = screen.getByRole('button', { name: 'bookSend' });

        expect(button).toHaveAttribute('aria-busy', 'true');
        expect(button.querySelector('svg')?.getAttribute('class')).toMatch(/animate-spin/);
    });

    it('closes its sheet once a target is chosen', async () => {
        const onSendToTelegram = vi.fn();
        renderCard({
            isWide: false,
            showKindleButton: true,
            showTelegramButton: true,
            telegramConnected: true,
            onSendToTelegram,
        });

        await userEvent.click(screen.getByRole('button', { name: 'bookSend' }));
        const sheet = await screen.findByRole('dialog');
        await userEvent.click(within(sheet).getByRole('button', { name: 'sendToTelegram.send' }));

        expect(onSendToTelegram).toHaveBeenCalledWith(book);
        expect(screen.queryByRole('dialog')).toBeNull();
    });
});
