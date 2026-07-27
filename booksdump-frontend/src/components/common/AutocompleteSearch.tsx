import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { BookOpen, Loader2, User } from 'lucide-react';

import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';

import { autocompleteService, AutocompleteSuggestion } from '../../api/autocomplete';
import { useAuthor } from '../../context/AuthorContext';
import { useSearchBar } from '../../context/SearchBarContext';

/**
 * AutocompleteSearch is a free-text search box that offers suggestions.
 *
 * It is not a picker: the reader may submit anything they typed, whether or not
 * it matches a suggestion. That is why this is an input with a listbox beside it
 * rather than a combobox that insists on a selection.
 *
 * The request rules are load-bearing and unchanged from the MUI version:
 * nothing is asked for under four characters, requests are debounced, and each
 * new keystroke aborts the one in flight — otherwise a slow answer to an old
 * query lands after a fast answer to the current one and replaces it.
 */

const MIN_QUERY_LENGTH = 4;
const DEBOUNCE_MS = 300;

interface AutocompleteSearchProps {
    value: string;
    onChange: (value: string) => void;
    searchType: string;
    disabled?: boolean;
    onEnterPressed?: () => void;
    placeholder?: string;
}

/** apiTypeFor maps a panel search mode onto the autocomplete endpoint's own. */
function apiTypeFor(searchType: string): 'title' | 'author' | 'all' {
    switch (searchType) {
        case 'title':
            return 'title';
        case 'author':
            return 'author';
        // An author's own books are still searched by title.
        case 'authorsBookSearch':
            return 'title';
        default:
            return 'all';
    }
}

function debounce<A extends unknown[]>(fn: (...args: A) => void, delay: number) {
    let timeoutId: ReturnType<typeof setTimeout>;
    return (...args: A) => {
        clearTimeout(timeoutId);
        timeoutId = setTimeout(() => fn(...args), delay);
    };
}

const AutocompleteSearch: React.FC<AutocompleteSearchProps> = ({
    value,
    onChange,
    searchType,
    disabled = false,
    onEnterPressed,
    placeholder,
}) => {
    const { t } = useTranslation();
    const { authorId } = useAuthor();
    const { selectedLanguage } = useSearchBar();

    const [suggestions, setSuggestions] = useState<AutocompleteSuggestion[]>([]);
    const [loading, setLoading] = useState(false);
    const [inputValue, setInputValue] = useState(value);
    const [open, setOpen] = useState(false);
    const [highlighted, setHighlighted] = useState(-1);

    const abortControllerRef = useRef<AbortController | null>(null);
    const latestQueryRef = useRef(value);
    const containerRef = useRef<HTMLDivElement | null>(null);

    const fetchSuggestions = useMemo(
        () =>
            debounce(
                async (query: string, type: string, currentAuthorId: string, currentLanguage: string) => {
                    abortControllerRef.current?.abort();

                    const trimmed = query.trim();
                    if (trimmed.length < MIN_QUERY_LENGTH) {
                        setSuggestions([]);
                        setLoading(false);
                        return;
                    }

                    const controller = new AbortController();
                    abortControllerRef.current = controller;
                    setLoading(true);

                    try {
                        const results = await autocompleteService.getSuggestions(
                            trimmed,
                            apiTypeFor(type),
                            type === 'authorsBookSearch' && currentAuthorId ? currentAuthorId : undefined,
                            currentLanguage,
                        );

                        // Drop the answer if the reader has typed on since.
                        if (controller.signal.aborted || query !== latestQueryRef.current) {
                            return;
                        }
                        setSuggestions(results || []);
                        setHighlighted(-1);
                        setOpen(true);
                    } catch {
                        if (!controller.signal.aborted) {
                            setSuggestions([]);
                        }
                    } finally {
                        if (!controller.signal.aborted) {
                            setLoading(false);
                        }
                    }
                },
                DEBOUNCE_MS,
            ),
        [],
    );

    useEffect(() => {
        latestQueryRef.current = inputValue;

        if (inputValue.trim().length < MIN_QUERY_LENGTH) {
            abortControllerRef.current?.abort();
            abortControllerRef.current = null;
            setSuggestions([]);
            setLoading(false);
            return;
        }

        fetchSuggestions(inputValue, searchType, authorId, selectedLanguage);
    }, [inputValue, searchType, authorId, selectedLanguage, fetchSuggestions]);

    // Keep in step when the panel resets the query, for instance after
    // navigating to a filtered list.
    useEffect(() => {
        setInputValue(value);
    }, [value]);

    useEffect(() => () => abortControllerRef.current?.abort(), []);

    useEffect(() => {
        if (!open) return;
        const onPointerDown = (event: MouseEvent) => {
            if (!containerRef.current?.contains(event.target as Node)) {
                setOpen(false);
            }
        };
        document.addEventListener('mousedown', onPointerDown);
        return () => document.removeEventListener('mousedown', onPointerDown);
    }, [open]);

    const handleInput = (next: string) => {
        // Clear straight away: stale suggestions under a changed query are worse
        // than none.
        setSuggestions([]);
        setLoading(false);
        setHighlighted(-1);
        setInputValue(next);
        onChange(next);
    };

    const pick = (suggestion: AutocompleteSuggestion) => {
        setInputValue(suggestion.value);
        onChange(suggestion.value);
        setOpen(false);
        setSuggestions([]);
    };

    const handleKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
        const listOpen = open && suggestions.length > 0;

        if (event.key === 'ArrowDown' && listOpen) {
            event.preventDefault();
            setHighlighted((index) => (index + 1) % suggestions.length);
            return;
        }
        if (event.key === 'ArrowUp' && listOpen) {
            event.preventDefault();
            setHighlighted((index) => (index <= 0 ? suggestions.length - 1 : index - 1));
            return;
        }
        if (event.key === 'Escape') {
            setOpen(false);
            return;
        }
        if (event.key === 'Enter') {
            // A highlighted suggestion is a choice; anything else submits what
            // was typed.
            if (listOpen && highlighted >= 0) {
                event.preventDefault();
                pick(suggestions[highlighted]);
                return;
            }
            setOpen(false);
            onEnterPressed?.();
        }
    };

    const hint =
        inputValue.trim().length < MIN_QUERY_LENGTH ? t('typeToSearch') : t('noOptions');
    const showList = open && !disabled && (loading || suggestions.length > 0 || inputValue.trim().length > 0);

    return (
        <div ref={containerRef} className="relative">
            {/*
              Deliberately type="text", not "search": a search input draws the
              browser's own clear button, which is system chrome this interface
              does not use anywhere else — and it lands exactly where the loading
              spinner goes.
            */}
            <Input
                type="text"
                role="combobox"
                aria-expanded={showList}
                aria-autocomplete="list"
                aria-label={placeholder || t('searchItem')}
                placeholder={placeholder || t('searchItem')}
                value={inputValue}
                disabled={disabled}
                onChange={(event) => handleInput(event.target.value)}
                onFocus={() => suggestions.length > 0 && setOpen(true)}
                onKeyDown={handleKeyDown}
                className="w-full"
            />

            {loading && (
                <Loader2
                    aria-hidden="true"
                    className="pointer-events-none absolute right-2.5 top-1/2 size-4 -translate-y-1/2 animate-spin text-muted-foreground"
                />
            )}

            {showList && (
                <ul
                    role="listbox"
                    className={cn(
                        // list-none: Tailwind is loaded without preflight here, so
                        // a bare <li> would still show its marker.
                        'absolute z-50 mt-1 max-h-72 w-full list-none overflow-y-auto rounded-md border border-border bg-popover p-1 shadow-md',
                        'scrollbar-thin',
                    )}
                >
                    {suggestions.length === 0 ? (
                        <li className="px-2 py-1.5 text-sm text-muted-foreground">
                            {loading ? t('loading') : hint}
                        </li>
                    ) : (
                        suggestions.map((suggestion, index) => (
                            <li key={`${suggestion.type}-${suggestion.value}-${index}`}>
                                <button
                                    type="button"
                                    role="option"
                                    aria-selected={index === highlighted}
                                    onMouseEnter={() => setHighlighted(index)}
                                    onClick={() => pick(suggestion)}
                                    className={cn(
                                        'flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm',
                                        index === highlighted && 'bg-accent text-accent-foreground',
                                    )}
                                >
                                    {suggestion.type === 'author' ? (
                                        <User aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
                                    ) : (
                                        <BookOpen aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
                                    )}
                                    <span className="min-w-0 flex-1 truncate">{suggestion.value}</span>
                                    <span className="shrink-0 text-xs text-muted-foreground">
                                        {suggestion.type === 'author' ? t('author') : t('book')}
                                    </span>
                                </button>
                            </li>
                        ))
                    )}
                </ul>
            )}
        </div>
    );
};

export default AutocompleteSearch;
