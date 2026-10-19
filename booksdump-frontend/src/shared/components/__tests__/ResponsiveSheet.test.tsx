import React from 'react';
import { render, screen, within } from '@testing-library/react';

import KindleSendConfirm from '@/features/kindle/KindleSendConfirm';
import KindleSetupDialog from '@/features/kindle/KindleSetupDialog';
import type { Book } from '@/api/books';

// The shared dialog-or-sheet. jsdom lays nothing out, so this holds the
// structure that keeps the actions on screen: the title above a body that can
// shrink and scroll, the actions below it, the whole capped. The geometry it
// produces — a collection with twenty authors at 320×640, 360×640, 390×844
// and on a desktop — was measured in Chromium (report-post5.md, post6).

const translation = { t: (key: string) => key, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({
    useTranslation: () => translation,
    Trans: ({ i18nKey }: { i18nKey: string }) => i18nKey,
}));

const matches = { current: false };
vi.mock('@/shared/hooks/useMediaQuery', () => ({
    useMediaQuery: () => matches.current,
}));

beforeAll(() => {
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
    Element.prototype.hasPointerCapture ??= () => false;
});

// The reviewer's fixture: an ordinary collection and its twenty authors.
const AUTHORS = [
    'Александр Сергеевич Пушкин',
    'Михаил Юрьевич Лермонтов',
    'Николай Васильевич Гоголь',
    'Иван Сергеевич Тургенев',
    'Фёдор Михайлович Достоевский',
    'Лев Николаевич Толстой',
    'Николай Семёнович Лесков',
    'Антон Павлович Чехов',
    'Михаил Афанасьевич Булгаков',
    'Александр Иванович Куприн',
    'Иван Алексеевич Бунин',
    'Леонид Николаевич Андреев',
    'Константин Георгиевич Паустовский',
    'Александр Степанович Грин',
    'Михаил Михайлович Пришвин',
    'Владимир Галактионович Короленко',
    'Александр Николаевич Островский',
    'Владимир Владимирович Набоков',
    'Василий Макарович Шукшин',
    'Андрей Платонович Платонов',
];
const collection: Book = {
    id: 3,
    title: 'Русская проза. Большой сборник произведений русских писателей девятнадцатого и двадцатого веков',
    authors: [{ id: 1, full_name: AUTHORS[0] }],
    authors_display: AUTHORS.map((name) => ({ name })),
    series: [],
    genres: [],
    annotation: '',
    filename: 'prose',
    cover: false,
    registerdate: '',
    docdate: '',
    lang: 'ru',
    fav: false,
    approved: true,
    path: 'lib/prose',
    format: 'fb2',
    favorite_count: 0,
};

const surfaces = {
    confirm: () =>
        render(
            <KindleSendConfirm
                open
                book={collection}
                address="reader@kindle.com"
                onConfirm={vi.fn()}
                onCancel={vi.fn()}
            />,
        ),
    setup: () => render(<KindleSetupDialog open onClose={vi.fn()} onOpenSettings={vi.fn()} />),
};
const ids = { confirm: 'kindle-confirm', setup: 'kindle-setup' };
const actions = {
    confirm: ['sendToKindle.confirm', 'cancel'],
    setup: ['kindleSetup.openSettings', 'close'],
};

const tokens = (el: Element) => el.className.split(/\s+/);

describe.each([
    ['a phone', true, 'drawer-content', /data-\[vaul-drawer-direction=bottom\]:max-h-\[92vh\]/],
    ['a desktop', false, 'dialog-content', /(^|\s)max-h-\[85vh\]/],
] as const)('on %s', (_, mobile, slot, cap) => {
    beforeEach(() => {
        matches.current = mobile;
    });

    it.each(['confirm', 'setup'] as const)(
        'keeps the %s actions out of the scrolling body',
        (name) => {
            surfaces[name]();
            const shell = screen.getByTestId(ids[name]);
            const body = screen.getByTestId(`${ids[name]}-scroll`);
            const footer = screen.getByTestId(`${ids[name]}-footer`);

            expect(shell).toHaveAttribute('data-slot', slot);
            // Capped, and laid out as a column that hands the body what is left.
            expect(shell.className).toMatch(cap);
            expect(tokens(shell)).toEqual(
                expect.arrayContaining(mobile ? [] : ['flex', 'flex-col']),
            );
            expect(tokens(shell)).not.toContain('overflow-y-auto');
            // The body may shrink below its content and scrolls instead.
            expect(tokens(body)).toEqual(expect.arrayContaining(['min-h-0', 'overflow-y-auto']));
            // Title above it, actions below it, neither inside it.
            const title = within(shell).getByRole('heading');
            expect(body).not.toContainElement(title);
            expect(body).not.toContainElement(footer);
            expect(
                title.compareDocumentPosition(body) & Node.DOCUMENT_POSITION_FOLLOWING,
            ).toBeTruthy();
            expect(
                body.compareDocumentPosition(footer) & Node.DOCUMENT_POSITION_FOLLOWING,
            ).toBeTruthy();
            for (const label of actions[name]) {
                expect(within(footer).getByRole('button', { name: label })).toBeInTheDocument();
            }
        },
    );

    it('scrolls a long collection’s title and every author, not the buttons', () => {
        surfaces.confirm();
        const body = screen.getByTestId('kindle-confirm-scroll');

        expect(body).toHaveTextContent(collection.title);
        for (const author of AUTHORS) expect(body).toHaveTextContent(author);
        expect(within(body).getByTestId('kindle-confirm-address')).toBeInTheDocument();
        expect(within(body).queryByRole('button', { name: 'sendToKindle.confirm' })).toBeNull();
    });
});
