import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Check } from 'lucide-react';

import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from '@/shared/ui/dropdown-menu';
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from '@/shared/ui/drawer';
import { getLanguageDisplaySafe, languageMapping } from '@/shared/lib/languageUtils';
import { cn } from '@/shared/lib/utils';

/**
 * LanguageSwitcher offers the languages this library actually holds books in.
 *
 * That is not a two-item choice: a well stocked instance has forty or more, so
 * on a phone the list is a sheet from the bottom edge — the same shape the
 * profile uses — rather than a dropdown. A menu anchored to a button in the top
 * bar would open a column of forty rows the width of the button, each of them
 * smaller than a fingertip, over a page that is still scrolling behind it.
 *
 * The order is the server's, which is by how many books are in each language,
 * so the ones worth choosing are the ones in reach.
 */

type LanguageSwitcherProps = {
    languages: string[];
    selected: string | null | undefined;
    onSelect: (lang: string) => void;
    /** The bar is laid out by the header; this only follows its decision. */
    isMobile: boolean;
    /** A bar this narrow has room for a flag and a code, but not a name. */
    isVeryNarrow: boolean;
};

const LanguageSwitcher: React.FC<LanguageSwitcherProps> = ({
    languages,
    selected,
    onSelect,
    isMobile,
    isVeryNarrow,
}) => {
    const { t } = useTranslation();
    const [open, setOpen] = useState(false);

    // Only languages the interface knows how to name are offered.
    const supported = languages.filter((lang) => getLanguageDisplaySafe(lang) !== null);

    const fullLabel = (lang: string) => getLanguageDisplaySafe(lang) ?? lang.toUpperCase();

    const triggerLabel = (lang: string) => {
        if (!isVeryNarrow) {
            return fullLabel(lang);
        }
        const info = languageMapping[lang];
        return info ? `${info.flag} ${lang.toUpperCase()}` : lang.toUpperCase();
    };

    // A function returning the element rather than a component, which would be
    // a fresh type on every render and so remount the button each time.
    const trigger = (props: React.ComponentProps<'button'> = {}) => (
        <button
            type="button"
            aria-label={t('language')}
            className={cn(
                'flex items-center justify-center truncate rounded px-2 font-medium uppercase text-neutral-400',
                'hover:bg-white/5 hover:text-white',
                isMobile ? 'h-8 min-w-[50px] text-[0.7rem]' : 'h-12 max-w-[120px] text-sm',
            )}
            {...props}
        >
            {selected ? triggerLabel(selected) : t('language')}
        </button>
    );

    const choose = (lang: string) => {
        onSelect(lang);
        setOpen(false);
    };

    if (isMobile) {
        return (
            <>
                {trigger({ onClick: () => setOpen(true) })}
                <Drawer open={open} onOpenChange={setOpen}>
                    <DrawerContent className="max-h-[85vh]">
                        <DrawerTitle className="px-4 pb-1 text-base font-medium">
                            {t('language')}
                        </DrawerTitle>
                        <DrawerDescription className="sr-only">{t('language')}</DrawerDescription>
                        <ul className="scrollbar-thin overflow-y-auto p-0 pb-6">
                            {supported.map((lang) => (
                                <li key={lang}>
                                    <button
                                        type="button"
                                        onClick={() => choose(lang)}
                                        aria-current={selected === lang ? 'true' : undefined}
                                        className={cn(
                                            'flex min-h-12 w-full items-center justify-between gap-3 px-4 text-left text-base',
                                            'hover:bg-accent',
                                            // The tick marks the current one;
                                            // text-primary here would read as
                                            // dimmer, not chosen.
                                            selected === lang && 'font-medium',
                                        )}
                                    >
                                        {fullLabel(lang)}
                                        {selected === lang && <Check className="size-4 flex-none" />}
                                    </button>
                                </li>
                            ))}
                        </ul>
                    </DrawerContent>
                </Drawer>
            </>
        );
    }

    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>{trigger()}</DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="scrollbar-thin max-h-[70vh] overflow-y-auto">
                {supported.map((lang) => (
                    <DropdownMenuItem
                        key={lang}
                        onSelect={() => onSelect(lang)}
                        className={cn(selected === lang && 'bg-accent')}
                    >
                        {fullLabel(lang)}
                    </DropdownMenuItem>
                ))}
            </DropdownMenuContent>
        </DropdownMenu>
    );
};

export default LanguageSwitcher;
