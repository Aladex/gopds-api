import React from 'react';
import { useTranslation } from 'react-i18next';

import { namesakeLabel, shortName, type NamedAuthor } from '@/shared/lib/authorTitle';
import { cn } from '@/shared/lib/utils';

/**
 * NamesakeName is an author entity's name as the site writes it everywhere an
 * entity is named — the author search, the suggestions, the scope chip, a
 * person in a book's byline menu: the short name, then, for an entity with a
 * namesake, a muted «· label» that tells the two apart.
 */
const NamesakeName: React.FC<{
    author: NamedAuthor;
    /** The name as drawn, when the caller marks it up (a matched query); the short name by default. */
    children?: React.ReactNode;
    /**
     * Whether a book count may stand for the label. A row that ends with the
     * entity's count already (a suggestion) says it once.
     */
    countAsLabel?: boolean;
    /** The label's size, matched to the line it stands in. */
    labelClassName?: string;
}> = ({ author, children, countAsLabel = true, labelClassName }) => {
    const { t } = useTranslation();
    const label = countAsLabel ? namesakeLabel(author, t) : (author.label ?? '');
    return (
        <>
            {children ?? shortName(author)}
            {label && (
                <span
                    data-testid="namesake-label"
                    className={cn('font-normal text-muted-foreground', labelClassName)}
                >
                    {/* A real space, not a margin: the name reads «Имя Фамилия · жанр»
                        when copied or read aloud. */}
                    {' · '}
                    {label}
                </span>
            )}
        </>
    );
};

export default NamesakeName;
