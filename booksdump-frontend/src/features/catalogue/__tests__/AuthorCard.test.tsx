import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

import type { AuthorCardData } from '@/api/books';
import AuthorCard, {
    AuthorCardSkeleton,
    CARD_FRAME,
    PILE_FRAME,
    TEXT_FRAME,
} from '@/features/catalogue/AuthorCard';

const translate = (key: string, options?: Record<string, unknown> | string) => {
    if (options && typeof options === 'object') {
        if ('count' in options) return `${key}:${String(options.count)}`;
        if ('name' in options) return `${key}:${String(options.name)}`;
    }
    return key;
};
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const cover = (n: number) => ({ path: `fb2-${n}.zip`, filename: `${n}.fb2`, title: `Книга ${n}` });

const NAME = 'Стивен Кинг';

const renderCard = (over: Partial<AuthorCardData> = {}, query = 'кинг', onGo = vi.fn()) => {
    const author: AuthorCardData = { id: 'e1', full_name: NAME, books_count: 12, ...over };
    const view = render(
        <MemoryRouter>
            <AuthorCard author={author} query={query} onGo={onGo} />
        </MemoryRouter>,
    );
    const images = () => [...view.container.querySelectorAll('img')];
    return { ...view, images, onGo };
};

const tile = () => screen.queryByTestId('author-tile');
/** Which of the pile's three slots each picture stands in, top first. */
const slots = (images: HTMLImageElement[]) => images.map((image) => image.dataset.slot);

describe('AuthorCard pile', () => {
    it('stacks the first three of any number of covers', () => {
        const { images } = renderCard({ covers: [1, 2, 3, 4, 5].map(cover) });

        expect(slots(images())).toEqual(['0', '1', '2']);
        expect(images()[0].getAttribute('src')).toContain('/books-thumbs/fb2-1-zip/1-fb2.jpg');
        expect(tile()).toBeNull();
    });

    it('stacks two covers as two', () => {
        const { images } = renderCard({ covers: [1, 2].map(cover) });

        expect(slots(images())).toEqual(['0', '1']);
        expect(tile()).toBeNull();
    });

    it('stands one cover alone, in the slot the text is laid out around', () => {
        const { images } = renderCard({ covers: [cover(1)] });

        expect(slots(images())).toEqual(['0']);
        expect(screen.getByTestId('author-pile')).toHaveClass(...PILE_FRAME.split(' '));
    });

    // Today's production answer has none of the summary fields at all, and the
    // next one has them empty until the backfill reaches the author.
    it.each([
        ['absent', undefined],
        ['empty', []],
        ['null', null],
    ])('draws the first letter of the surname when covers are %s', (_, covers) => {
        const { images } = renderCard({ covers: covers as AuthorCardData['covers'] });

        expect(images()).toHaveLength(0);
        expect(tile()).toHaveTextContent('К');
        expect(screen.getByTestId('author-pile')).toHaveClass(...PILE_FRAME.split(' '));
    });

    it('closes the pile over a cover that failed', () => {
        const { images } = renderCard({ covers: [1, 2, 3].map(cover) });

        fireEvent.error(images()[0]);

        expect(slots(images())).toEqual(['0', '1']);
        expect(images()[0].getAttribute('src')).toContain('2-fb2.jpg');
    });

    it('draws the letter once every cover has failed', () => {
        const { images } = renderCard({ covers: [1, 2].map(cover) });

        fireEvent.error(images()[1]);
        fireEvent.error(images()[0]);

        expect(images()).toHaveLength(0);
        expect(tile()).toHaveTextContent('К');
    });

    // The same titles are on the third line; the link should not read them twice.
    it('leaves the covers out of what the link says', () => {
        const { images } = renderCard({ covers: [cover(1)] });

        expect(images()[0]).toHaveAttribute('alt', '');
    });
});

describe('AuthorCard text', () => {
    it('renders from an answer with no summary, the count alone under the name', () => {
        const { container } = renderCard();

        expect(screen.getByText('bookCount:12')).toBeInTheDocument();
        expect(container.querySelector('[data-testid="author-works"]')).toBeNull();
    });

    it('leaves no meta line behind where there is no count', () => {
        renderCard({ books_count: undefined });

        expect(screen.queryByTestId('author-meta')).toBeNull();
    });

    it('joins two genres after the count, the second one lower-cased', () => {
        renderCard({ genres: ['Ужасы', 'Мистика', 'Триллер'] });

        expect(screen.getByTestId('author-meta')).toHaveTextContent(
            /bookCount:12 · Ужасы, мистика$/,
        );
    });

    it('keeps an acronym of a genre as it is', () => {
        renderCard({ genres: ['Ужасы', 'SF'] });

        expect(screen.getByTestId('author-meta')).toHaveTextContent('Ужасы, SF');
    });

    it('lists the works', () => {
        renderCard({ works: ['Оно', 'Сияние'] });

        expect(screen.getByTestId('author-works')).toHaveTextContent('Оно · Сияние');
    });

    it('tags a collective author', () => {
        renderCard({ kind: 'collective' });

        expect(screen.getByText('authorSearch.coauthors')).toBeInTheDocument();
    });

    it('does not tag a person', () => {
        renderCard({ kind: 'person' });

        expect(screen.queryByText('authorSearch.coauthors')).toBeNull();
    });

    it('still tells namesakes apart by their label', () => {
        renderCard({ label: 'Фантастика' });

        expect(screen.getByText(/Фантастика/)).toBeInTheDocument();
    });

    it('lets the query recede and the rest of the name carry the row', () => {
        renderCard();

        const name = screen.getByTestId('author-name');
        expect(within(name).getByText('Кинг')).toHaveAttribute('data-hit');
        expect(name).toHaveTextContent(NAME);
    });

    it('lights a one-book author less', () => {
        const { unmount } = renderCard({ books_count: 465 });
        expect(screen.getByTestId('author-name').className).toContain('font-semibold');
        unmount();

        renderCard({ books_count: 1 });
        expect(screen.getByTestId('author-name').className).toContain('font-medium');
    });
});

describe('AuthorCard note', () => {
    it('says a hit with no trace of the query is a similar name', () => {
        renderCard({ works: ['Оно'] }, 'александр');

        expect(screen.getByTestId('author-works')).toHaveTextContent(
            'authorSearch.similarName · Оно',
        );
    });

    it('names the matched name when the server has one', () => {
        renderCard({ matched_name: 'Алекс Кинг' }, 'александр');

        expect(screen.getByTestId('author-works')).toHaveTextContent(
            'authorSearch.alsoIn:Алекс Кинг',
        );
        expect(screen.queryByText('authorSearch.similarName')).toBeNull();
    });

    // The name on the card already explains itself.
    it('stays out of the way of a hit that carries the query', () => {
        renderCard({ matched_name: 'King, Stephen', works: ['Оно'] });

        expect(screen.getByTestId('author-works')).toHaveTextContent(/^Оно$/);
    });
});

describe('AuthorCard link', () => {
    it('is a real link to the author', () => {
        renderCard({ id: 'e1' });

        expect(screen.getByRole('link')).toHaveAttribute('href', '/author/e1');
    });

    it('carries the name through on a plain click', () => {
        const { onGo } = renderCard({ label: 'Ужасы' });

        fireEvent.click(screen.getByRole('link'));

        expect(onGo).toHaveBeenCalledTimes(1);
        expect(onGo.mock.calls[0][0]).toBe(`${NAME} · Ужасы`);
    });

    // The reader stays on this page, so its search box must stay as it is.
    it.each(['ctrlKey', 'metaKey', 'shiftKey'])('leaves a %s click to the browser', (modifier) => {
        const { onGo } = renderCard();
        const link = screen.getByRole('link');
        link.addEventListener('click', (event) => event.preventDefault());

        fireEvent.click(link, { [modifier]: true });

        expect(onGo).not.toHaveBeenCalled();
    });
});

describe('AuthorCardSkeleton', () => {
    it('is laid out by the same frames as the card', () => {
        const { container } = render(<AuthorCardSkeleton />);

        expect(container.firstElementChild).toHaveClass(...CARD_FRAME.split(' '));
        expect(screen.getByTestId('author-pile')).toHaveClass(...PILE_FRAME.split(' '));
        expect(screen.getByTestId('author-text')).toHaveClass(...TEXT_FRAME.split(' '));
    });

    it('is hidden from assistive technology', () => {
        const { container } = render(<AuthorCardSkeleton />);

        expect(container.firstElementChild).toHaveAttribute('aria-hidden', 'true');
    });
});
