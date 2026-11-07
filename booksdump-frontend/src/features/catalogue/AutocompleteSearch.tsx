import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { BookOpen, Loader2, Search, Star, User, X } from 'lucide-react';

import { inputFocusRing, inputFrame } from '@/shared/ui/input';
import { cn } from '@/shared/lib/utils';

import { autocompleteService, AutocompleteSuggestion, SuggestionScope } from '@/api/autocomplete';
import { useSearchBar } from '@/context/SearchBarContext';
import { orderSuggestions } from '@/features/catalogue/suggestionOrder';

/**
 * AutocompleteSearch is a free-text search box that offers suggestions.
 *
 * It is not a picker: the reader may submit anything they typed, whether or not
 * it matches a suggestion. That is why this is an input with a listbox beside it
 * rather than a combobox that insists on a selection.
 *
 * The request rules are load-bearing: nothing is asked for under three
 * characters, requests are debounced, and each new keystroke aborts the one in
 * flight — otherwise a slow answer to an old query lands after a fast answer to
 * the current one and replaces it. A failed request is reported as a failure
 * with a way to retry, because rendering it as an empty list would make a dead
 * backend indistinguishable from "no suggestions".
 *
 * Suggestions are kept together with the query they answer and shown only while
 * the two still agree. Holding them apart, and clearing the list by hand
 * wherever the query changes, is the same rule enforced in several places at
 * once — and only as good as the least careful of them.
 */

const MIN_QUERY_LENGTH = 3;

/**
 * Counts Unicode code points, not UTF-16 code units, because that is what the
 * backend counts. `'😀a'.length` is 3 — one astral character costs two units —
 * so a two-character query would have passed this gate here and been answered
 * with an empty picker there. Iterating a string yields code points.
 */
const runeLength = (value: string): number => [...value].length;
const DEBOUNCE_MS = 300;

interface AutocompleteSearchProps {
    value: string;
    onChange: (value: string) => void;
    searchType: string;
    /**
     * Confines suggestions to the list the reader is standing in. Passed in
     * rather than inferred from searchType: what is being searched for and
     * where it is being searched are two separate questions, and folding them
     * into one string was what made the second one invisible.
     */
    scope?: SuggestionScope;
    disabled?: boolean;
    onEnterPressed?: () => void;
    /**
     * Fires when the reader picks a suggestion, with the full suggestion —
     * type and id included. The text goes through onChange like any edit;
     * navigation to what was picked is the parent's call, not this box's.
     */
    onSuggestionSelected?: (suggestion: AutocompleteSuggestion) => void;
    /**
     * What clearing the box means when the box holds the query the page is
     * filtered by: undo the search, not just the text. Absent while nothing is
     * being searched, and then the cross only empties the field.
     */
    onClear?: () => void;
    placeholder?: string;
    /**
     * Where the search goes, said on a row of its own above the suggestions.
     * Without it there is no such row.
     */
    runTag?: string;
    /** What the open field says while it is empty. */
    hint?: string;
    /** One place to go, offered in the open field while it is empty. */
    shelf?: { label: string; onPick: () => void };
}

interface Answer {
    query: string;
    items: AutocompleteSuggestion[];
    /** True when the request for query failed — not to be read as "no options". */
    error: boolean;
}

const NO_ANSWER: Answer = { query: '\u0000', items: [], error: false };

/** apiTypeFor maps a panel search mode onto the autocomplete endpoint's own. */
function apiTypeFor(searchType: string): 'title' | 'author' | 'all' {
    switch (searchType) {
        case 'title':
            return 'title';
        case 'author':
            return 'author';
        default:
            return 'all';
    }
}

const AutocompleteSearch: React.FC<AutocompleteSearchProps> = ({
    value,
    onChange,
    searchType,
    scope,
    disabled = false,
    onEnterPressed,
    onSuggestionSelected,
    onClear,
    placeholder,
    runTag,
    hint: emptyHint,
    shelf,
}) => {
    const { t } = useTranslation();
    const { selectedLanguage } = useSearchBar();

    const [answer, setAnswer] = useState<Answer>(NO_ANSWER);
    const [loading, setLoading] = useState(false);
    // Text the reader did not type is text the picker should stay shut over.
    // The query lives in the URL, so landing on a filtered page puts words in
    // the box — at mount, and again on every navigation, which the panel does
    // from an effect rather than at render. Both used to spring the picker
    // open across the results underneath it. Focusing or typing opens it.
    const [dismissed, setDismissed] = useState(true);
    const typed = useRef(value);
    const [highlighted, setHighlighted] = useState(-1);

    const abortControllerRef = useRef<AbortController | null>(null);
    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const containerRef = useRef<HTMLDivElement | null>(null);
    const inputRef = useRef<HTMLInputElement | null>(null);

    const query = value.trim();
    // Anything fetched for a different query is simply not shown.
    const answered = answer.query === query ? answer.items : [];
    const suggestions = searchType === 'all' ? orderSuggestions(answered, query) : answered;
    const listId = useId();
    const optionId = (index: number) => `${listId}-${index}`;
    const failed = answer.query === query && answer.error;

    const fetchSuggestions = useCallback(
        async (
            wanted: string,
            type: string,
            currentScope: SuggestionScope | undefined,
            currentLanguage: string,
        ) => {
            abortControllerRef.current?.abort();
            const controller = new AbortController();
            abortControllerRef.current = controller;
            setLoading(true);

            try {
                const results = await autocompleteService.getSuggestions(
                    wanted,
                    apiTypeFor(type),
                    currentScope || undefined,
                    currentLanguage,
                    controller.signal,
                );
                if (controller.signal.aborted) return;
                setAnswer({ query: wanted, items: results || [], error: false });
                setHighlighted(-1);
            } catch {
                if (!controller.signal.aborted) {
                    setAnswer({ query: wanted, items: [], error: true });
                }
            } finally {
                if (!controller.signal.aborted) {
                    setLoading(false);
                }
            }
        },
        [],
    );

    useEffect(() => {
        if (runeLength(query) < MIN_QUERY_LENGTH) {
            abortControllerRef.current?.abort();
            abortControllerRef.current = null;
            return;
        }
        if (answer.query === query) {
            return;
        }

        const timer = setTimeout(
            () => fetchSuggestions(query, searchType, scope, selectedLanguage),
            DEBOUNCE_MS,
        );
        timerRef.current = timer;
        return () => clearTimeout(timer);
        // answer.query is read but deliberately not a dependency: reacting to it
        // would schedule a fresh request the moment one arrives.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [query, searchType, scope, selectedLanguage, fetchSuggestions]);

    useEffect(
        () => () => {
            abortControllerRef.current?.abort();
            if (timerRef.current) clearTimeout(timerRef.current);
        },
        [],
    );

    useEffect(() => {
        if (dismissed) return;
        const onPointerDown = (event: MouseEvent) => {
            if (!containerRef.current?.contains(event.target as Node)) {
                setDismissed(true);
            }
        };
        document.addEventListener('mousedown', onPointerDown);
        return () => document.removeEventListener('mousedown', onPointerDown);
    }, [dismissed]);

    useEffect(() => {
        if (value === typed.current) {
            return;
        }
        typed.current = value;
        setDismissed(true);
    }, [value]);

    const handleInput = (next: string) => {
        typed.current = next;
        setDismissed(false);
        setHighlighted(-1);
        onChange(next);
    };

    /**
     * Emptying the box puts the caret back in it: clearing is almost always the
     * start of typing something else, and a reader who had to click the field
     * again would have paid twice for one intention.
     *
     * When the words in the box are the ones filtering the page — a search
     * lives in the URL, so they usually are — clearing them is also the way
     * back to the unfiltered list, and onClear is what carries that. Handing
     * it to this button rather than to a second control beside the field keeps
     * one gesture for one intention, and costs no width on a phone, where a
     * second chip squeezed the scope down to its cross.
     */
    const clear = () => {
        onChange('');
        setDismissed(true);
        setHighlighted(-1);
        if (onClear) {
            onClear();
            return;
        }
        inputRef.current?.focus();
    };

    /**
     * What a row adds after its text: a book's author, or an author entity's
     * genre label. A namesake of the same genre is told apart by the count the
     * row ends with, the books it opens.
     */
    const asideOf = (suggestion: AutocompleteSuggestion) =>
        suggestion.secondary || suggestion.label;

    /** How much a row stands for: an author's books, a title's editions. */
    const countOf = (suggestion: AutocompleteSuggestion) => {
        const count = suggestion.books_count ?? 0;
        if (suggestion.type === 'author') {
            return count > 0 ? t('bookCount', { count }) : '';
        }
        return count > 1 ? t('editionCount', { count }) : '';
    };

    const isEmpty = query.length === 0;
    const onShelf = isEmpty && shelf !== undefined;
    const optionCount = onShelf ? 1 : suggestions.length;

    const pick = (suggestion: AutocompleteSuggestion) => {
        onChange(suggestion.value);
        setDismissed(true);
        onSuggestionSelected?.(suggestion);
    };

    /**
     * A failed answer is retried on demand, without the debounce: the reader
     * already waited once for this query, and the second wait is the one that
     * would feel broken.
     */
    const retry = () => {
        setDismissed(false);
        void fetchSuggestions(query, searchType, scope, selectedLanguage);
    };

    const takeShelf = () => {
        setDismissed(true);
        setHighlighted(-1);
        shelf?.onPick();
    };

    const handleKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
        const listOpen = !dismissed && optionCount > 0;

        // The arrows stop at the ends rather than going round: above the first
        // row is nothing filled, and Enter there is a search of the words.
        if (event.key === 'ArrowDown' && listOpen) {
            event.preventDefault();
            setHighlighted((index) => Math.min(index + 1, optionCount - 1));
            return;
        }
        if (event.key === 'ArrowUp' && listOpen) {
            event.preventDefault();
            setHighlighted((index) => Math.max(index - 1, -1));
            return;
        }
        if (event.key === 'Escape') {
            if (highlighted >= 0) {
                setHighlighted(-1);
                return;
            }
            setDismissed(true);
            return;
        }
        if (event.key === 'Enter') {
            // A highlighted suggestion is a choice; anything else submits what
            // was typed.
            if (listOpen && highlighted >= 0) {
                event.preventDefault();
                if (onShelf) {
                    takeShelf();
                } else {
                    pick(suggestions[highlighted]);
                }
                return;
            }
            setDismissed(true);
            onEnterPressed?.();
        }
    };

    const run = () => {
        setDismissed(true);
        onEnterPressed?.();
    };

    const hint = runeLength(query) < MIN_QUERY_LENGTH ? t('typeToSearch') : t('noOptions');
    const showList = !dismissed && !disabled && query.length > 0;
    const showEmpty = !dismissed && !disabled && isEmpty && Boolean(shelf || emptyHint);
    const open = showList || showEmpty;
    const clearable = value.length > 0 && !disabled;

    return (
        <div ref={containerRef} className="relative">
            {/*
              Deliberately type="text", not "search": a search input draws the
              browser's own clear button, which is system chrome this interface
              does not use anywhere else — and it lands exactly where the loading
              spinner goes.
            */}
            {/*
              The frame lives on the wrapper rather than on the input so the
              spinner and the clear button can be laid out beside the text
              instead of floated over it with padding measured to match.
            */}
            <div
                className={cn(
                    inputFrame,
                    inputFocusRing.replace(/focus-visible:/g, 'focus-within:'),
                    'flex items-center gap-1.5 py-0',
                    (clearable || loading) && 'pr-1',
                    disabled && 'pointer-events-none cursor-not-allowed opacity-50',
                )}
            >
                <input
                    ref={inputRef}
                    type="text"
                    role="combobox"
                    aria-expanded={open}
                    aria-autocomplete="list"
                    aria-controls={open ? listId : undefined}
                    aria-activedescendant={highlighted >= 0 ? optionId(highlighted) : undefined}
                    aria-label={placeholder || t('searchItem')}
                    placeholder={placeholder || t('searchItem')}
                    value={value}
                    disabled={disabled}
                    onChange={(event) => handleInput(event.target.value)}
                    onFocus={() => setDismissed(false)}
                    onKeyDown={handleKeyDown}
                    className="h-full min-w-0 flex-1 bg-transparent text-inherit outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed"
                />

                {loading && (
                    <Loader2
                        aria-hidden="true"
                        className="pointer-events-none size-4 shrink-0 animate-spin text-muted-foreground"
                    />
                )}

                {clearable && (
                    <button
                        type="button"
                        tabIndex={-1}
                        aria-label={onClear ? t('resetSearch') : t('clearSearch')}
                        title={onClear ? t('resetSearch') : t('clearSearch')}
                        onClick={clear}
                        className="flex size-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
                    >
                        <X className="size-4" />
                    </button>
                )}
            </div>

            {open && (
                <div
                    className={cn(
                        'absolute z-50 mt-1 max-h-72 w-full overflow-y-auto rounded-md border border-border bg-popover p-1 shadow-md',
                        'scrollbar-thin',
                    )}
                >
                    {showList && runTag !== undefined && (
                        <button
                            type="button"
                            onClick={run}
                            className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm font-medium hover:bg-accent"
                        >
                            <Search
                                aria-hidden="true"
                                className="size-4 shrink-0 text-muted-foreground"
                            />
                            <span className="min-w-0 flex-1 truncate">
                                {t('search.run', { query })}
                            </span>
                            <span className="shrink-0 text-xs text-muted-foreground">{runTag}</span>
                        </button>
                    )}
                    {/* list-none: a bare <ul> keeps its marker and its indent. */}
                    <ul
                        id={listId}
                        role="listbox"
                        aria-label={t('search.suggestions')}
                        onPointerLeave={(event) => {
                            if (event.pointerType === 'mouse') setHighlighted(-1);
                        }}
                        className="m-0 list-none p-0"
                    >
                        {showEmpty && shelf && (
                            <li>
                                <button
                                    type="button"
                                    role="option"
                                    id={optionId(0)}
                                    aria-selected={highlighted === 0}
                                    onPointerMove={(event) => {
                                        if (event.pointerType === 'mouse') setHighlighted(0);
                                    }}
                                    onClick={takeShelf}
                                    className={cn(
                                        'flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm',
                                        highlighted === 0 && 'bg-accent text-accent-foreground',
                                    )}
                                >
                                    <Star
                                        aria-hidden="true"
                                        className="size-4 shrink-0 text-muted-foreground"
                                    />
                                    {shelf.label}
                                </button>
                            </li>
                        )}
                        {showList &&
                            (suggestions.length === 0 ? (
                                failed ? (
                                    <li className="flex items-center justify-between gap-2 px-2 py-1.5 text-sm text-muted-foreground">
                                        <span className="min-w-0 truncate">
                                            {t('searchSuggestionsError')}
                                        </span>
                                        <button
                                            type="button"
                                            onClick={retry}
                                            className="shrink-0 rounded px-1.5 py-0.5 text-xs font-medium text-foreground hover:bg-accent"
                                        >
                                            {t('retry')}
                                        </button>
                                    </li>
                                ) : runTag === undefined ? (
                                    <li className="px-2 py-1.5 text-sm text-muted-foreground">
                                        {loading ? t('loading') : hint}
                                    </li>
                                ) : !loading && runeLength(query) >= MIN_QUERY_LENGTH ? (
                                    <li className="px-2 py-1.5 text-sm text-muted-foreground">
                                        {t('noOptions')}
                                    </li>
                                ) : null
                            ) : (
                                suggestions.map((suggestion, index) => (
                                    <li key={`${suggestion.type}-${suggestion.value}-${index}`}>
                                        <button
                                            type="button"
                                            role="option"
                                            id={optionId(index)}
                                            aria-selected={index === highlighted}
                                            onPointerMove={(event) => {
                                                // The fill means Enter takes this
                                                // row, and a finger never leaves
                                                // one behind.
                                                if (event.pointerType === 'mouse') {
                                                    setHighlighted(index);
                                                }
                                            }}
                                            onClick={() => pick(suggestion)}
                                            className={cn(
                                                'flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm',
                                                index === highlighted &&
                                                    'bg-accent text-accent-foreground',
                                            )}
                                        >
                                            {suggestion.type === 'author' ? (
                                                <User
                                                    aria-hidden="true"
                                                    className="size-4 shrink-0 text-muted-foreground"
                                                />
                                            ) : (
                                                <BookOpen
                                                    aria-hidden="true"
                                                    className="size-4 shrink-0 text-muted-foreground"
                                                />
                                            )}
                                            <span className="min-w-0 flex-1 truncate">
                                                {suggestion.value}
                                                {asideOf(suggestion) && (
                                                    <span className="ml-1.5 text-xs text-muted-foreground">
                                                        {asideOf(suggestion)}
                                                    </span>
                                                )}
                                            </span>
                                            {/*
                                              Every row names its kind in words,
                                              since one list holds both now, and
                                              says how much is behind it. A title
                                              used to say only "Книга", once per
                                              writer who had used it, so the same
                                              words filled the picker as if they
                                              were different choices.
                                            */}
                                            <span className="shrink-0 text-xs text-muted-foreground">
                                                {suggestion.type === 'author'
                                                    ? t('author')
                                                    : t('book')}
                                            </span>
                                            {countOf(suggestion) && (
                                                <span className="shrink-0 text-xs text-muted-foreground">
                                                    {countOf(suggestion)}
                                                </span>
                                            )}
                                        </button>
                                    </li>
                                ))
                            ))}
                    </ul>
                    {showEmpty && emptyHint && (
                        <p className="m-0 px-2 py-1.5 text-sm text-muted-foreground">{emptyHint}</p>
                    )}
                </div>
            )}
        </div>
    );
};

export default AutocompleteSearch;
