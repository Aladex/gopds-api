import React from 'react';
import { useTranslation } from 'react-i18next';
import { Plus, X } from 'lucide-react';

import { cn } from '@/shared/lib/utils';
import { useCatalogueSearchContext } from '@/features/catalogue/CatalogueSearchContext';

/**
 * ScopeChip says which list a search is confined to.
 *
 * While the reader is on a scoped list it stays on screen either way — on, with
 * a cross to drop it, or off and offering itself back. It used to vanish once
 * released, leaving no way to confine the search again short of navigating out
 * and in.
 */
const ScopeChip: React.FC<{ className?: string }> = ({ className }) => {
    const { t } = useTranslation();
    const { scope, releaseScope } = useCatalogueSearchContext();

    if (scope.kind === null) {
        return null;
    }

    if (scope.active) {
        return (
            <span
                className={cn(
                    'inline-flex h-7 min-w-0 items-center rounded-full bg-accent pl-3 text-[13px] text-accent-foreground',
                    'min-[601px]:h-6 min-[601px]:text-xs',
                    className,
                )}
            >
                {/* A long label is cut on a narrow screen, so the whole of it
                    stays reachable. */}
                <span className="truncate" title={scope.label}>
                    {scope.label}
                </span>
                {/* The chip is 28px and the cross 40: it hangs over the chip's
                    edges, so the target is a thumb's and the shape stays small. */}
                <button
                    type="button"
                    onClick={releaseScope}
                    aria-label={t('searchEverywhere')}
                    title={t('searchEverywhere')}
                    className={cn(
                        '-my-1.5 flex size-10 flex-none items-center justify-center rounded-full outline-none',
                        'focus-visible:ring-[3px] focus-visible:ring-focus',
                        'min-[601px]:my-0 min-[601px]:size-6',
                    )}
                >
                    <X className="size-3.5" />
                </button>
            </span>
        );
    }

    return (
        <button
            type="button"
            onClick={scope.reclaim}
            aria-label={t('searchScopeRestore')}
            title={t('searchScopeRestore')}
            className={cn(
                'inline-flex h-10 min-w-0 items-center text-[13px] text-muted-foreground outline-none',
                'focus-visible:ring-[3px] focus-visible:ring-focus',
                'min-[601px]:h-6 min-[601px]:text-xs',
                className,
            )}
        >
            <span
                className={cn(
                    'inline-flex h-7 min-w-0 items-center gap-1 rounded-full border border-dashed border-border pr-3 pl-2',
                    'hover:border-solid hover:bg-accent hover:text-accent-foreground',
                    'min-[601px]:h-6',
                )}
            >
                <Plus className="size-3.5 flex-none" />
                <span className="truncate">{scope.label}</span>
            </span>
        </button>
    );
};

export default ScopeChip;
