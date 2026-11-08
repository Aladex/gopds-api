import { useCallback, useEffect, useRef, useState } from 'react';

import * as booksApi from '@/api/books';
import type { Book, BookVariantRow } from '@/api/books';
import {
    forgetVariant,
    readRememberedVariant,
    rememberVariant,
} from '@/features/catalogue/variants/rememberedVariant';

/**
 * useCardVariant decides which of a card's files the card shows, and swaps it.
 *
 * Which file opens, first match wins: the list's own reason (a saved file, the
 * feed's new file) → that file; a choice the reader made here before → that
 * file; otherwise the list's file, the card's default. The card's files are
 * read once, when the selector opens — or on mount when the card must know
 * where it stands without the reader asking: a remembered file to open, or a
 * saved file whose place in the card the control names. A remembered file the
 * card no longer has is forgotten, and the card stays on the list's file.
 */
export function useCardVariant(book: Book): {
    shown: Book;
    rows: BookVariantRow[] | null;
    loading: boolean;
    open: () => void;
    select: (bookID: number) => void;
} {
    const key = book.variants?.key;
    const reason = book.variants?.reason;
    // Everything below belongs to one card as one list sent it: another page,
    // or another file listed for this card, starts over. The state carries the
    // identity it was made for, and is read as empty under any other.
    const identity = `${book.id}:${key ?? ''}`;
    const [state, setState] = useState<{
        identity: string;
        rows: BookVariantRow[] | null;
        loading: boolean;
        chosen: number | null;
    }>({ identity, rows: null, loading: false, chosen: null });
    const own = state.identity === identity;
    const rows = own ? state.rows : null;
    const loading = own && state.loading;
    const chosen = own ? state.chosen : null;
    const update = useCallback(
        (
            change: Partial<{
                rows: BookVariantRow[] | null;
                loading: boolean;
                chosen: number | null;
            }>,
        ) =>
            setState((previous) =>
                previous.identity === identity
                    ? { ...previous, ...change }
                    : { identity, rows: null, loading: false, chosen: null, ...change },
            ),
        [identity],
    );
    // One request per card and listed file, however often the selector opens.
    const asked = useRef<{ identity: string; rows: Promise<BookVariantRow[] | null> } | null>(null);

    const fetchRows = useCallback((): Promise<BookVariantRow[] | null> => {
        if (!key) return Promise.resolve(null);
        if (asked.current?.identity !== identity) {
            update({ loading: true });
            const request = booksApi
                .getBookVariants(book.id)
                .then((answer) => {
                    update({ rows: answer.variants });
                    return answer.variants;
                })
                .catch((error: unknown) => {
                    console.error('Error reading the variants of a book', error);
                    // A later open may try again.
                    if (asked.current?.rows === request) asked.current = null;
                    return null;
                })
                .finally(() => update({ loading: false }));
            asked.current = { identity, rows: request };
        }
        return asked.current.rows;
    }, [book.id, identity, key, update]);

    useEffect(() => {
        if (!key) return;
        if (reason) {
            // The list's file wins; its place in the card is worth reading.
            void fetchRows();
            return;
        }
        const remembered = readRememberedVariant(key);
        if (remembered === null || remembered === book.id) return;
        let current = true;
        void fetchRows().then((loaded) => {
            if (!current || !loaded) return;
            if (loaded.some((row) => row.id === remembered)) {
                update({ chosen: remembered });
            } else {
                forgetVariant(key);
            }
        });
        return () => {
            current = false;
        };
    }, [book.id, key, reason, fetchRows, update]);

    const open = useCallback(() => {
        void fetchRows();
    }, [fetchRows]);

    const select = useCallback(
        (bookID: number) => {
            if (!key) return;
            const row = rows?.find((candidate) => candidate.id === bookID);
            update({ chosen: bookID === book.id ? null : bookID });
            // The default is what the card opens on anyway: nothing to keep.
            if (row?.default) {
                forgetVariant(key);
            } else {
                rememberVariant(key, bookID);
            }
        },
        [book.id, key, rows, update],
    );

    const shown = (chosen !== null && rows?.find((row) => row.id === chosen)) || book;
    return { shown, rows, loading, open, select };
}
