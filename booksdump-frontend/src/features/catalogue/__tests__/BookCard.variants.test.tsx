import React from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';

import BookCard, { type BookCardProps } from '@/features/catalogue/BookCard';
import * as booksApi from '@/api/books';
import type { Book, BookVariantRow, BookVariantsInfo } from '@/api/books';

/*
 * A card with variants gets one control: on a wide card a block at the top of
 * the rail, on a phone a chip at the end of the dates line. Picking a file
 * swaps everything that belongs to the file; the title, the authors and the
 * star belong to the card. The reader's pick is remembered in the browser.
 */

// The test translator spells its options, so a test can tell which numbers
// reached a phrase: variantPosition(n=1,count=6).
const translate = (key: string, options?: unknown): string => {
    if (typeof options === 'string') return options;
    if (options && typeof options === 'object') {
        const args = Object.entries(options as Record<string, unknown>)
            .filter(([name]) => name !== 'defaultValue')
            .map(([name, value]) => `${name}=${String(value)}`)
            .join(',');
        return args ? `${key}(${args})` : key;
    }
    return key;
};
const translation = { t: translate, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => ({ setSearchItem: vi.fn() }) }));
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => ({ setAuthorName: vi.fn() }) }));
vi.mock('@/api/books', async () => ({
    ...(await vi.importActual<typeof import('@/api/books')>('@/api/books')),
    getBookVariants: vi.fn(),
}));

const getBookVariants = vi.mocked(booksApi.getBookVariants);

beforeAll(() => {
    // vaul's sheet captures the pointer on press; jsdom has no such API.
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.scrollIntoView ??= () => {};
});

const DAY = 24 * 60 * 60 * 1000;
const isoDay = (offsetDays: number) =>
    new Date(Date.now() - offsetDays * DAY).toISOString().slice(0, 10);

const KEY = '7b0b7a3e-0000-4000-8000-000000000001';
const CARD_TITLE = 'Хоббит, или Туда и обратно';

const base: Book = {
    id: 10,
    title: CARD_TITLE,
    authors: [{ id: 1, full_name: 'Джон Толкин' }],
    series: [],
    genres: [],
    annotation: 'Аннотация десятого файла',
    filename: 'f10',
    cover: true,
    registerdate: `${isoDay(10)}T10:00:00Z`,
    docdate: isoDay(400),
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'p10.zip',
    format: 'fb2',
    favorite_count: 0,
    publisher: 'АСТ',
    isbn: [],
    translators: ['Кирилл Королёв'],
};

const variantsInfo = (over: Partial<BookVariantsInfo> = {}): BookVariantsInfo => ({
    key: KEY,
    count: 3,
    kind: 'variants',
    ...over,
});

const row = (id: number, over: Partial<BookVariantRow> = {}): BookVariantRow => ({
    ...base,
    id,
    filename: `f${id}`,
    path: `p${id}.zip`,
    annotation: `Аннотация файла ${id}`,
    registerdate: `${isoDay(id)}T10:00:00Z`,
    docdate: isoDay(400 + id),
    publisher: `Издательство ${id}`,
    translators: [`Переводчик Номер${id}`],
    lineage: `doc-${id}`,
    latest: true,
    rank: 1,
    tag: '',
    doc_version: '1.0',
    default: false,
    ...over,
});

const hobbitRows = (): BookVariantRow[] => [
    row(10, {
        rank: 1,
        default: true,
        annotation: base.annotation,
        publisher: 'АСТ',
        translators: ['Кирилл Королёв'],
    }),
    row(20, { rank: 2, title: 'Хоббит', series: [{ id: 5, ser: 'Средиземье', ser_no: 1 }] }),
    row(30, { rank: 3 }),
];

const answer = (rows: BookVariantRow[], kind = 'variants') => ({
    key: KEY,
    kind,
    count: kind === 'revisions' ? rows.length : new Set(rows.map((r) => r.lineage)).size,
    default_id: rows.find((r) => r.default)?.id ?? rows[0].id,
    variants: rows,
});

const renderCard = (props: Partial<BookCardProps> = {}) =>
    render(
        <MemoryRouter>
            <BookCard
                book={{ ...base, variants: variantsInfo() }}
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

const card = () => screen.getByTestId('book-card');
const datesLine = () => screen.getByText(/^bookAdded/).parentElement as HTMLElement;
const cover = () => within(screen.getByTestId('book-cover')).queryByRole('img');

beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    getBookVariants.mockResolvedValue(answer(hobbitRows()));
});

/** Opens the selector the way the card offers it at its size. */
const openSelector = async (isWide: boolean) => {
    if (isWide) {
        await userEvent.click(within(screen.getByTestId('variant-selector')).getByRole('button'));
        return screen.findByRole('menu');
    }
    await userEvent.click(screen.getByTestId('variant-chip'));
    return screen.findByRole('dialog');
};

/** The row of a file in an open selector, by its label's distinctive part. */
const optionFor = async (root: HTMLElement, text: RegExp) => {
    const option = (await within(root).findAllByText(text))[0];
    return option.closest('[role^="menuitem"], button') as HTMLElement;
};

describe('a single book', () => {
    it('renders no selector', () => {
        renderCard({ book: base });

        expect(screen.queryByTestId('variant-selector')).toBeNull();
        expect(screen.queryByTestId('variant-chip')).toBeNull();
        // The dates line holds the two dates and nothing else.
        expect(datesLine().children).toHaveLength(2);
        // The rail starts with «Читать онлайн», as it always did.
        const rail = screen.getByTestId('book-actions');
        expect(rail.firstElementChild).toHaveTextContent('bookReadOnline');
        expect(getBookVariants).not.toHaveBeenCalled();
    });
});

describe('the control', () => {
    it('desktop shows «Вариант 1 из 6» at the top of the rail', () => {
        renderCard({ book: { ...base, variants: variantsInfo({ count: 6 }) } });

        const rail = screen.getByTestId('book-actions');
        const selector = screen.getByTestId('variant-selector');
        expect(rail.firstElementChild).toBe(selector);
        expect(selector).toHaveTextContent('variantLabel');
        expect(selector).toHaveTextContent('variantPosition(n=1,count=6)');
        // The trigger names the shown file before anything is fetched.
        expect(within(selector).getByRole('button')).toHaveTextContent('К. Королёв');
        expect(getBookVariants).not.toHaveBeenCalled();
        expect(screen.queryByTestId('variant-chip')).toBeNull();
    });

    it('desktop says «последняя из 22» for revisions', () => {
        renderCard({ book: { ...base, variants: variantsInfo({ count: 22, kind: 'revisions' }) } });

        const selector = screen.getByTestId('variant-selector');
        expect(selector).toHaveTextContent('revisionLabel');
        expect(selector).toHaveTextContent('revisionLatestOf(count=22)');
    });

    it('phone shows the chip at the end of the dates line', () => {
        renderCard({ isWide: false, book: { ...base, variants: variantsInfo({ count: 6 }) } });

        const chip = screen.getByTestId('variant-chip');
        expect(datesLine().lastElementChild).toBe(chip);
        expect(chip).toHaveTextContent('variantCount(count=6)');
        expect(screen.queryByTestId('variant-selector')).toBeNull();
    });

    it('opens one fetch, however often it is opened', async () => {
        renderCard();
        const menu = await openSelector(true);
        await within(menu).findAllByRole('menuitemradio');
        await userEvent.keyboard('{Escape}');
        await openSelector(true);
        expect(getBookVariants).toHaveBeenCalledTimes(1);
        expect(getBookVariants).toHaveBeenCalledWith(10);
    });
});

describe('picking a row', () => {
    it.each([true, false])(
        'swaps cover, dates, colophon, annotation and the actions’ book id (wide %s)',
        async (isWide) => {
            const onDownload = vi.fn();
            const onPreview = vi.fn();
            renderCard({ isWide, onDownload, onPreview });
            expect(card()).toHaveTextContent('Аннотация десятого файла');

            const list = await openSelector(isWide);
            await userEvent.click(await optionFor(list, /П\. Номер20/));

            await waitFor(() => expect(card()).toHaveTextContent('Аннотация файла 20'));
            expect(card()).not.toHaveTextContent('Аннотация десятого файла');
            expect(datesLine()).toHaveTextContent(row(20).registerdate);
            // A phone keeps the colophon behind «Подробнее».
            if (!isWide) await userEvent.click(screen.getByRole('button', { name: 'bookMore' }));
            expect(card()).toHaveTextContent('Издательство 20');
            expect(card()).toHaveTextContent('Средиземье');
            if (isWide) expect(cover()).toHaveAttribute('src', expect.stringContaining('f20'));

            if (isWide) {
                await userEvent.click(
                    within(screen.getByTestId('book-actions')).getByRole('button', { name: /FB2/ }),
                );
            } else {
                await userEvent.click(
                    within(screen.getByTestId('book-actions')).getByRole('button', {
                        name: /bookDownload/,
                    }),
                );
                await userEvent.click(
                    within(await screen.findByRole('dialog')).getByRole('button', { name: 'FB2' }),
                );
            }
            expect(onDownload).toHaveBeenCalledWith('fb2', 20);
            await userEvent.click(
                within(screen.getByTestId('book-actions')).getAllByRole('button')[isWide ? 1 : 0],
            );
            expect(onPreview).toHaveBeenLastCalledWith(expect.objectContaining({ id: 20 }));
        },
    );

    it('keeps the title, the authors and the star', async () => {
        const onToggleFavourite = vi.fn();
        renderCard({ book: { ...base, fav: true, variants: variantsInfo() }, onToggleFavourite });

        const list = await openSelector(true);
        await userEvent.click(await optionFor(list, /П\. Номер20/));
        await waitFor(() => expect(card()).toHaveTextContent('Аннотация файла 20'));

        expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent(CARD_TITLE);
        expect(card()).toHaveTextContent('Джон Толкин');
        const star = screen.getByRole('button', { name: 'bookFav' });
        expect(star).toHaveAttribute('aria-pressed', 'true');

        await userEvent.click(star);
        expect(onToggleFavourite).toHaveBeenCalledWith(expect.objectContaining({ id: 10 }), 20);
    });

    it('says which file the phone chip shows after a swap', async () => {
        renderCard({ isWide: false });
        const sheet = await openSelector(false);
        await userEvent.click(await optionFor(sheet, /П\. Номер20/));

        await waitFor(() =>
            expect(screen.getByTestId('variant-chip')).toHaveTextContent('variantOf(n=2,count=3)'),
        );
        expect(screen.getByTestId('variant-chip')).toHaveTextContent('П. Номер20');
    });

    it('remembers the pick for the card', async () => {
        renderCard();
        const list = await openSelector(true);
        await userEvent.click(await optionFor(list, /П\. Номер30/));

        expect(localStorage.getItem(`booksdump:variant:${KEY}`)).toBe('30');
    });

    it('forgets the pick when the default is picked again', async () => {
        localStorage.setItem(`booksdump:variant:${KEY}`, '30');
        renderCard();
        await waitFor(() => expect(card()).toHaveTextContent('Аннотация файла 30'));

        const list = await openSelector(true);
        await userEvent.click(await optionFor(list, /К\. Королёв/));

        await waitFor(() => expect(card()).toHaveTextContent('Аннотация десятого файла'));
        expect(localStorage.getItem(`booksdump:variant:${KEY}`)).toBeNull();
    });
});

describe('which file opens', () => {
    it('remembered choice opens on mount', async () => {
        localStorage.setItem(`booksdump:variant:${KEY}`, '20');
        renderCard();

        await waitFor(() => expect(card()).toHaveTextContent('Аннотация файла 20'));
        expect(getBookVariants).toHaveBeenCalledWith(10);
        expect(screen.getByTestId('variant-selector')).toHaveTextContent(
            'variantPosition(n=2,count=3)',
        );
    });

    it('falls back when the remembered file is gone', async () => {
        localStorage.setItem(`booksdump:variant:${KEY}`, '99');
        renderCard();

        await waitFor(() => expect(getBookVariants).toHaveBeenCalled());
        await waitFor(() => expect(localStorage.getItem(`booksdump:variant:${KEY}`)).toBeNull());
        expect(card()).toHaveTextContent('Аннотация десятого файла');
    });

    it('falls back when the variants cannot be read', async () => {
        localStorage.setItem(`booksdump:variant:${KEY}`, '20');
        getBookVariants.mockRejectedValue(new Error('offline'));
        renderCard();

        await waitFor(() => expect(getBookVariants).toHaveBeenCalled());
        expect(card()).toHaveTextContent('Аннотация десятого файла');
    });

    it('falls back when storage throws', () => {
        const getItem = vi.spyOn(window.localStorage, 'getItem').mockImplementation(() => {
            throw new Error('SecurityError');
        });
        renderCard();
        expect(card()).toHaveTextContent('Аннотация десятого файла');
        expect(getBookVariants).not.toHaveBeenCalled();
        getItem.mockRestore();
    });

    it('reason "saved" ignores the remembered choice', async () => {
        localStorage.setItem(`booksdump:variant:${KEY}`, '20');
        renderCard({
            book: {
                ...base,
                id: 30,
                annotation: 'Сохранённый файл',
                variants: variantsInfo({ reason: 'saved' }),
            },
        });

        // The saved file's place is read, so the control can say where it is.
        await waitFor(() => expect(getBookVariants).toHaveBeenCalledWith(30));
        await waitFor(() =>
            expect(screen.getByTestId('variant-selector')).toHaveTextContent(
                'variantPosition(n=3,count=3)',
            ),
        );
        expect(card()).toHaveTextContent('Сохранённый файл');
        expect(card()).not.toHaveTextContent('Аннотация файла 20');
        expect(localStorage.getItem(`booksdump:variant:${KEY}`)).toBe('20');
    });
});

describe('the list of files', () => {
    it('heads the list with what differs and marks the default and the shown file', async () => {
        renderCard();
        const menu = await openSelector(true);

        expect(await within(menu).findByText(/variantsDiffer/)).toBeInTheDocument();
        const first = await optionFor(menu, /К\. Королёв/);
        expect(first).toHaveTextContent('variantDefault');
        expect(first).toHaveAttribute('aria-checked', 'true');
        // Variants of different documents show their covers.
        expect(first.querySelector('[data-variant-thumb]')).not.toBeNull();
        expect(await optionFor(menu, /П\. Номер20/)).toHaveAttribute('aria-checked', 'false');
        // A file with a title of its own says so.
        expect(await optionFor(menu, /П\. Номер20/)).toHaveTextContent('variantAs(title=Хоббит)');
    });

    it('labels variants by what tells documents apart, and a folded revision by its number', async () => {
        getBookVariants.mockResolvedValue(
            answer([
                ...hobbitRows(),
                row(21, {
                    lineage: 'doc-20',
                    latest: false,
                    rank: 4,
                    doc_version: '0.9',
                    translators: ['Переводчик Номер20'],
                }),
            ]),
        );
        renderCard();
        const menu = await openSelector(true);

        const head = await optionFor(menu, /П\. Номер20/);
        expect(head).not.toHaveTextContent('revisionShort');
        await userEvent.click(within(menu).getByText(/earlierRevisions\(count=1\)/));
        expect(await within(menu).findByText(/revisionShort\(v=0\.9\)/)).toBeInTheDocument();
    });

    it.each([true, false])('revisions fold under the latest (wide %s)', async (isWide) => {
        const day = isoDay(3);
        getBookVariants.mockResolvedValue(
            answer(
                [
                    row(10, {
                        lineage: 'doc',
                        doc_version: '2.1',
                        docdate: day,
                        rank: 1,
                        default: true,
                    }),
                    row(11, {
                        lineage: 'doc',
                        doc_version: '2.0',
                        docdate: day,
                        rank: 2,
                        latest: false,
                    }),
                    row(12, {
                        lineage: 'doc',
                        doc_version: '1.9',
                        docdate: day,
                        rank: 3,
                        latest: false,
                    }),
                ],
                'revisions',
            ),
        );
        renderCard({
            isWide,
            book: { ...base, variants: variantsInfo({ kind: 'revisions', count: 3 }) },
        });
        const list = await openSelector(isWide);

        await within(list).findByText(/revisionShort\(v=2\.1\)/);
        expect(within(list).queryByText(/revisionShort\(v=2\.0\)/)).toBeNull();
        const fold = within(list).getByText(/earlierRevisions\(count=2\)/);
        // Revisions of one document have no pictures to tell them apart.
        expect(list.querySelectorAll('img, [data-variant-thumb]')).toHaveLength(0);

        await userEvent.click(fold);
        expect(await within(list).findByText(/revisionShort\(v=2\.0\)/)).toBeInTheDocument();
        expect(within(list).getByText(/revisionShort\(v=1\.9\)/)).toBeInTheDocument();

        // Revisions are told apart by their number alone: the rows' differing
        // publishers say nothing here.
        expect(await optionFor(list, /revisionShort\(v=2\.1\)/)).not.toHaveTextContent(
            'Издательство',
        );
        if (isWide) {
            // Once read, the trigger names the shown file by its revision.
            expect(
                within(screen.getByTestId('variant-selector')).getByRole('button'),
            ).toHaveTextContent('revisionShort(v=2.1)');
        }

        await userEvent.click(await optionFor(list, /revisionShort\(v=1\.9\)/));
        await waitFor(() => expect(card()).toHaveTextContent('Аннотация файла 12'));
    });
});

describe('the feed mark', () => {
    it.each([
        ['new_variant', 'variantMarkNewVariant'],
        ['new_revision', 'variantMarkNewRevision'],
    ] as const)(
        'feed mark %s shows and opens on the new file, ignoring the remembered choice',
        async (reason, text) => {
            localStorage.setItem(`booksdump:variant:${KEY}`, '20');
            renderCard({
                isWide: false,
                book: {
                    ...base,
                    id: 30,
                    annotation: 'Новый файл',
                    variants: variantsInfo({ reason }),
                },
            });

            const mark = screen.getByTestId('variant-mark');
            expect(mark).toHaveTextContent(text);
            // In the dates line, before the chip.
            expect(datesLine()).toContainElement(mark);
            expect(mark.nextElementSibling).toBe(screen.getByTestId('variant-chip'));
            await waitFor(() => expect(getBookVariants).toHaveBeenCalledWith(30));
            expect(card()).toHaveTextContent('Новый файл');
            expect(card()).not.toHaveTextContent('Аннотация файла 20');
        },
    );

    it('is not shown for a saved file, nor after a pick', async () => {
        renderCard({ book: { ...base, variants: variantsInfo({ reason: 'saved' }) } });
        expect(screen.queryByTestId('variant-mark')).toBeNull();
    });

    it('goes when the reader picks another file', async () => {
        renderCard({
            book: { ...base, id: 30, variants: variantsInfo({ reason: 'new_variant' }) },
        });
        expect(screen.getByTestId('variant-mark')).toBeInTheDocument();
        const list = await openSelector(true);
        await userEvent.click(await optionFor(list, /П\. Номер20/));
        await waitFor(() => expect(screen.queryByTestId('variant-mark')).toBeNull());
    });
});
