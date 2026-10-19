import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import BookCard, { type BookCardProps } from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';

// The card's two send buttons: each shown or hidden by the reader's switch,
// the Telegram one a locked promise with a hint that has to reach a mouse, a
// keyboard and a finger alike, and the Kindle one either sending or — muted —
// asking for the setup.

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
const telegramButton = () => screen.queryByRole('button', { name: 'sendToTelegram.soon' });

describe('which send buttons the card shows', () => {
    it.each([
        [true, true],
        [true, false],
        [false, true],
        [false, false],
    ])('kindle %s, telegram %s', (showKindle, showTelegram) => {
        renderCard({ showKindleButton: showKindle, showTelegramButton: showTelegram });

        expect(Boolean(kindleButton())).toBe(showKindle);
        expect(Boolean(telegramButton())).toBe(showTelegram);
    });

    it('puts them between Read and the star', () => {
        renderCard({ showKindleButton: true, showTelegramButton: true });
        const names = screen
            .getAllByRole('button')
            .map((button) => button.getAttribute('aria-label') ?? button.textContent);
        const read = names.indexOf('bookRead');
        const kindle = names.indexOf('sendToKindle.setup');
        const telegram = names.indexOf('sendToTelegram.soon');
        const star = names.indexOf('bookFavAdd');
        expect([read, kindle, telegram, star].every((at) => at >= 0)).toBe(true);
        expect(read < kindle && kindle < telegram && telegram < star).toBe(true);
    });
});

describe('the Telegram button', () => {
    it('is locked, and says why on hover, on focus and on a tap', async () => {
        const user = userEvent.setup();
        renderCard({ showTelegramButton: true });
        const button = telegramButton()!;

        expect(button).toHaveAttribute('aria-disabled', 'true');
        // Locked but not disabled: a disabled button could never show its hint.
        expect(button).not.toBeDisabled();

        await user.tab();
        while (document.activeElement !== button) await user.tab();
        expect(await screen.findByRole('tooltip')).toHaveTextContent('sendToTelegram.soon');

        await user.click(document.body);
        await user.click(button);
        expect(await screen.findByRole('tooltip')).toHaveTextContent('sendToTelegram.soon');
    });
});

describe('the Kindle button', () => {
    it('looks muted and still answers when there is no Kindle yet', async () => {
        const onSendToKindle = vi.fn();
        renderCard({ showKindleButton: true, kindleConfigured: false, onSendToKindle });
        const button = kindleButton()!;

        expect(button).toHaveAccessibleName('sendToKindle.setup');
        expect(button.className).toMatch(/text-muted-foreground/);
        expect(button).not.toBeDisabled();

        await userEvent.click(button);
        expect(onSendToKindle).toHaveBeenCalledWith(book);
    });

    it('asks to send the book when there is a Kindle to send to', async () => {
        const onSendToKindle = vi.fn();
        renderCard({ showKindleButton: true, kindleConfigured: true, onSendToKindle });
        const button = kindleButton()!;

        expect(button).toHaveAccessibleName('sendToKindle.send');
        expect(button.className).not.toMatch(/text-muted-foreground/);
        await userEvent.click(button);
        expect(onSendToKindle).toHaveBeenCalledWith(book);
    });

    it('says «Kindle» beside its icon, as Read does, while Telegram stays an icon', () => {
        renderCard({ showKindleButton: true, showTelegramButton: true, kindleConfigured: true });
        const button = kindleButton()!;
        const read = screen.getByRole('button', { name: 'bookRead' });

        expect(button).toHaveTextContent('sendToKindle.button');
        // The visible word is in the name, for voice control.
        expect(button).toHaveAccessibleName('sendToKindle.send');
        expect(button.querySelector('svg')).toBeInTheDocument();
        // Read's size and spacing, so the two read as a pair.
        for (const cls of ['h-8', 'gap-1.5']) {
            expect(button.className.split(/\s+/)).toContain(cls);
            expect(read.className.split(/\s+/)).toContain(cls);
        }
        expect(telegramButton()).toHaveTextContent('');
    });

    it('keeps the word, muted, when there is no Kindle yet', () => {
        renderCard({ showKindleButton: true, kindleConfigured: false });

        expect(kindleButton()).toHaveTextContent('sendToKindle.button');
        expect(kindleButton()!.className).toMatch(/text-muted-foreground/);
    });

    it('shows that the book is on its way', () => {
        renderCard({ showKindleButton: true, kindleConfigured: true, isSendingToKindle: true });
        const button = kindleButton()!;

        expect(button).toHaveAccessibleName('sendToKindle.sending');
        expect(button).toHaveAttribute('aria-busy', 'true');
        expect(button.querySelector('svg')?.getAttribute('class')).toMatch(/animate-spin/);
    });
});
