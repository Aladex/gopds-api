import React from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';

import type { AuthorCardData, AuthorCover } from '@/api/books';
import { authorPath } from '@/features/catalogue/addresses';
import { authorInitial, isUnexplained, nameParts } from '@/features/catalogue/authorMatch';
import { authorTitle, namesakeLabel } from '@/shared/lib/authorTitle';
import { thumbURL } from '@/shared/lib/coverUrl';
import { isPlainClick } from '@/shared/lib/isPlainClick';
import { cn } from '@/shared/lib/utils';
import { Badge } from '@/shared/ui/badge';

/**
 * AuthorCard is one author of the search page: a pile of their covers beside
 * their name, so a reader tells two namesakes apart before reading either.
 *
 * Every height here is fixed by the frames below and none by the data, which is
 * what lets the skeleton stand exactly where the card will: a long name takes a
 * second line and the last line gives way, never the row. The summary fields
 * may all be missing, so every one of them is optional at each step.
 */

/** The card's box, with the sizes the pile is measured in. */
export const CARD_FRAME = cn(
    'box-border grid h-[104px] grid-cols-[var(--pile-w)_minmax(0,1fr)] gap-x-3 overflow-hidden',
    'rounded border border-border bg-card px-3 py-[9px]',
    '[--cover-h:86px] [--cover-w:60px] [--pile-off:13px] [--pile-w:86px]',
    'card:h-[124px] card:gap-x-3.5 card:px-4 card:py-[11px]',
    'card:[--cover-h:100px] card:[--cover-w:70px] card:[--pile-off:20px] card:[--pile-w:112px]',
    'max-[359px]:gap-x-2.5',
    'max-[359px]:[--cover-h:76px] max-[359px]:[--cover-w:52px] max-[359px]:[--pile-off:9px] max-[359px]:[--pile-w:70px]',
);

/** The pile's slot. It keeps its width with one cover, or with none. */
export const PILE_FRAME = 'relative isolate block h-(--cover-h)';

/** The text column: centred, and cut at the bottom when it runs over. */
export const TEXT_FRAME = cn(
    'flex h-[76px] min-w-0 flex-col justify-center-safe self-center overflow-hidden *:shrink-0',
    'card:h-[100px] card:gap-y-[3px] max-[359px]:h-[74px]',
);

const COVER_FRAME = 'absolute box-border rounded';

/** Where each cover of the pile lies, top first. */
const SLOTS = [
    'top-0 left-0 z-3 h-(--cover-h) w-(--cover-w) shadow-[0_0_0_1.5px_var(--card),0_2px_5px_rgba(0,0,0,.18)]',
    'top-[4%] left-(--pile-off) z-2 h-[92%] w-[calc(var(--cover-w)*.92)] shadow-[0_0_0_1.5px_var(--card)]',
    'top-[8%] left-[calc(var(--pile-off)*2)] z-1 h-[84%] w-[calc(var(--cover-w)*.84)] shadow-[0_0_0_1.5px_var(--card)]',
];

const genrePhrase = (genres: string[]): string =>
    genres
        .slice(0, 2)
        .map((genre, index) =>
            // An acronym stays as it is: «SF» is not «sF».
            index === 0 || genre.slice(0, 2) === genre.slice(0, 2).toLocaleUpperCase()
                ? genre
                : genre[0].toLocaleLowerCase() + genre.slice(1),
        )
        .join(', ');

/**
 * AuthorPile draws what is left of the covers: a cover that fails drops out and
 * the rest close over it, down to the letter when none is left. /books-thumbs/
 * answers 404 for a missing or broken cover, which is what makes that visible.
 */
const AuthorPile: React.FC<{ covers: AuthorCover[]; name: string }> = ({ covers, name }) => {
    const [failed, setFailed] = React.useState<ReadonlySet<string>>(new Set());

    const shown = covers
        .map(thumbURL)
        .filter((url) => !failed.has(url))
        .slice(0, SLOTS.length);

    return (
        <span data-testid="author-pile" className={PILE_FRAME}>
            {shown.length === 0 ? (
                <span
                    aria-hidden="true"
                    data-testid="author-tile"
                    className={cn(
                        COVER_FRAME,
                        SLOTS[0],
                        'flex items-center justify-center overflow-hidden border-t-[3px] border-primary bg-secondary text-[22px] leading-none font-semibold text-muted-foreground',
                    )}
                >
                    {authorInitial(name)}
                </span>
            ) : (
                shown.map((url, index) => (
                    <img
                        key={url}
                        src={url}
                        // The titles are on the card's last line.
                        alt=""
                        loading="lazy"
                        data-slot={index}
                        onError={() => setFailed((before) => new Set(before).add(url))}
                        className={cn(COVER_FRAME, SLOTS[index], 'bg-muted object-cover')}
                    />
                ))
            )}
        </span>
    );
};

const AuthorName: React.FC<{ name: string; query: string }> = ({ name, query }) => {
    const parts = nameParts(name, query);
    if (!parts) {
        return name;
    }
    return (
        <>
            {parts.before}
            <span data-hit="" className="font-normal text-muted-foreground">
                {parts.hit}
            </span>
            {parts.after}
        </>
    );
};

export interface AuthorCardProps {
    author: AuthorCardData;
    /** What was searched for, which the name and the note are read against. */
    query: string;
    /** Called as the card is followed, with the name the author's page goes by. */
    onGo: (scopeName: string) => void;
}

const AuthorCard: React.FC<AuthorCardProps> = ({ author, query, onGo }) => {
    const { t } = useTranslation();
    const genres = author.genres ?? [];
    const works = (author.works ?? []).join(' · ');
    const label = namesakeLabel(author, t);
    const count = author.books_count;

    // The note outranks the titles: it answers why this row is here at all.
    const note = isUnexplained(author.full_name, query)
        ? author.matched_name
            ? t('authorSearch.alsoIn', { name: author.matched_name })
            : t('authorSearch.similarName')
        : '';

    return (
        <Link
            to={authorPath(author.id)}
            onClick={(event) => {
                if (isPlainClick(event)) {
                    onGo(authorTitle(author, t));
                }
            }}
            className={cn(
                CARD_FRAME,
                'text-left no-underline transition-colors hover:border-muted-foreground hover:no-underline',
                'focus-visible:ring-[3px] focus-visible:ring-focus focus-visible:outline-none',
            )}
        >
            <AuthorPile covers={author.covers ?? []} name={author.full_name} />
            <span data-testid="author-text" className={TEXT_FRAME}>
                <span
                    data-testid="author-name"
                    className={cn(
                        'line-clamp-2 text-base leading-[1.25] text-foreground [overflow-wrap:anywhere]',
                        'card:text-[17px] max-[359px]:text-[15px]',
                        // A one-book author recedes beside the 465-book one.
                        (count ?? 0) <= 1 ? 'font-medium' : 'font-semibold',
                    )}
                >
                    <AuthorName name={author.full_name} query={query} />
                    {author.kind === 'collective' && (
                        <Badge
                            variant="secondary"
                            className="ml-1.5 rounded-sm px-1.5 py-0 align-[1px] text-[11px] leading-[18px] font-semibold text-muted-foreground uppercase"
                        >
                            {t('authorSearch.coauthors')}
                        </Badge>
                    )}
                    {/* An entity with a namesake says which one it is. */}
                    {label && (
                        <span className="ml-1.5 text-sm font-normal text-muted-foreground">
                            · {label}
                        </span>
                    )}
                </span>
                {(count || genres.length > 0) && (
                    <span
                        data-testid="author-meta"
                        className="line-clamp-2 text-[13px] leading-[18px] text-muted-foreground card:line-clamp-1"
                    >
                        {count ? (
                            <span className="font-semibold text-foreground tabular-nums">
                                {t('bookCount', { count })}
                            </span>
                        ) : null}
                        {count && genres.length > 0 ? ' · ' : null}
                        {genrePhrase(genres)}
                    </span>
                )}
                {(note || works) && (
                    <span
                        data-testid="author-works"
                        className="truncate text-[12.5px] leading-[18px] text-muted-foreground"
                    >
                        {note && <span className="text-foreground">{note}</span>}
                        {note && works ? ' · ' : null}
                        {works}
                    </span>
                )}
            </span>
        </Link>
    );
};

/** AuthorCardSkeleton stands where a card will: the same frames, a full pile. */
export const AuthorCardSkeleton: React.FC = () => (
    <div aria-hidden="true" data-testid="author-card-skeleton" className={CARD_FRAME}>
        {/* It pulses as one: each slot on its own would darken where they overlap. */}
        <span data-testid="author-pile" className={cn(PILE_FRAME, 'animate-pulse')}>
            {SLOTS.map((slot) => (
                <span key={slot} className={cn(COVER_FRAME, slot, 'bg-muted')} />
            ))}
        </span>
        <span data-testid="author-text" className={TEXT_FRAME}>
            <span className="block h-5 w-[55%] animate-pulse rounded bg-muted" />
            <span className="mt-[5px] block h-3 w-3/4 animate-pulse rounded bg-muted" />
            <span className="mt-1.5 block h-[11px] w-[90%] animate-pulse rounded bg-muted" />
        </span>
    </div>
);

export default AuthorCard;
