import type { Author, Book, Series } from '@/api/books';
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { X } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { BouncingDots } from '@/shared/ui/bouncing-dots';
import { Badge } from '@/shared/ui/badge';
import { Button, buttonVariants } from '@/shared/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { Field } from '@/shared/ui/field';
import { Input } from '@/shared/ui/input';
import { cn } from '@/shared/lib/utils';

import * as adminApi from '@/api/admin';
import { isApiError } from '@/api/errors';

type EditBookDialogProps = {
    open: boolean;
    onClose: () => void;
    book: Book | null;
    onBookUpdated: (updatedBook: Book) => void;
};

/** Nothing is asked of the server until there is this much to ask about. */
const MIN_QUERY_LENGTH = 2;
const DEBOUNCE_MS = 250;

const TITLE_FIELD_ID = 'edit-book-title';

/** A chosen author or series, and an offered one, reduced to what is drawn. */
type TokenItem = { key: string; label: string };

const authorKey = (author: Author) =>
    author.id ? `id:${author.id}` : `name:${author.full_name?.trim().toLowerCase()}`;
const seriesKey = (series: Series) =>
    series.id ? `id:${series.id}` : `name:${series.ser?.trim().toLowerCase()}`;

/** DismissButton sends an inline alert away without dismissing the dialog. */
const DismissButton: React.FC<{ label: string; onClick: () => void }> = ({ label, onClick }) => (
    <button
        type="button"
        aria-label={label}
        onClick={onClick}
        className="absolute top-2.5 right-3 text-muted-foreground hover:text-foreground"
    >
        <X aria-hidden="true" className="size-4" />
    </button>
);

/**
 * TokenField picks several values out of a server-side search, and accepts one
 * that is not there yet.
 *
 * The authors and series of a book are a small, open set: most of what a
 * librarian types already exists, and what does not has to be creatable in the
 * same gesture — which is why Enter on plain text adds it rather than being
 * swallowed as "no match".
 */
const TokenField: React.FC<{
    id: string;
    label: string;
    placeholder: string;
    hint: string;
    removeLabel: (label: string) => string;
    tokens: TokenItem[];
    options: TokenItem[];
    query: string;
    loading: boolean;
    disabled?: boolean;
    onQueryChange: (value: string) => void;
    onPick: (optionKey: string) => void;
    onCreate: (text: string) => void;
    onRemove: (tokenKey: string) => void;
}> = ({
    id,
    label,
    placeholder,
    hint,
    removeLabel,
    tokens,
    options,
    query,
    loading,
    disabled,
    onQueryChange,
    onPick,
    onCreate,
    onRemove,
}) => {
    const { t } = useTranslation();
    const [dismissed, setDismissed] = useState(false);
    const [highlighted, setHighlighted] = useState(-1);
    const wrapperRef = useRef<HTMLDivElement | null>(null);

    const showList = !dismissed && !disabled && query.trim().length > 0;
    const emptyHint = query.trim().length < MIN_QUERY_LENGTH ? t('typeToSearch') : t('noOptions');

    /*
     * The form scrolls, and an absolutely positioned list is clipped by the panel
     * that scrolls it — MUI put its own in a portal, which nothing here does. So
     * the field asks to be brought into view together with the room the list
     * needs, which scroll-mb below reserves for it.
     */
    useEffect(() => {
        if (!showList) return;
        wrapperRef.current?.scrollIntoView?.({ block: 'nearest' });
    }, [showList]);

    const create = () => {
        const text = query.trim();
        if (!text) return;
        onCreate(text);
        setHighlighted(-1);
    };

    const handleKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
        const listOpen = showList && options.length > 0;

        if (event.key === 'ArrowDown' && listOpen) {
            event.preventDefault();
            setHighlighted((index) => (index + 1) % options.length);
            return;
        }
        if (event.key === 'ArrowUp' && listOpen) {
            event.preventDefault();
            setHighlighted((index) => (index <= 0 ? options.length - 1 : index - 1));
            return;
        }
        if (event.key === 'Escape') {
            setDismissed(true);
            return;
        }
        if (event.key === 'Backspace' && query === '' && tokens.length > 0) {
            onRemove(tokens[tokens.length - 1].key);
            return;
        }
        if (event.key === 'Enter') {
            // A dialog-wide Enter would submit the form; here it is a choice.
            event.preventDefault();
            if (listOpen && highlighted >= 0) {
                onPick(options[highlighted].key);
                setHighlighted(-1);
                return;
            }
            create();
        }
    };

    return (
        <div ref={wrapperRef} className="flex flex-col gap-1 scroll-mb-56">
            <label htmlFor={id} className="text-xs text-muted-foreground">
                {label}
            </label>

            <div className="relative">
                {/* The chips live inside the frame, so the field reads as one
                    control rather than a box with a list stuck below it. */}
                <div
                    className={cn(
                        'flex min-h-9 w-full flex-wrap items-center gap-1 rounded-md border border-input bg-transparent px-2 py-1 shadow-xs dark:bg-input/30',
                        'focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50',
                        disabled && 'pointer-events-none opacity-50',
                    )}
                >
                    {tokens.map((token) => (
                        <Badge key={token.key} variant="outline" className="gap-1 py-0.5 pr-1 pl-2">
                            {token.label}
                            <button
                                type="button"
                                aria-label={removeLabel(token.label)}
                                disabled={disabled}
                                onClick={() => onRemove(token.key)}
                                className="rounded-full text-muted-foreground hover:text-foreground"
                            >
                                <X aria-hidden="true" className="size-3" />
                            </button>
                        </Badge>
                    ))}

                    <input
                        id={id}
                        type="text"
                        role="combobox"
                        aria-expanded={showList}
                        aria-autocomplete="list"
                        aria-describedby={`${id}-hint`}
                        placeholder={tokens.length === 0 ? placeholder : undefined}
                        value={query}
                        disabled={disabled}
                        onChange={(event) => {
                            setDismissed(false);
                            setHighlighted(-1);
                            onQueryChange(event.target.value);
                        }}
                        onFocus={() => setDismissed(false)}
                        onKeyDown={handleKeyDown}
                        className="h-7 min-w-32 flex-1 bg-transparent text-base outline-none placeholder:text-muted-foreground md:text-sm"
                    />

                    {loading && <BouncingDots className="text-muted-foreground" />}
                </div>

                {/*
                  z-modal, because the series field below is positioned too and a
                  later positioned sibling paints over an earlier one: the authors
                  list would otherwise open behind the next field's frame.
                  list-none, because Tailwind runs without preflight.
                */}
                {showList && (
                    <ul
                        role="listbox"
                        aria-label={label}
                        className="absolute z-modal mt-1 max-h-56 w-full list-none overflow-y-auto rounded-md border border-border bg-popover p-1 shadow-md scrollbar-thin"
                    >
                        {options.length === 0 ? (
                            <li className="px-2 py-1.5 text-sm text-muted-foreground">
                                {loading ? t('loading') : emptyHint}
                            </li>
                        ) : (
                            options.map((option, index) => (
                                <li key={option.key}>
                                    <button
                                        type="button"
                                        role="option"
                                        aria-selected={index === highlighted}
                                        onMouseEnter={() => setHighlighted(index)}
                                        onClick={() => onPick(option.key)}
                                        className={cn(
                                            'w-full rounded px-2 py-1.5 text-left text-sm',
                                            index === highlighted &&
                                                'bg-accent text-accent-foreground',
                                        )}
                                    >
                                        {option.label}
                                    </button>
                                </li>
                            ))
                        )}
                    </ul>
                )}
            </div>

            <p id={`${id}-hint`} className="text-xs text-muted-foreground">
                {hint}
            </p>
        </div>
    );
};

const EditBookDialog: React.FC<EditBookDialogProps> = ({ open, onClose, book, onBookUpdated }) => {
    const { t } = useTranslation();
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [success, setSuccess] = useState(false);

    // Form fields
    const [title, setTitle] = useState('');
    const [annotation, setAnnotation] = useState('');
    const [docdate, setDocdate] = useState('');
    const [lang, setLang] = useState('');

    // Authors and series
    const [authors, setAuthors] = useState<Author[]>([]);
    const [series, setSeries] = useState<Series[]>([]);

    // Available authors and series for autocomplete
    const [availableAuthors, setAvailableAuthors] = useState<Author[]>([]);
    const [availableSeries, setAvailableSeries] = useState<Series[]>([]);
    const [authorsQuery, setAuthorsQuery] = useState('');
    const [seriesQuery, setSeriesQuery] = useState('');
    const [authorsLoading, setAuthorsLoading] = useState(false);
    const [seriesLoading, setSeriesLoading] = useState(false);
    const [coverFile, setCoverFile] = useState<File | null>(null);
    const [coverUploading, setCoverUploading] = useState(false);
    const [coverUploadSuccess, setCoverUploadSuccess] = useState(false);
    const [coverUploadError, setCoverUploadError] = useState<string | null>(null);

    // Validation errors
    const [titleError, setTitleError] = useState('');

    useEffect(() => {
        if (book && open) {
            setTitle(book.title || '');
            setAnnotation(book.annotation || '');
            setDocdate(book.docdate || '');
            setLang(book.lang || '');
            setAuthors(book.authors || []);
            setSeries(book.series || []);
            setAuthorsQuery('');
            setSeriesQuery('');
            setCoverFile(null);
            setCoverUploading(false);
            setCoverUploadSuccess(false);
            setCoverUploadError(null);
            setError(null);
            setSuccess(false);
            setTitleError('');
        }
    }, [book, open]);

    const handleClose = () => {
        if (!loading) {
            setTitle('');
            setAnnotation('');
            setDocdate('');
            setLang('');
            setAuthors([]);
            setSeries([]);
            setAuthorsQuery('');
            setSeriesQuery('');
            setCoverFile(null);
            setCoverUploading(false);
            setCoverUploadSuccess(false);
            setCoverUploadError(null);
            setError(null);
            setSuccess(false);
            setTitleError('');
            onClose();
        }
    };

    useEffect(() => {
        if (!open) return;
        const query = authorsQuery.trim();
        if (query.length < MIN_QUERY_LENGTH) {
            setAvailableAuthors([]);
            setAuthorsLoading(false);
            return;
        }

        let active = true;
        setAuthorsLoading(true);
        const timer = setTimeout(async () => {
            try {
                const data = await adminApi.searchAuthors(query);
                if (!active) return;
                setAvailableAuthors(data?.authors || []);
            } catch {
                if (!active) return;
                setAvailableAuthors([]);
            } finally {
                if (active) {
                    setAuthorsLoading(false);
                }
            }
        }, DEBOUNCE_MS);

        return () => {
            active = false;
            clearTimeout(timer);
        };
    }, [authorsQuery, open]);

    useEffect(() => {
        if (!open) return;
        const query = seriesQuery.trim();
        if (query.length < MIN_QUERY_LENGTH) {
            setAvailableSeries([]);
            setSeriesLoading(false);
            return;
        }

        let active = true;
        setSeriesLoading(true);
        const timer = setTimeout(async () => {
            try {
                const data = await adminApi.searchSeries(query);
                if (!active) return;
                setAvailableSeries(data?.series || []);
            } catch {
                if (!active) return;
                setAvailableSeries([]);
            } finally {
                if (active) {
                    setSeriesLoading(false);
                }
            }
        }, DEBOUNCE_MS);

        return () => {
            active = false;
            clearTimeout(timer);
        };
    }, [seriesQuery, open]);

    const normalizeAuthors = (values: Array<Author | string>): Author[] => {
        const next: Author[] = [];
        const seenIDs = new Set<number>();
        const seenNames = new Set<string>();

        values.forEach((value) => {
            if (typeof value === 'string') {
                const name = value.trim();
                if (!name) return;
                const normalized = name.toLowerCase();
                if (seenNames.has(normalized)) return;
                next.push({ id: 0, full_name: name });
                seenNames.add(normalized);
                return;
            }

            if (value.id && seenIDs.has(value.id)) return;
            const name = value.full_name?.trim();
            if (value.id) {
                seenIDs.add(value.id);
            } else if (name) {
                const normalized = name.toLowerCase();
                if (seenNames.has(normalized)) return;
                seenNames.add(normalized);
            }
            next.push(value);
        });

        return next;
    };

    const normalizeSeries = (values: Array<Series | string>): Series[] => {
        const next: Series[] = [];
        const seenIDs = new Set<number>();
        const seenNames = new Set<string>();

        values.forEach((value) => {
            if (typeof value === 'string') {
                const name = value.trim();
                if (!name) return;
                const normalized = name.toLowerCase();
                if (seenNames.has(normalized)) return;
                next.push({ id: 0, ser: name, ser_no: 0 });
                seenNames.add(normalized);
                return;
            }

            if (value.id && seenIDs.has(value.id)) return;
            const name = value.ser?.trim();
            if (value.id) {
                seenIDs.add(value.id);
            } else if (name) {
                const normalized = name.toLowerCase();
                if (seenNames.has(normalized)) return;
                seenNames.add(normalized);
            }
            next.push(value);
        });

        return next;
    };

    const updateSeriesNumber = (seriesID: number, seriesName: string, rawValue: string) => {
        const trimmed = rawValue.trim();
        let nextValue = 0;
        if (trimmed !== '') {
            const parsed = Number(trimmed);
            if (!Number.isNaN(parsed)) {
                nextValue = Math.max(0, Math.trunc(parsed));
            }
        }

        setSeries((prev) =>
            prev.map((entry) =>
                entry.id === seriesID && entry.ser === seriesName
                    ? {
                          ...entry,
                          ser_no: nextValue,
                      }
                    : entry,
            ),
        );
    };

    const validateForm = (): boolean => {
        let isValid = true;

        if (!title.trim()) {
            setTitleError(t('titleRequired'));
            isValid = false;
        } else {
            setTitleError('');
        }

        return isValid;
    };

    const handleSave = async () => {
        if (!book) return;

        if (!validateForm()) {
            return;
        }

        setLoading(true);
        setError(null);
        setSuccess(false);

        try {
            const updatedBook = {
                ...book,
                title: title.trim(),
                annotation: annotation.trim(),
                docdate: docdate.trim(),
                lang: lang.trim(),
                authors: authors,
                series: series,
            };

            const data = await adminApi.saveBook(book.id, updatedBook);

            setSuccess(true);
            // Backend returns data in format: { result: Book, error: null }
            onBookUpdated((data.result ?? data) as Book);

            // Close dialog after a short delay to show success message
            setTimeout(() => {
                handleClose();
            }, 1000);
        } catch (err) {
            console.error('Error updating book:', err);
            const detail = isApiError(err)
                ? (err.body as { detail?: string } | undefined)?.detail
                : undefined;
            setError(detail || t('errorUpdatingBook'));
        } finally {
            setLoading(false);
        }
    };

    const handleCoverUpload = async () => {
        if (!book || !coverFile) return;

        setCoverUploading(true);
        setCoverUploadSuccess(false);
        setCoverUploadError(null);

        try {
            const formData = new FormData();
            formData.append('cover', coverFile);
            const data = await adminApi.uploadBookCover(book.id, formData);

            setCoverUploadSuccess(true);
            onBookUpdated((data.result ?? data) as Book);
            setCoverFile(null);
        } catch (err) {
            const detail = isApiError(err)
                ? (err.body as { detail?: string } | undefined)?.detail
                : undefined;
            setCoverUploadError(detail || t('coverUploadError'));
        } finally {
            setCoverUploading(false);
        }
    };

    if (!book) return null;

    // Whatever is already on the book is not offered again.
    const chosenAuthorKeys = new Set(authors.map(authorKey));
    const authorOptions = availableAuthors
        .filter((candidate) => !chosenAuthorKeys.has(authorKey(candidate)))
        .map((candidate) => ({ key: authorKey(candidate), label: candidate.full_name }));

    const chosenSeriesKeys = new Set(series.map(seriesKey));
    const seriesOptions = availableSeries
        .filter((candidate) => !chosenSeriesKeys.has(seriesKey(candidate)))
        .map((candidate) => ({ key: seriesKey(candidate), label: candidate.ser }));

    return (
        <Dialog open={open} onOpenChange={(next) => !next && handleClose()}>
            <DialogContent
                closeLabel={t('close')}
                // Left alone, focus lands on the close button in the corner.
                onOpenAutoFocus={(event) => {
                    event.preventDefault();
                    document.getElementById(TITLE_FIELD_ID)?.focus();
                }}
                className="flex max-h-[90vh] flex-col gap-0 p-0 sm:max-w-2xl"
            >
                <DialogHeader className="border-b border-border px-6 py-4 pr-12">
                    <DialogTitle>{t('editBook')}</DialogTitle>
                    <DialogDescription className="sr-only">{t('editBookHint')}</DialogDescription>
                </DialogHeader>

                <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4 scrollbar-thin">
                    {error && (
                        <Alert variant="destructive" className="mb-4 pr-10">
                            <AlertDescription>{error}</AlertDescription>
                            <DismissButton label={t('close')} onClick={() => setError(null)} />
                        </Alert>
                    )}

                    {success && (
                        <Alert className="mb-4 border-green-600/40 dark:border-green-400/40">
                            <AlertDescription className="text-green-600 dark:text-green-400">
                                {t('bookUpdatedSuccessfully')}
                            </AlertDescription>
                        </Alert>
                    )}

                    {coverUploadSuccess && (
                        <Alert className="mb-4 border-green-600/40 pr-10 dark:border-green-400/40">
                            <AlertDescription className="text-green-600 dark:text-green-400">
                                {t('coverUploadSuccess')}
                            </AlertDescription>
                            <DismissButton
                                label={t('close')}
                                onClick={() => setCoverUploadSuccess(false)}
                            />
                        </Alert>
                    )}

                    {coverUploadError && (
                        <Alert variant="destructive" className="mb-4 pr-10">
                            <AlertDescription>{coverUploadError}</AlertDescription>
                            <DismissButton
                                label={t('close')}
                                onClick={() => setCoverUploadError(null)}
                            />
                        </Alert>
                    )}

                    <div className="flex flex-col gap-4">
                        <Field id={TITLE_FIELD_ID} label={t('title')} error={titleError}>
                            <Input
                                id={TITLE_FIELD_ID}
                                type="text"
                                required
                                value={title}
                                disabled={loading}
                                aria-invalid={Boolean(titleError)}
                                aria-describedby={titleError ? `${TITLE_FIELD_ID}-hint` : undefined}
                                onChange={(event) => setTitle(event.target.value)}
                            />
                        </Field>

                        <Field id="edit-book-annotation" label={t('annotation')}>
                            {/*
                              A bare textarea: Tailwind runs without preflight, so
                              it needs the input's own frame spelled out rather
                              than inherited.
                            */}
                            <textarea
                                id="edit-book-annotation"
                                rows={6}
                                value={annotation}
                                disabled={loading}
                                onChange={(event) => setAnnotation(event.target.value)}
                                className={cn(
                                    'w-full min-w-0 rounded-md border border-input bg-transparent px-3 py-2 text-base shadow-xs outline-none transition-[color,box-shadow] md:text-sm dark:bg-input/30',
                                    'placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50',
                                    'focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50',
                                    'scrollbar-thin',
                                )}
                            />
                        </Field>

                        <Field
                            id="edit-book-docdate"
                            label={t('publicationDate')}
                            hint={t('dateFormatHint')}
                        >
                            <Input
                                id="edit-book-docdate"
                                type="text"
                                placeholder="YYYY-MM-DD"
                                value={docdate}
                                disabled={loading}
                                aria-describedby="edit-book-docdate-hint"
                                onChange={(event) => setDocdate(event.target.value)}
                            />
                        </Field>

                        <Field
                            id="edit-book-lang"
                            label={t('language')}
                            hint={t('languageCodeHint')}
                        >
                            <Input
                                id="edit-book-lang"
                                type="text"
                                placeholder="en, ru, uk, etc."
                                value={lang}
                                disabled={loading}
                                aria-describedby="edit-book-lang-hint"
                                onChange={(event) => setLang(event.target.value)}
                            />
                        </Field>

                        <TokenField
                            id="edit-book-authors"
                            label={t('authors')}
                            placeholder={t('authorsSearchOrCreate')}
                            hint={t('authorsEditHint')}
                            removeLabel={(name) => t('removeItem', { name })}
                            tokens={authors.map((author) => ({
                                key: authorKey(author),
                                label: author.full_name,
                            }))}
                            options={authorOptions}
                            query={authorsQuery}
                            loading={authorsLoading}
                            disabled={loading}
                            onQueryChange={setAuthorsQuery}
                            onPick={(key) => {
                                const picked = availableAuthors.find(
                                    (candidate) => authorKey(candidate) === key,
                                );
                                if (picked) setAuthors(normalizeAuthors([...authors, picked]));
                            }}
                            onCreate={(text) => setAuthors(normalizeAuthors([...authors, text]))}
                            onRemove={(key) =>
                                setAuthors(authors.filter((author) => authorKey(author) !== key))
                            }
                        />

                        <TokenField
                            id="edit-book-series"
                            label={t('series')}
                            placeholder={t('seriesSearchOrCreate')}
                            hint={t('seriesEditHint')}
                            removeLabel={(name) => t('removeItem', { name })}
                            tokens={series.map((entry) => ({
                                key: seriesKey(entry),
                                label: `${entry.ser}${entry.ser_no ? ' #' + entry.ser_no : ''}`,
                            }))}
                            options={seriesOptions}
                            query={seriesQuery}
                            loading={seriesLoading}
                            disabled={loading}
                            onQueryChange={setSeriesQuery}
                            onPick={(key) => {
                                const picked = availableSeries.find(
                                    (candidate) => seriesKey(candidate) === key,
                                );
                                if (picked) setSeries(normalizeSeries([...series, picked]));
                            }}
                            onCreate={(text) => setSeries(normalizeSeries([...series, text]))}
                            onRemove={(key) =>
                                setSeries(series.filter((entry) => seriesKey(entry) !== key))
                            }
                        />

                        {series.length > 0 && (
                            <div className="flex flex-col gap-2">
                                <p className="text-xs text-muted-foreground">
                                    {t('seriesNumberHint')}
                                </p>
                                {series.map((entry) => {
                                    const numberId = `series-number-${entry.id}-${entry.ser}`;
                                    return (
                                        <Field
                                            key={numberId}
                                            id={numberId}
                                            label={`${entry.ser} ${t('seriesNumber')}`}
                                        >
                                            <Input
                                                id={numberId}
                                                type="number"
                                                min={0}
                                                value={entry.ser_no ? entry.ser_no : ''}
                                                disabled={loading}
                                                onChange={(event) =>
                                                    updateSeriesNumber(
                                                        entry.id,
                                                        entry.ser,
                                                        event.target.value,
                                                    )
                                                }
                                            />
                                        </Field>
                                    );
                                })}
                            </div>
                        )}

                        <div className="flex flex-col gap-2">
                            <p className="text-xs font-medium text-muted-foreground">
                                {t('cover')}
                            </p>
                            <p className="text-xs text-muted-foreground">{t('coverUploadHint')}</p>
                            <div className="flex flex-wrap items-center gap-2">
                                {/*
                                  The file input keeps its own semantics — it is
                                  what the label names and what a screen reader
                                  reaches — and is only hidden from sight, so the
                                  browser's own "no file chosen" text is replaced
                                  by the name below.
                                */}
                                <input
                                    id="edit-book-cover"
                                    type="file"
                                    accept="image/*"
                                    disabled={loading || coverUploading}
                                    onChange={(event) => {
                                        const file = event.target.files?.[0] || null;
                                        setCoverFile(file);
                                        setCoverUploadSuccess(false);
                                        setCoverUploadError(null);
                                    }}
                                    className="peer sr-only"
                                />
                                {/* After the input, so peer-focus-visible can reach
                                    it: the ring belongs on the label a keyboard
                                    user can see, not on the hidden control. */}
                                <label
                                    htmlFor="edit-book-cover"
                                    className={cn(
                                        buttonVariants({ variant: 'outline', size: 'sm' }),
                                        'cursor-pointer peer-focus-visible:border-ring peer-focus-visible:ring-[3px] peer-focus-visible:ring-ring/50',
                                        (loading || coverUploading) &&
                                            'pointer-events-none opacity-50',
                                    )}
                                >
                                    {t('chooseCover')}
                                </label>
                                <span className="text-sm text-muted-foreground">
                                    {coverFile ? coverFile.name : t('noCoverSelected')}
                                </span>
                                <Button
                                    size="sm"
                                    disabled={!coverFile || loading || coverUploading}
                                    onClick={handleCoverUpload}
                                >
                                    {coverUploading && <BouncingDots />}
                                    {coverUploading ? t('uploading') : t('uploadCover')}
                                </Button>
                            </div>
                        </div>

                        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-1 rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">
                            <dt className="font-semibold">{t('bookId')}:</dt>
                            <dd className="min-w-0 truncate">{book.id}</dd>
                            <dt className="font-semibold">{t('filename')}:</dt>
                            <dd className="min-w-0 truncate">{book.filename}</dd>
                            <dt className="font-semibold">{t('format')}:</dt>
                            <dd className="min-w-0 truncate">{book.format}</dd>
                            <dt className="font-semibold">{t('bookAdded')}:</dt>
                            <dd className="min-w-0 truncate">{book.registerdate}</dd>
                        </dl>
                    </div>
                </div>

                <DialogFooter className="border-t border-border px-6 py-4">
                    <Button variant="ghost" onClick={handleClose} disabled={loading}>
                        {t('cancel')}
                    </Button>
                    <Button onClick={handleSave} disabled={loading}>
                        {loading && <BouncingDots />}
                        {loading ? t('saving') : t('save')}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};

export default EditBookDialog;
