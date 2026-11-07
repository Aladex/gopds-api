import React from 'react';

import { CONTENT_COLUMN } from '@/shared/layout/breakpoints';

/**
 * AppSkeleton stands in for the whole application while the session and the
 * language are still loading.
 *
 * It runs before i18n exists, so it can carry no text — which makes its shape
 * the only thing it can say. It draws the header and a few book cards in the
 * proportions Header and SkeletonCard use, so the interface that replaces it
 * lands where the reader is already looking instead of shifting under them.
 *
 * It runs before any hook can ask the width, so the 601px boundary is written in
 * the stylesheet, at the pixel where Header changes shape.
 */

const CARD_COUNT = 4;

/** Matches SkeletonCard's cover, so the two placeholders agree. */
const COVER_WIDTH = 104;

/** Header is dark in both themes, so its placeholders are too. */
const barBlock = 'animate-pulse rounded bg-chrome-foreground/15';

const bodyBlock = 'animate-pulse rounded bg-muted';

const AppSkeleton: React.FC = () => (
    <div aria-hidden="true" className="min-h-screen animate-in fade-in duration-300 bg-background">
        {/* The header: two storeys on a phone, one row above it. */}
        <div className="w-full bg-chrome min-[601px]:px-4">
            <div
                data-testid="skeleton-column"
                className={`${CONTENT_COLUMN} flex flex-col min-[601px]:h-16 min-[601px]:flex-row min-[601px]:items-center min-[601px]:gap-4`}
            >
                <div className="flex h-10 flex-none items-center justify-between gap-4 px-2 min-[601px]:contents">
                    <div className="size-6 flex-none animate-pulse rounded-full bg-chrome-foreground/15" />
                    <div className="flex items-center gap-2 min-[601px]:order-last">
                        <div className={`h-4 w-14 ${barBlock}`} />
                        <div className="size-5 animate-pulse rounded-full bg-chrome-foreground/15" />
                    </div>
                </div>
                <div className="flex h-11 flex-none items-center px-2 min-[601px]:h-9 min-[601px]:w-[560px] min-[601px]:max-w-[60%] min-[601px]:px-0">
                    <div className={`h-9 w-full ${barBlock}`} />
                </div>
                <div className="hidden flex-1 min-[601px]:block" />
            </div>
        </div>

        {/* The section row, its bottom border outside the row's own height. */}
        <div className="h-[41px] border-b border-border bg-card px-4 min-[601px]:h-[45px]">
            <div
                data-testid="skeleton-column"
                className={`${CONTENT_COLUMN} flex h-full items-center gap-6`}
            >
                <div className={`h-4 w-14 ${bodyBlock}`} />
                <div className={`h-4 w-20 ${bodyBlock}`} />
                <div className={`h-4 w-20 ${bodyBlock}`} />
            </div>
        </div>

        {/* The catalogue is the first screen after loading, so it is what the
            placeholder draws — the same card frame SkeletonCard uses. The top is
            the room ListLine takes: 8px on a phone, 40px from 601 up. */}
        <div data-testid="skeleton-list" className="flex flex-col px-4 pt-2 pb-4 min-[601px]:pt-10">
            {Array.from({ length: CARD_COUNT }).map((_, index) => (
                <div key={index} className={`${CONTENT_COLUMN} py-1.5`}>
                    <div className="rounded border border-border bg-card p-4">
                        <div
                            className="grid gap-4"
                            style={{ gridTemplateColumns: `${COVER_WIDTH}px minmax(0, 1fr)` }}
                        >
                            <div className="flex flex-col gap-2">
                                <div
                                    style={{ width: COVER_WIDTH }}
                                    className={`h-[150px] rounded-sm ${bodyBlock}`}
                                />
                                {/* Four download formats in two columns. */}
                                <div className="grid grid-cols-2 gap-1">
                                    <div className={`h-4 ${bodyBlock}`} />
                                    <div className={`h-4 ${bodyBlock}`} />
                                    <div className={`h-4 ${bodyBlock}`} />
                                    <div className={`h-4 ${bodyBlock}`} />
                                </div>
                            </div>
                            <div className="flex min-w-0 flex-col gap-2.5 py-1">
                                <div className={`h-5 w-2/5 ${bodyBlock}`} />
                                <div className={`h-3 w-3/5 ${bodyBlock}`} />
                                <div className={`h-3 w-1/3 ${bodyBlock}`} />
                                <div className={`mt-1 h-3 w-full ${bodyBlock}`} />
                                <div className={`h-3 w-4/5 ${bodyBlock}`} />
                            </div>
                        </div>
                        {/* The card's action row, ruled off the same way. */}
                        <div className="mt-2 flex justify-end gap-1 border-t border-border pt-2.5">
                            <div className={`size-6 ${bodyBlock}`} />
                        </div>
                    </div>
                </div>
            ))}
        </div>
    </div>
);

export default AppSkeleton;
