import React from 'react';
import { useTranslation } from 'react-i18next';
import { Search } from 'lucide-react';

import { Button } from '@/shared/ui/button';

type EmptyListProps = {
    icon: React.ReactNode;
    title: React.ReactNode;
    /** What the reader can do about it, or why it is empty. */
    body: React.ReactNode;
    children?: React.ReactNode;
};

/** A list with nothing to show, saying why: the frame the author search's has. */
const EmptyList: React.FC<EmptyListProps> = ({ icon, title, body, children }) => (
    <div className="rounded border border-border bg-card px-5 py-8 text-center">
        <div className="mb-3 flex justify-center text-muted-foreground">{icon}</div>
        <h3 className="mt-0 mb-1.5 text-[17px] leading-[1.3] font-semibold">{title}</h3>
        <p className="mt-0 mb-0 text-sm text-muted-foreground">{body}</p>
        {children}
    </div>
);

type LanguageEmptyProps = {
    body: React.ReactNode;
    onShowAll: () => void;
    children?: React.ReactNode;
};

/**
 * Nothing found under a language: the filter may be all that stands in the way,
 * so the page says so and offers to take it off. Outlined, so it does not read as
 * the page's way to read something.
 */
export const LanguageEmpty: React.FC<LanguageEmptyProps> = ({ body, onShowAll, children }) => {
    const { t } = useTranslation();
    return (
        <EmptyList
            icon={<Search aria-hidden="true" className="size-6" />}
            title={t('empty.nothing')}
            body={body}
        >
            <div className="mt-4 flex flex-wrap justify-center gap-2">
                <Button variant="outline" onClick={onShowAll} className="h-11 sm:h-11">
                    {t('empty.showAll')}
                </Button>
                {children}
            </div>
        </EmptyList>
    );
};

export default EmptyList;
