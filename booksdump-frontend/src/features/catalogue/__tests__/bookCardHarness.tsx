import React from 'react';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

import BookCard, { type BookCardProps } from '@/features/catalogue/BookCard';
import type { Book } from '@/api/books';

/*
 * Shared scaffolding for the book card specs. The i18n mock itself stays in
 * each spec file (vi.mock is hoisted per file); this module holds what every
 * one of them would otherwise copy: the translator, a book, a render, and the
 * fake layout jsdom does not have.
 */

/**
 * The test translator: answers with the key, with a string fallback when one
 * is given, and spells a counted phrase as `key:count` so a test can tell
 * which number reached it.
 */
export const translate = (key: string, options?: unknown): string => {
    if (typeof options === 'string') return options;
    if (options && typeof options === 'object' && 'count' in options) {
        return `${key}:${(options as { count: number }).count}`;
    }
    return key;
};

export const makeBook = (overrides: Partial<Book> = {}): Book => ({
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
    ...overrides,
});

export const renderCard = (props: Partial<BookCardProps> = {}) =>
    render(
        <MemoryRouter>
            <BookCard
                book={makeBook()}
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

/**
 * What the fake layout reports. jsdom lays nothing out, so every size is 0
 * and nothing is ever clipped; the card decides «Подробнее» and the «+N»
 * chip by measuring, and these are the measurements it gets instead.
 */
export interface FakeLayout {
    /** The collapsed annotation runs past its three lines. */
    annotationClipped?: boolean;
    /** The one-line colophon is cut. */
    colophonClipped?: boolean;
    /** How many genre chips sit on the first row; the rest wrap below it. */
    chipsOnFirstRow?: number;
}

const CHIP_WIDTH = 60;
const ROW_HEIGHT = 30;
const CONTAINER_WIDTH = 1000;

const METRICS = [
    'scrollHeight',
    'clientHeight',
    'scrollWidth',
    'clientWidth',
    'offsetTop',
    'offsetLeft',
    'offsetWidth',
] as const;

/**
 * Installs the fake layout on HTMLElement.prototype and returns the undo.
 * Keyed by the attributes the card puts on what it measures, so an element
 * the card does not measure reads as it always does under jsdom.
 */
export const fakeLayout = (layout: FakeLayout): (() => void) => {
    const saved = METRICS.map(
        (name) => [name, Object.getOwnPropertyDescriptor(HTMLElement.prototype, name)] as const,
    );
    const chipIndex = (el: HTMLElement) => Number(el.dataset.chip);
    const firstRow = layout.chipsOnFirstRow ?? Number.POSITIVE_INFINITY;
    const values: Record<(typeof METRICS)[number], (el: HTMLElement) => number> = {
        scrollHeight: (el) =>
            el.dataset.clip === 'annotation' && layout.annotationClipped ? 120 : 60,
        clientHeight: () => 60,
        scrollWidth: (el) => (el.dataset.clip === 'colophon' && layout.colophonClipped ? 900 : 300),
        clientWidth: (el) => (el.dataset.clip === 'chips' ? CONTAINER_WIDTH : 300),
        offsetTop: (el) =>
            el.dataset.chip !== undefined && chipIndex(el) >= firstRow ? ROW_HEIGHT : 0,
        offsetLeft: (el) => {
            if (el.dataset.chip === undefined) return 0;
            const index = chipIndex(el);
            return (index < firstRow ? index : index - firstRow) * CHIP_WIDTH;
        },
        offsetWidth: (el) => (el.dataset.chip !== undefined ? CHIP_WIDTH - 6 : 0),
    };
    for (const name of METRICS) {
        Object.defineProperty(HTMLElement.prototype, name, {
            configurable: true,
            get(this: HTMLElement) {
                return values[name](this);
            },
        });
    }
    return () => {
        for (const [name, descriptor] of saved) {
            if (descriptor) Object.defineProperty(HTMLElement.prototype, name, descriptor);
            else delete (HTMLElement.prototype as unknown as Record<string, unknown>)[name];
        }
    };
};
