import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
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
    // questions, and the panel now asks the second one on screen.
    it('confines suggestions to the author the search is scoped to', async () => {
        const user = userEvent.setup();
        const { input } = setup({ searchType: 'title', authorScope: '42' });

        await user.type(input, 'Старый');
        await waitFor(() =>
            expect(getSuggestions).toHaveBeenCalledWith(
                'Старый',
                'title',
                '42',
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
            { value: 'Сто лет одиночества', type: 'book' as const, id: 1, secondary: 'Толстой Лев' },
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

        await waitFor(() =>
            expect(screen.getByText('searchSuggestionsError')).toBeInTheDocument(),
        );
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

    it('keeps no copy of the query — the panel owns it', async () => {
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
