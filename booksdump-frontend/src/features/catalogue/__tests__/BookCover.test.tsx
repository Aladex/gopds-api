import { fireEvent, render, screen } from '@testing-library/react';

import BookCover, { type BookCoverProps } from '@/features/catalogue/BookCover';

/*
 * A cover that failed once is replaced by the typographic tile — for that
 * picture. An edit or a rescan hands the card a new poster address (or a book
 * that has a cover at last), and the new picture has to be asked for: a
 * failure or a loaded state left over from the old address would keep the
 * tile up, or show the new image before it arrived.
 */

const props = (overrides: Partial<BookCoverProps> = {}): BookCoverProps => ({
    src: '/books-posters/lib/old.jpg',
    alt: 'Пикник на обочине',
    hasPicture: true,
    title: 'Пикник на обочине',
    author: 'Аркадий Стругацкий',
    ...overrides,
});

const picture = () => screen.queryByRole('img', { name: 'Пикник на обочине' });
const tile = () => screen.queryByTestId('book-cover-tile');

describe('the cover after its picture changes', () => {
    it('asks for a new address after the old one failed', () => {
        const view = render(<BookCover {...props()} />);
        fireEvent.error(picture()!);
        expect(picture()).toBeNull();
        expect(tile()).not.toBeNull();

        view.rerender(<BookCover {...props({ src: '/books-posters/lib/new.jpg' })} />);

        expect(tile()).toBeNull();
        expect(picture()).toHaveAttribute('src', '/books-posters/lib/new.jpg');
    });

    it('asks for the picture once a book without one gets one', () => {
        const view = render(<BookCover {...props({ hasPicture: false })} />);
        expect(picture()).toBeNull();

        view.rerender(<BookCover {...props({ hasPicture: true })} />);

        expect(picture()).not.toBeNull();
    });

    it('waits for the new picture rather than showing it as loaded', () => {
        const view = render(<BookCover {...props()} />);
        fireEvent.load(picture()!);
        expect(picture()!.className).toMatch(/\bopacity-100\b/);

        view.rerender(<BookCover {...props({ src: '/books-posters/lib/new.jpg' })} />);

        // jsdom never finishes loading an image, so the new one is still on
        // its way: hidden behind the dots, not shown as the old one was.
        expect(picture()!.className).toMatch(/\bopacity-0\b/);
    });
});
