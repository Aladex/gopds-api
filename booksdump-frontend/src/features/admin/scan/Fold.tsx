import React from 'react';
import { ChevronRight } from 'lucide-react';

import { cn } from '@/shared/lib/utils';

/*
 * A fold of a stage panel: a native <details> with a chevron, a label and a
 * muted meta on the right. Its summary is at least 44 px tall so a thumb hits
 * it. The content stays in the document while closed, as <details> keeps it.
 *
 * defaultOpen is the attribute React writes: a fold opened by hand stays open
 * across renders, and a fold whose defaultOpen turns false (a fix scan that
 * ended) closes with it.
 */

interface FoldProps {
    label: React.ReactNode;
    meta?: React.ReactNode;
    defaultOpen?: boolean;
    /** flush: a full-width fold at the bottom of a panel, ruled on top instead of boxed. */
    flush?: boolean;
    className?: string;
    children: React.ReactNode;
}

const Fold: React.FC<FoldProps> = ({
    label,
    meta,
    defaultOpen = false,
    flush = false,
    className,
    children,
}) => (
    <details
        open={defaultOpen}
        className={cn(
            'group min-w-0',
            flush ? 'border-t' : 'rounded-lg border border-border/60',
            className,
        )}
    >
        <summary
            className={cn(
                'flex min-h-11 cursor-pointer list-none flex-wrap items-center gap-x-2.5 gap-y-0.5 py-2 [&::-webkit-details-marker]:hidden',
                flush ? 'px-4 text-muted-foreground sm:px-5' : 'px-3',
            )}
        >
            <ChevronRight
                aria-hidden="true"
                className="size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-90"
            />
            <span
                className={cn(
                    'min-w-0 flex-1 text-sm',
                    flush ? 'text-foreground sm:text-muted-foreground' : 'font-medium',
                )}
            >
                {label}
            </span>
            {meta !== undefined && meta !== null && (
                <span className="basis-full pl-6.5 text-xs text-muted-foreground tabular-nums sm:basis-auto sm:pl-0 sm:text-sm">
                    {meta}
                </span>
            )}
        </summary>
        <div className={cn('flex min-w-0 flex-col gap-3 pb-3', flush ? 'px-4 sm:px-5' : 'px-3')}>
            {children}
        </div>
    </details>
);

export default Fold;
