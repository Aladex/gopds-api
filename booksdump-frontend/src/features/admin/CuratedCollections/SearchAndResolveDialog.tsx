import React, { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Search } from 'lucide-react';

import { Button } from '@/shared/ui/button';
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
import * as booksApi from '@/api/books';

interface SearchHit {
    id: number;
    title: string;
    authors?: { id: number; full_name: string }[];
}

// SearchAndResolveDialog opens an inline title search against /api/books/list
// so the admin can pick the right local book for a not_found / wrongly-resolved
// item without leaving the curated-collection page.
const SearchAndResolveDialog: React.FC<{
    open: boolean;
    initialQuery: string;
    onClose: () => void;
    onPick: (bookID: number) => Promise<void>;
}> = ({ open, initialQuery, onClose, onPick }) => {
    const { t } = useTranslation();
    const [query, setQuery] = useState(initialQuery);
    const [hits, setHits] = useState<SearchHit[]>([]);
    const [loading, setLoading] = useState(false);
    const [busy, setBusy] = useState(false);

    const runSearch = useCallback(async (term: string) => {
        const trimmed = term.trim();
        if (!trimmed) return;
        setLoading(true);
        try {
            const data = await booksApi.listBooks({ title: trimmed, limit: 20, offset: 0 });
            setHits(data?.books ?? []);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        if (!open) return;
        // Opening carries the item's own title across, and searches it straight
        // away: the admin came here to resolve that one item, so making them
        // press Search first would only cost a click.
        setQuery(initialQuery);
        runSearch(initialQuery);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open]);

    const pick = async (id: number) => {
        if (busy) return;
        setBusy(true);
        try {
            await onPick(id);
            onClose();
        } finally {
            setBusy(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
            <DialogContent
                closeLabel={t('close')}
                className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-2xl"
            >
                <DialogHeader className="border-b border-border px-6 py-4 pr-12">
                    <DialogTitle>
                        {t('curatedCollections.searchDialog.title', 'Find a book in the library')}
                    </DialogTitle>
                    <DialogDescription className="sr-only">
                        {t(
                            'curatedCollections.searchDialog.description',
                            'Search the library by title and pick the book this item should point at.',
                        )}
                    </DialogDescription>
                </DialogHeader>

                <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto px-6 py-4">
                    <div className="mb-4 flex items-end gap-2">
                        <Field
                            id="resolve-search-query"
                            label={t('curatedCollections.searchDialog.queryLabel', 'Title')}
                            className="flex-1"
                        >
                            <Input
                                id="resolve-search-query"
                                autoFocus
                                value={query}
                                onChange={(event) => setQuery(event.target.value)}
                                onKeyDown={(event) => {
                                    if (event.key === 'Enter') {
                                        event.preventDefault();
                                        runSearch(query);
                                    }
                                }}
                            />
                        </Field>
                        <Button
                            variant="outline"
                            onClick={() => runSearch(query)}
                            disabled={loading || !query.trim()}
                        >
                            <Search className="size-4" />
                            {t('curatedCollections.searchDialog.searchBtn', 'Search')}
                        </Button>
                    </div>

                    {loading && (
                        <p className="text-sm text-muted-foreground">
                            {t('curatedCollections.searchDialog.loading', 'Searching…')}
                        </p>
                    )}

                    {!loading && hits.length === 0 && (
                        <p className="text-sm text-muted-foreground">
                            {t('curatedCollections.searchDialog.empty', 'No matches yet')}
                        </p>
                    )}

                    {!loading && hits.length > 0 && (
                        <div className="flex flex-col gap-2">
                            {hits.map((hit) => (
                                <div
                                    key={hit.id}
                                    className="flex items-center justify-between gap-2 rounded-md border border-border p-2"
                                >
                                    {/* min-w-0 so the truncation below has
                                        something to shrink into: a flex item
                                        defaults to its content's width. */}
                                    <div className="min-w-0">
                                        <p className="truncate text-sm">{hit.title}</p>
                                        <p className="truncate text-xs text-muted-foreground">
                                            <span className="tabular-nums">#{hit.id}</span>
                                            {' · '}
                                            {(hit.authors ?? []).map((a) => a.full_name).join(', ')}
                                        </p>
                                    </div>
                                    <Button
                                        size="sm"
                                        disabled={busy}
                                        onClick={() => pick(hit.id)}
                                        aria-label={`${t('curatedCollections.resolve', 'Resolve')}: ${hit.title}`}
                                    >
                                        {t('curatedCollections.resolve', 'Resolve')}
                                    </Button>
                                </div>
                            ))}
                        </div>
                    )}
                </div>

                <DialogFooter className="border-t border-border px-6 py-4">
                    <Button variant="ghost" onClick={onClose}>
                        {t('close')}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};

export default SearchAndResolveDialog;
