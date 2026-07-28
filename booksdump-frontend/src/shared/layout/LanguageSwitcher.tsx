import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Check } from 'lucide-react';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from '@/shared/ui/drawer';
import { getLanguageDisplaySafe, languageMapping } from '@/shared/lib/languageUtils';
import { cn } from '@/shared/lib/utils';

/**
 * LanguageSwitcher offers the languages this library actually holds books in.
 *
 * It sets the catalogue filter and nothing else. It used to set the interface
 * locale too, so choosing Ukrainian books turned the interface English — the
 * two now live apart, and the interface has its own control.
 *
 * That is not a two-item choice: a well stocked instance has forty or more, so
 * the list is a window of its own — a sheet from the bottom edge on a phone, a
 * dialog on a desktop. It used to be a menu hanging off the button in the top
 * bar, which opened a column of forty rows the width of that button, over a
 * page still scrolling behind it.
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

    const choose = (lang: string) => {
        onSelect(lang);
        setOpen(false);
    };

    // The same list in both shells: which language is on offer, and which one
    // is in force, does not depend on the size of the screen.
    const list = (
        <ul className="scrollbar-thin overflow-y-auto p-0">
            {supported.map((lang) => (
                <li key={lang}>
                    <button
                        type="button"
                        onClick={() => choose(lang)}
                        aria-current={selected === lang ? 'true' : undefined}
                        className={cn(
                            'flex min-h-12 w-full items-center justify-between gap-3 rounded px-4 text-left text-base',
                            'sm:min-h-10 sm:px-3 sm:text-sm',
                            'hover:bg-accent',
                            // The dialog focuses its first row on opening, and
                            // the browser's own ring is the one blue thing left
                            // on the screen.
                            'outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50',
                            // The tick marks the current one; text-primary here
                            // would read as dimmer, not chosen.
                            selected === lang && 'font-medium',
                        )}
                    >
                        {fullLabel(lang)}
                        {selected === lang && <Check className="size-4 flex-none" />}
                    </button>
                </li>
            ))}
        </ul>
    );

    return (
        <>
            <button
                type="button"
                aria-label={t('booksLanguage')}
                onClick={() => setOpen(true)}
                className={cn(
                    'flex items-center justify-center truncate rounded px-2 font-medium uppercase text-neutral-400',
                    'hover:bg-white/5 hover:text-white',
                    isMobile ? 'h-8 min-w-[50px] text-[0.7rem]' : 'h-12 max-w-[120px] text-sm',
                )}
            >
                {selected ? triggerLabel(selected) : t('language')}
            </button>

            {isMobile ? (
                <Drawer open={open} onOpenChange={setOpen}>
                    <DrawerContent className="max-h-[85vh]">
                        <DrawerTitle className="px-4 pb-1 text-base font-medium">
                            {t('booksLanguage')}
                        </DrawerTitle>
                        <DrawerDescription className="sr-only">{t('booksLanguage')}</DrawerDescription>
                        <div className="overflow-y-auto pb-6">{list}</div>
                    </DrawerContent>
                </Drawer>
            ) : (
                <Dialog open={open} onOpenChange={setOpen}>
                    <DialogContent closeLabel={t('close')} className="sm:max-w-sm">
                        <DialogTitle>{t('booksLanguage')}</DialogTitle>
                        <DialogDescription className="sr-only">{t('booksLanguage')}</DialogDescription>
                        {/* Forty-odd languages scroll inside the dialog rather
                            than growing it past the screen. */}
                        <div className="scrollbar-thin max-h-[60vh] overflow-y-auto">{list}</div>
                    </DialogContent>
                </Dialog>
            )}
        </>
    );
};

export default LanguageSwitcher;
