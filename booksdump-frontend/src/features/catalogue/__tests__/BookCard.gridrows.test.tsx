import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

import BookCard from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';

/*
 * The wide card gives its cover column two grid rows (~206px: the cover plus
 * the format buttons), and when the text column beside it is shorter, the
 * grid's own auto rows share the extra height — the title row balloons and an
 * empty band opens between the dates and the metadata.
 *
 * jsdom lays nothing out, so no test here can see that band. What is pinned is
 * the class contract the fix rests on: on `sm+` the first row keeps its
 * content height (`auto`) and the second takes whatever is spare (`1fr`), so
 * the spare height lands below the metadata block instead of inside the title
 * row. The measured half — that the band is really gone at both widths — was
 * verified in a browser against the smoke server (see the task D report).
 */

const translate = (key: string, fallback?: string) => fallback ?? key;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: translate }) }));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName: vi.fn() }) }));

// Dates are counted from now: a literal date drifts as the calendar moves.
const day = 24 * 60 * 60 * 1000;
const registeredOn = new Date(Date.now() - 30 * day).toISOString().slice(0, 10);
const publishedOn = new Date(Date.now() - 365 * day).toISOString().slice(0, 10);

// The card that shows the bug: nothing under the dates but one author line
// and the "no annotation" note, so the text column is far shorter than the
// cover column beside it.
const book: Book = {
    id: 1,
    title: 'Уик-энд с Остерманом',
    authors: [{ id: 1, full_name: 'Роберт Силверберг' }],
    series: [],
    genres: [{ id: 11, genre: 'Научная фантастика' }],
    annotation: '',
    filename: 'weekend',
    cover: false,
    registerdate: registeredOn,
    docdate: publishedOn,
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'lib/weekend',
    format: 'fb2',
    favorite_count: 0,
};

const renderCard = () =>
    render(
        <MemoryRouter>
            <BookCard
                book={book}
                isWide={true}
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
            />
        </MemoryRouter>,
    );

/** The two-column grid the card's blocks fall into; the article's first child. */
const gridOf = (container: HTMLElement) =>
    container.querySelector<HTMLElement>('article[data-testid="book-card"] > div');

describe('the card grid rows on a wide screen', () => {
    it('keeps the title row at its content height and drops spare height below the meta block', () => {
        const { container } = renderCard();
        // The whole contract in one token: row 1 auto, row 2 1fr — the cover
        // column spans both, so the spare height the span forces lands in the
        // second row, under the metadata, not spread over the title row.
        expect(gridOf(container)).toHaveClass('sm:grid-rows-[auto_1fr]');
    });

    it('leaves the mobile rows alone', () => {
        const { container } = renderCard();
        // No unprefixed row template: below sm the grid keeps its own auto
        // rows, where the cover does not span and nothing stretches.
        expect(gridOf(container)?.className).not.toMatch(/(?:^|\s)grid-rows-/);
    });
});
