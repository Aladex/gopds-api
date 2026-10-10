import React from 'react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/shared/lib/utils';

/**
 * The order of the list — two options, so a pair of buttons, in the same
 * quiet style as the interface language toggle. The newest-first order stays
 * the default; choosing "by author" is remembered in the address, so it
 * survives a reload and every page of the list.
 */
const BookSortToggle: React.FC<{ byAuthor: boolean; onChange: (byAuthor: boolean) => void }> = ({
    byAuthor,
    onChange,
}) => {
    const { t } = useTranslation();
    const options = [
        { key: 'sortNewest', active: !byAuthor, value: false },
        { key: 'sortByAuthor', active: byAuthor, value: true },
    ];
    return (
        <div
            role="group"
            aria-label={t('sortBy')}
            className="inline-flex items-center gap-1 text-xs"
        >
            {options.map((option) => (
                <button
                    key={option.key}
                    type="button"
                    onClick={() => onChange(option.value)}
                    aria-pressed={option.active}
                    className={cn(
                        'rounded px-2 py-1 transition-colors',
                        'outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50',
                        option.active
                            ? 'bg-accent font-medium text-accent-foreground'
                            : 'text-muted-foreground hover:text-foreground',
                    )}
                >
                    {t(option.key)}
                </button>
            ))}
        </div>
    );
};

export default BookSortToggle;
