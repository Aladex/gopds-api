import React from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';

import type { Book } from '@/api/books';
import { Button } from '@/shared/ui/button';
import { cn } from '@/shared/lib/utils';
import { useMeasured } from '@/features/catalogue/hooks/useMeasured';

/**
 * GenreChips is a book's genres as a single row of chips, with «+N» for the
 * ones that do not fit; an opened card shows them all, wrapped.
 *
 * Which chips fit depends on the width the card gets and on how long each
 * genre's name is, so it is measured rather than guessed: every chip stays
 * in the row's flow, the row is clipped to one line, and the measurement
 * counts the chips that landed on the first line — leaving room at its end
 * for the «+N» itself. The chips past that count are hidden (still laid out,
 * so the next measurement sees them), and «+N» is placed right after the last
 * one shown. It opens the card, which is where the rest are.
 */

type GenreChipsProps = {
    genres: NonNullable<Book['genres']>;
    open: boolean;
    onOpen: () => void;
    onGo: () => void;
    /** Reports how many chips the closed row is hiding. */
    onHiddenChange: (hidden: number) => void;
};

/** The gap between two chips, and the room kept at a row's end for «+N». */
const GAP = 6;
const PLUS_ROOM = 40;

type Fit = { visible: number; plusLeft: number };

/**
 * How many chips fit on the first line with «+N» after them, and where «+N»
 * goes. All of them fit when none wrapped.
 */
export const measureChips = (row: HTMLElement): Fit => {
    const chips = Array.from(row.querySelectorAll<HTMLElement>('[data-chip]'));
    if (chips.length === 0) return { visible: 0, plusLeft: 0 };
    const top = chips[0].offsetTop;
    let visible = chips.findIndex((chip) => chip.offsetTop > top);
    if (visible === -1) return { visible: chips.length, plusLeft: 0 };
    const end = (index: number) => chips[index].offsetLeft + chips[index].offsetWidth;
    // One chip always shows, even if «+N» then has to sit over its end.
    while (visible > 1 && end(visible - 1) + GAP + PLUS_ROOM > row.clientWidth) visible -= 1;
    return {
        visible,
        plusLeft: Math.min(end(visible - 1) + GAP, Math.max(0, row.clientWidth - PLUS_ROOM)),
    };
};

const sameFit = (a: Fit, b: Fit) => a.visible === b.visible && a.plusLeft === b.plusLeft;

const GenreChips: React.FC<GenreChipsProps> = ({ genres, open, onOpen, onGo, onHiddenChange }) => {
    const { t } = useTranslation();
    const initial = React.useMemo<Fit>(() => ({ visible: genres.length, plusLeft: 0 }), [genres]);
    const [rowRef, fit] = useMeasured<HTMLDivElement, Fit>(measureChips, initial, !open, sameFit);
    const hidden = open ? 0 : genres.length - fit.visible;

    // The card offers «Подробнее» when the row is hiding chips.
    const closedHidden = genres.length - fit.visible;
    React.useEffect(() => onHiddenChange(closedHidden), [closedHidden, onHiddenChange]);

    return (
        <div
            ref={rowRef}
            data-clip="chips"
            className={cn(
                'relative flex min-w-0 flex-wrap gap-1.5',
                !open && 'max-h-[26px] overflow-hidden',
            )}
        >
            {genres.map((genre, index) => (
                <Link
                    key={genre.id}
                    data-chip={index}
                    to={`/books/find/genre/${genre.id}/1`}
                    onClick={onGo}
                    // Past the count, a chip keeps its place in the flow but is
                    // neither seen nor reached until the card is opened.
                    className={cn(
                        'inline-flex h-[26px] max-w-full items-center rounded-full bg-secondary px-2.5 text-xs text-foreground',
                        'hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-focus focus-visible:outline-none',
                        hidden > 0 && index >= fit.visible && 'invisible',
                    )}
                >
                    <span className="truncate">{t(genre.genre, genre.genre)}</span>
                </Link>
            ))}
            {hidden > 0 && (
                <Button
                    variant="outline"
                    size="xs"
                    onClick={onOpen}
                    aria-label={t('bookGenresMore', { count: hidden })}
                    style={{ left: fit.plusLeft }}
                    className="absolute top-0 h-[26px] rounded-full bg-card px-2 font-normal text-primary"
                >
                    +{hidden}
                </Button>
            )}
        </div>
    );
};

export default GenreChips;
