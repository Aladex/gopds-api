import React from 'react';

/**
 * SkeletonCard stands in for a book card while the page loads.
 *
 * It mirrors the real card's frame and cover proportions so the list does not
 * jump when the books arrive.
 */
const SkeletonCard: React.FC = () => (
    <div
        aria-hidden="true"
        className="rounded border border-border bg-card p-4"
    >
        <div className="grid grid-cols-[104px_minmax(0,1fr)] gap-4">
            <div className="h-[150px] w-[104px] animate-pulse rounded-sm bg-muted" />
            <div className="flex min-w-0 flex-col gap-2.5 py-1">
                <div className="h-5 w-2/5 animate-pulse rounded bg-muted" />
                <div className="h-3 w-3/5 animate-pulse rounded bg-muted" />
                <div className="h-3 w-1/3 animate-pulse rounded bg-muted" />
                <div className="mt-1 h-3 w-full animate-pulse rounded bg-muted" />
                <div className="h-3 w-4/5 animate-pulse rounded bg-muted" />
            </div>
        </div>
    </div>
);

export default SkeletonCard;
