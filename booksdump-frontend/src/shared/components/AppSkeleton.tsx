import React from 'react';

/**
 * AppSkeleton stands in for the whole application while the session and the
 * language are still loading.
 *
 * It runs before i18n exists, so it can carry no text — which makes its shape
 * the only thing it can say. It draws the header and a few book cards in the
 * proportions Header and SkeletonCard use, so the interface that replaces it
 * lands where the reader is already looking instead of shifting under them.
 *
 * The breakpoints are Tailwind's rather than Header's own media queries: the
 * skeleton has no state to consult and a placeholder does not need the exact
 * pixel where the bar changes height.
 */

const CARD_COUNT = 4;

/** Matches SkeletonCard's cover, so the two placeholders agree. */
const COVER_WIDTH = 104;

/** Header is dark in both themes, so its placeholders are too. */
const barBlock = 'animate-pulse rounded bg-white/15';

const bodyBlock = 'animate-pulse rounded bg-muted';

const AppSkeleton: React.FC = () => (
    <div aria-hidden="true" className="min-h-screen animate-in fade-in duration-300 bg-background">
        {/* Header: logo and section links on the left, controls on the right. */}
        <div className="flex h-12 w-full items-center gap-4 bg-neutral-900 px-4 sm:h-16">
            <div className="size-6 flex-none animate-pulse rounded-full bg-white/15" />
            <div className="hidden items-center gap-6 sm:flex">
                <div className={`h-4 w-20 ${barBlock}`} />
                <div className={`h-4 w-24 ${barBlock}`} />
                <div className={`h-4 w-20 ${barBlock}`} />
            </div>
            <div className="flex-1" />
            <div className="flex items-center gap-2">
                <div className={`h-4 w-14 ${barBlock}`} />
                <div className="size-5 animate-pulse rounded-full bg-white/15" />
                <div className={`hidden h-4 w-20 sm:block ${barBlock}`} />
            </div>
        </div>

        {/* The catalogue is the first screen after loading, so it is what the
            placeholder draws — the same card frame SkeletonCard uses. */}
        <div className="flex flex-col p-4">
            {Array.from({ length: CARD_COUNT }).map((_, index) => (
                <div key={index} className="mx-auto w-full max-w-[1200px] py-1.5">
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
