import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import AutocompleteSearch from '@/features/catalogue/AutocompleteSearch';
import { autocompleteService } from '@/api/autocomplete';

// What matters here is not the markup but the request discipline. The suggestion
// endpoint is hit on every keystroke a reader makes, so the three-character
// minimum, the debounce and the type mapping are the difference between a search
// box and a load generator — and a mismapped type silently returns the wrong
// kind of suggestion with no error to notice. Two more rules keep the box
// honest: a newer keystroke cancels the request in flight, and a failed request
// says so instead of pretending there were no suggestions.

vi.mock('@/api/autocomplete', () => ({
    autocompleteService: { getSuggestions: vi.fn() },
}));

const getSuggestions = vi.mocked(autocompleteService.getSuggestions);

const authorState = { authorId: '' };
vi.mock('@/context/AuthorContext', () => ({ useAuthor: () => authorState }));

const searchBarState = { selectedLanguage: 'ru' };
vi.mock('@/context/SearchBarContext', () => ({ useSearchBar: () => searchBarState }));

// A stable t: an effect-keyed identity that changes every render would loop.
// Interpolated calls collapse to the key — the assertions read keys, not text.
const translate = (key: string, options?: unknown) => (typeof options === 'string' ? options : key);
const translation = { t: translate };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const BOOKS = [
    { value: 'Звездная пыль', type: 'book' as const, id: 1 },
    { value: 'Звездная тень', type: 'book' as const, id: 2 },
];

/**
 * The box is fully controlled, so the harness owns the value the way the search
 * panel does — otherwise typing would go nowhere.
 */
const Harness: React.FC<
    { onChange: (v: string) => void } & Partial<React.ComponentProps<typeof AutocompleteSearch>>
> = ({ onChange, ...props }) => {
    const [value, setValue] = React.useState(props.value ?? '');
    return (
        <AutocompleteSearch
            searchType="title"
            placeholder="Поиск"
            {...props}
            value={value}
            onChange={(next) => {
                setValue(next);
                onChange(next);
            }}
        />
    );
};

function setup(props: Partial<React.ComponentProps<typeof AutocompleteSearch>> = {}) {
    const onChange = vi.fn();
    const onEnterPressed = vi.fn();
    const view = render(<Harness onChange={onChange} onEnterPressed={onEnterPressed} {...props} />);
    return { ...view, onChange, onEnterPressed, input: screen.getByRole('combobox') };
}

beforeEach(() => {
    authorState.authorId = '';
    searchBarState.selectedLanguage = 'ru';
    getSuggestions.mockReset();
    getSuggestions.mockResolvedValue(BOOKS);
});

describe('AutocompleteSearch', () => {
    it('asks for nothing until there are three characters to ask about', async () => {
        const user = userEvent.setup();
        const { input } = setup();

        await user.type(input, 'Зв');

        // Long enough for the debounce to have fired if it were going to.
        await new Promise((resolve) => setTimeout(resolve, 500));
        expect(getSuggestions).not.toHaveBeenCalled();

        await user.type(input, 'е');
        await waitFor(() => expect(getSuggestions).toHaveBeenCalledTimes(1));
        expect(getSuggestions).toHaveBeenCalledWith(
            'Зве',
            'title',
            undefined,
            'ru',
            expect.any(AbortSignal),
        );
    });

    it('stays shut over a page it was seeded onto', async () => {
        // Landing on /search?q=война&type=books seeds the field from the route.
        // The picker used to spring open across the results underneath it.
        const { input } = setup({ value: 'война' });

        await new Promise((resolve) => setTimeout(resolve, 500));
        expect(screen.queryByRole('listbox')).not.toBeInTheDocument();

        // Reaching for the field is asking for it, and it opens.
        await userEvent.setup().click(input);
        await userEvent.setup().type(input, 'х');
        await waitFor(() => expect(screen.getByRole('listbox')).toBeInTheDocument());
    });

    it('stays shut when the search seeds it after mount, as navigation does', async () => {
        // The search writes the query into the box from an effect, so
        // the value arrives a beat after mount rather than with it. Rendering
        // empty and then filling in is what a real navigation looks like.
        const props = {
            searchType: 'title',
            placeholder: 'Поиск',
            onChange: vi.fn(),
        } as const;
        const { rerender } = render(<AutocompleteSearch {...props} value="" />);
        rerender(<AutocompleteSearch {...props} value="война" />);

        await new Promise((resolve) => setTimeout(resolve, 500));
        expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    });

    it('counts code points, so two astral characters are not three', async () => {
        const user = userEvent.setup();
        const { input } = setup();

        // Two characters a reader sees, four UTF-16 code units. Counting units
        // would send this to the backend, which counts runes and answers with
        // an empty picker — the two ends of the same gate disagreeing.
        await user.type(input, '😀🚀');

        await new Promise((resolve) => setTimeout(resolve, 500));
        expect(getSuggestions).not.toHaveBeenCalled();

        // A third code point opens the gate on both sides.
        await user.type(input, '🎈');
        await waitFor(() => expect(getSuggestions).toHaveBeenCalledTimes(1));
        expect(getSuggestions).toHaveBeenCalledWith(
            '😀🚀🎈',
            'title',
            undefined,
            'ru',
            expect.any(AbortSignal),
        );
    });

    it('collapses a burst of typing into a single request', async () => {
        const user = userEvent.setup();
        const { input } = setup();

        await user.type(input, 'Звездная');

        await waitFor(() => expect(getSuggestions).toHaveBeenCalled());
        await new Promise((resolve) => setTimeout(resolve, 400));
        expect(getSuggestions).toHaveBeenCalledTimes(1);
        expect(getSuggestions).toHaveBeenCalledWith(
            'Звездная',
            'title',
            undefined,
            'ru',
            expect.any(AbortSignal),
        );
    });

    it('searches an author by name but their own books by title', async () => {
        const user = userEvent.setup();
        authorState.authorId = '42';
        const { input } = setup({ searchType: 'author' });

        await user.type(input, 'Пришвин');
        await waitFor(() =>
            expect(getSuggestions).toHaveBeenCalledWith(
                'Пришвин',
                'author',
                undefined,
                'ru',
                expect.any(AbortSignal),
            ),
        );
    });

    // Where to search is a prop of its own rather than a value of searchType:
    // what is being looked for and where it is being looked for are separate
    // questions, and the field now asks the second one on screen.
    it('confines suggestions to the list the search is scoped to', async () => {
        const user = userEvent.setup();
        const { input } = setup({ searchType: 'title', scope: { kind: 'author', id: '42' } });

        await user.type(input, 'Старый');
        await waitFor(() =>
            expect(getSuggestions).toHaveBeenCalledWith(
                'Старый',
                'title',
                { kind: 'author', id: '42' },
                'ru',
                expect.any(AbortSignal),
            ),
        );
    });

    it('asks the whole library when no scope is given', async () => {
        const user = userEvent.setup();
        const { input } = setup({ searchType: 'title' });

        await user.type(input, 'Старый');
        await waitFor(() =>
            expect(getSuggestions).toHaveBeenCalledWith(
                'Старый',
                'title',
                undefined,
                'ru',
                expect.any(AbortSignal),
            ),
        );
    });

    it('aborts the request in flight when the query moves on', async () => {
        const user = userEvent.setup();
        const { input } = setup();

        await user.type(input, 'Звездная');
        await waitFor(() => expect(getSuggestions).toHaveBeenCalledTimes(1));
        const firstSignal = getSuggestions.mock.calls[0][4] as AbortSignal;

        await user.type(input, '!');
        await waitFor(() => expect(getSuggestions).toHaveBeenCalledTimes(2));

        expect(firstSignal.aborted).toBe(true);
    });

    it('shows what came back, labelled by kind', async () => {
        const user = userEvent.setup();
        const { input } = setup();

        await user.type(input, 'Звездная');

        await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(2));
        expect(screen.getByText('Звездная пыль')).toBeInTheDocument();
        expect(screen.getAllByText('book')).toHaveLength(2);
    });

    it('shows the author beside a titled book and the size of an author', async () => {
        const user = userEvent.setup();
        getSuggestions.mockResolvedValue([
            {
                value: 'Сто лет одиночества',
                type: 'book' as const,
                id: 1,
                secondary: 'Толстой Лев',
            },
            { value: 'Толстой Лев', type: 'author' as const, id: 2, books_count: 10 },
        ]);
        const { input } = setup();

        await user.type(input, 'Сто лет');

        await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(2));
        // The secondary names the book's author; the author row names itself.
        expect(screen.getAllByText('Толстой Лев')).toHaveLength(2);
        expect(screen.getByText('bookCount')).toBeInTheDocument();
    });

    it('adopts a suggestion the reader picks and closes the list', async () => {
        const user = userEvent.setup();
        const { input, onChange, onEnterPressed } = setup();

        await user.type(input, 'Звездная');
        await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(2));

        await user.click(screen.getByText('Звездная тень'));

        expect(onChange).toHaveBeenLastCalledWith('Звездная тень');
        expect(screen.queryByRole('option')).not.toBeInTheDocument();
        // Picking is not submitting.
        expect(onEnterPressed).not.toHaveBeenCalled();
    });

    it('reports which suggestion was picked', async () => {
        const user = userEvent.setup();
        const onSuggestionSelected = vi.fn();
        const { input } = setup({ onSuggestionSelected });

        await user.type(input, 'Звездная');
        await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(2));

        await user.click(screen.getByText('Звездная тень'));

        expect(onSuggestionSelected).toHaveBeenCalledTimes(1);
        expect(onSuggestionSelected).toHaveBeenCalledWith(BOOKS[1]);
    });

    it('submits what was typed when no suggestion is highlighted', async () => {
        const user = userEvent.setup();
        const { input, onEnterPressed } = setup();

        await user.type(input, 'Звездная{Enter}');

        expect(onEnterPressed).toHaveBeenCalledTimes(1);
    });

    it('takes the highlighted suggestion instead of submitting', async () => {
        const user = userEvent.setup();
        const { input, onChange, onEnterPressed } = setup();

        await user.type(input, 'Звездная');
        await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(2));

        await user.keyboard('{ArrowDown}{Enter}');

        expect(onChange).toHaveBeenLastCalledWith('Звездная пыль');
        expect(onEnterPressed).not.toHaveBeenCalled();
    });

    it('drops suggestions the moment the query changes under them', async () => {
        const user = userEvent.setup();
        const { input } = setup();

        await user.type(input, 'Звездная');
        await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(2));

        await user.type(input, 'X');

        // Stale suggestions under a changed query are worse than none.
        expect(screen.queryByText('Звездная пыль')).not.toBeInTheDocument();
    });

    it('shows a retryable error instead of "no options" when the request fails', async () => {
        const user = userEvent.setup();
        getSuggestions.mockRejectedValue(new Error('boom'));
        const { input } = setup();

        await user.type(input, 'Звездная');

        await waitFor(() => expect(screen.getByText('searchSuggestionsError')).toBeInTheDocument());
        expect(screen.queryByText('noOptions')).not.toBeInTheDocument();

        getSuggestions.mockResolvedValue(BOOKS);
        await user.click(screen.getByRole('button', { name: 'retry' }));

        await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(2));
    });

    it('offers no controls at all while the favourites filter is on', async () => {
        const user = userEvent.setup();
        const { input } = setup({ disabled: true });

        expect(input).toBeDisabled();
        await user.type(input, 'Звездная');
        expect(getSuggestions).not.toHaveBeenCalled();
    });

    it('keeps the browser out of it', () => {
        const { input } = setup();

        // type="search" would add the browser's own clear button — system chrome
        // this interface uses nowhere else.
        expect(input).toHaveAttribute('type', 'text');
    });

    it('keeps no copy of the query — the context owns it', async () => {
        const user = userEvent.setup();
        const onChange = vi.fn();
        const { rerender } = render(
            <AutocompleteSearch
                value="Пришвин"
                onChange={onChange}
                searchType="title"
                placeholder="Поиск"
            />,
        );
        expect(screen.getByRole('combobox')).toHaveValue('Пришвин');

        // Typing reports upwards and changes nothing on its own: a box that kept
        // its own copy would drift from the panel that resets it.
        await user.type(screen.getByRole('combobox'), 'X');
        expect(onChange).toHaveBeenCalledWith('ПришвинX');
        expect(screen.getByRole('combobox')).toHaveValue('Пришвин');

        rerender(
            <AutocompleteSearch
                value=""
                onChange={onChange}
                searchType="title"
                placeholder="Поиск"
            />,
        );
        expect(screen.getByRole('combobox')).toHaveValue('');
    });
});

describe('AutocompleteSearch as one field for books and authors', () => {
    const MIXED = [
        { value: 'Кинг Конг', type: 'book' as const, id: 1, books_count: 3 },
        { value: 'Кинг', type: 'book' as const, id: 2, books_count: 1 },
        { value: 'Стивен Кинг', type: 'author' as const, id: 'a', books_count: 778 },
    ];

    const type = async (props = {}, text = 'кинг') => {
        const view = setup({ searchType: 'all', ...props });
        await userEvent.type(view.input, text);
        return view;
    };
    const rows = () => screen.findAllByRole('option');

    beforeEach(() => {
        getSuggestions.mockResolvedValue(MIXED);
    });

    it('asks for both lanes', async () => {
        await type();

        await waitFor(() =>
            expect(getSuggestions).toHaveBeenCalledWith(
                'кинг',
                'all',
                undefined,
                'ru',
                expect.anything(),
            ),
        );
    });

    it('puts the lanes in the mixed order', async () => {
        await type();

        const first = (await rows())[0];
        expect(first).toHaveTextContent('Стивен Кинг');
    });

    it('keeps the server’s order for one lane', async () => {
        await type({ searchType: 'title' });

        expect((await rows())[0]).toHaveTextContent('Кинг Конг');
    });

    it('names the kind of every row, and what is behind it', async () => {
        await type();
        await rows();

        expect(screen.getByText('author')).toBeInTheDocument();
        expect(screen.getByText('bookCount')).toBeInTheDocument();
        expect(screen.getAllByText('book')).toHaveLength(2);
        expect(screen.getAllByText('editionCount')).toHaveLength(1);
    });

    it('fills nothing until the reader moves', async () => {
        const { input } = await type();
        const options = await rows();

        expect(input).not.toHaveAttribute('aria-activedescendant');
        for (const option of options) {
            expect(option).toHaveAttribute('aria-selected', 'false');
        }
    });

    it('keeps the fill and the announced row the same one', async () => {
        const { input } = await type();
        const options = await rows();

        await userEvent.keyboard('{ArrowDown}{ArrowDown}');

        expect(options[1]).toHaveAttribute('aria-selected', 'true');
        expect(input).toHaveAttribute('aria-activedescendant', options[1].id);
        // The fill itself, on that row and no other.
        expect(options[1]).toHaveClass('bg-accent');
        expect(options[0]).not.toHaveClass('bg-accent');
    });

    it('walks back off the list, not round it', async () => {
        const { input } = await type();
        await rows();

        await userEvent.keyboard('{ArrowDown}{ArrowUp}');

        expect(input).not.toHaveAttribute('aria-activedescendant');

        await userEvent.keyboard('{ArrowUp}');
        expect(input).not.toHaveAttribute('aria-activedescendant');
    });

    it('stops at the last row', async () => {
        const { input } = await type();
        const options = await rows();

        await userEvent.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}');

        expect(input).toHaveAttribute('aria-activedescendant', options.at(-1)?.id);
    });

    it('lets go of a filled row on Escape before it lets go of the list', async () => {
        const { input } = await type();
        await rows();

        await userEvent.keyboard('{ArrowDown}{Escape}');
        expect(input).not.toHaveAttribute('aria-activedescendant');
        expect(screen.getByRole('listbox')).toBeInTheDocument();

        await userEvent.keyboard('{Escape}');
        expect(screen.queryByRole('listbox')).toBeNull();
    });

    it('fills a row under a real mouse', async () => {
        const { input } = await type();
        const options = await rows();

        fireEvent.pointerMove(options[2], { pointerType: 'mouse' });

        expect(input).toHaveAttribute('aria-activedescendant', options[2].id);

        fireEvent.pointerLeave(screen.getByRole('listbox'), { pointerType: 'mouse' });
        expect(input).not.toHaveAttribute('aria-activedescendant');
    });

    it('never leaves a fill behind a finger', async () => {
        const { input } = await type();
        const options = await rows();

        fireEvent.pointerMove(options[2], { pointerType: 'touch' });

        expect(input).not.toHaveAttribute('aria-activedescendant');
    });

    it('takes a filled row on Enter and submits the words without one', async () => {
        const onSuggestionSelected = vi.fn();
        const { onEnterPressed } = await type({ onSuggestionSelected });
        await rows();

        await userEvent.keyboard('{Enter}');
        expect(onEnterPressed).toHaveBeenCalledTimes(1);
        expect(onSuggestionSelected).not.toHaveBeenCalled();
    });

    describe('with a run row', () => {
        it('offers the search itself, saying where it goes', async () => {
            await type({ runTag: 'в книгах' });
            await rows();

            expect(screen.getByRole('button', { name: /search\.run/ })).toHaveTextContent(
                'в книгах',
            );
        });

        it('is the same submit as Enter', async () => {
            const { onEnterPressed } = await type({ runTag: 'в книгах' });

            await userEvent.click(await screen.findByRole('button', { name: /search\.run/ }));

            expect(onEnterPressed).toHaveBeenCalledTimes(1);
        });

        it('stands outside the rows the arrows walk', async () => {
            const { input } = await type({ runTag: 'в книгах' });
            const options = await rows();

            expect(options.map((option) => option.textContent)).not.toContain('search.run');
            await userEvent.keyboard('{ArrowDown}');

            expect(input).toHaveAttribute('aria-activedescendant', options[0].id);
        });

        it('shows from the first letter, ahead of any suggestion', async () => {
            await type({ runTag: 'в книгах' }, 'к');

            expect(screen.getByRole('button', { name: /search\.run/ })).toBeInTheDocument();
            expect(screen.queryByText('typeToSearch')).toBeNull();
            expect(getSuggestions).not.toHaveBeenCalled();
        });

        it('still reports a failure with a retry', async () => {
            getSuggestions.mockRejectedValue(new Error('down'));
            await type({ runTag: 'в книгах' });

            expect(await screen.findByText('searchSuggestionsError')).toBeInTheDocument();
            expect(screen.getByRole('button', { name: 'retry' })).toBeInTheDocument();
        });

        it('stays shut over a seeded page, as the list does', () => {
            setup({ searchType: 'all', runTag: 'в книгах', value: 'кинг' });

            expect(screen.queryByRole('button', { name: /search\.run/ })).toBeNull();
        });
    });

    describe('in an empty field', () => {
        const shelf = { label: 'Избранное', onPick: vi.fn() };

        beforeEach(() => shelf.onPick.mockClear());

        it('shows nothing until it has focus', () => {
            setup({ searchType: 'all', hint: 'Подсказка', shelf });

            expect(screen.queryByText('Подсказка')).toBeNull();
        });

        it('offers the shelf and says how to search', async () => {
            const { input } = setup({ searchType: 'all', hint: 'Подсказка', shelf });

            await userEvent.click(input);

            expect(screen.getByRole('option', { name: 'Избранное' })).toBeInTheDocument();
            expect(screen.getByText('Подсказка')).toBeInTheDocument();
        });

        it('lets the arrows reach the shelf and Enter take it', async () => {
            const { input, onEnterPressed } = setup({ searchType: 'all', shelf });

            await userEvent.click(input);
            await userEvent.keyboard('{ArrowDown}');
            expect(input).toHaveAttribute(
                'aria-activedescendant',
                screen.getByRole('option', { name: 'Избранное' }).id,
            );

            await userEvent.keyboard('{Enter}');

            expect(shelf.onPick).toHaveBeenCalledTimes(1);
            expect(onEnterPressed).not.toHaveBeenCalled();
        });

        it('goes by the tap too', async () => {
            const { input } = setup({ searchType: 'all', shelf });

            await userEvent.click(input);
            await userEvent.click(screen.getByRole('option', { name: 'Избранное' }));

            expect(shelf.onPick).toHaveBeenCalledTimes(1);
        });

        it('offers no search of nothing', async () => {
            const { input } = setup({ searchType: 'all', runTag: 'в книгах', shelf });

            await userEvent.click(input);

            expect(screen.queryByRole('button', { name: /search\.run/ })).toBeNull();
        });

        it('takes the row under the arrows once there are words, not the shelf', async () => {
            const onSuggestionSelected = vi.fn();
            await type({ shelf, onSuggestionSelected });
            await rows();

            await userEvent.keyboard('{ArrowDown}{Enter}');

            expect(onSuggestionSelected).toHaveBeenCalledTimes(1);
            expect(shelf.onPick).not.toHaveBeenCalled();
        });

        it('gives way to suggestions once there are words', async () => {
            await type({ hint: 'Подсказка', shelf });
            await rows();

            expect(screen.queryByRole('option', { name: 'Избранное' })).toBeNull();
            expect(screen.queryByText('Подсказка')).toBeNull();
        });
    });
});

describe('AutocompleteSearch in a takeover', () => {
    beforeEach(() => {
        getSuggestions.mockResolvedValue(BOOKS);
    });

    it('stands open with nothing to show', () => {
        setup({ searchType: 'all', taking: true, top: <div>top</div> });

        expect(screen.getByText('top')).toBeInTheDocument();
    });

    it('asks to take the screen on a real focus only', async () => {
        const onFocused = vi.fn();
        const { input } = setup({ searchType: 'all', value: 'кинг', onFocused });
        expect(onFocused).not.toHaveBeenCalled();

        await userEvent.click(input);

        expect(onFocused).toHaveBeenCalledTimes(1);
    });

    it('asks to be closed by Escape once nothing is filled', async () => {
        const onDismissed = vi.fn();
        const { input } = setup({ searchType: 'all', onDismissed });
        await userEvent.type(input, 'кинг');
        await screen.findAllByRole('option');

        await userEvent.keyboard('{ArrowDown}{Escape}');
        expect(onDismissed).not.toHaveBeenCalled();

        await userEvent.keyboard('{Escape}');
        expect(onDismissed).toHaveBeenCalledTimes(1);
    });

    it('takes the caret with it when it ends, so the keyboard goes', async () => {
        const { input, rerender } = setup({ searchType: 'all', taking: true });
        await userEvent.click(input);
        expect(input).toHaveFocus();

        rerender(
            <Harness onChange={vi.fn()} onEnterPressed={vi.fn()} searchType="all" taking={false} />,
        );

        expect(input).not.toHaveFocus();
    });

    it('keeps the input the same element through the change', async () => {
        const { input, rerender } = setup({ searchType: 'all', taking: false });

        rerender(<Harness onChange={vi.fn()} searchType="all" taking />);

        expect(screen.getByRole('combobox')).toBe(input);
    });

    it('goes back to what the empty field offers when the cross empties it', async () => {
        const { input } = setup({ searchType: 'all', hint: 'Подсказка', taking: true });
        await userEvent.type(input, 'ки');

        await userEvent.click(screen.getByRole('button', { name: 'clearSearch' }));

        expect(screen.getByText('Подсказка')).toBeInTheDocument();
        expect(input).toHaveFocus();
    });

    it('leaves room for the keyboard at the end of the panel', () => {
        setup({ searchType: 'all', taking: true, keyboard: 300, top: <div>top</div> });

        expect(screen.getByText('top').parentElement).toHaveStyle({ paddingBottom: '300px' });
    });
});
