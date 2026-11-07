import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Check } from 'lucide-react';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from '@/shared/ui/drawer';
import { ALL_LANGUAGES, getLanguageDisplaySafe } from '@/shared/lib/languageUtils';
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
    /** Which shell the list opens in. */
    isMobile: boolean;
    /** The control that opens it: where it stands decides how it looks. */
    renderTrigger: (trigger: {
        open: () => void;
        /** The language in force, named, since a label replaces the content. */
        label: string;
        title: string;
    }) => React.ReactNode;
};

const LanguageSwitcher: React.FC<LanguageSwitcherProps> = ({
    languages,
    selected,
    onSelect,
    isMobile,
    renderTrigger,
}) => {
    const { t } = useTranslation();
    const [open, setOpen] = useState(false);

    // Only languages the interface knows how to name are offered.
    const supported = languages.filter((lang) => getLanguageDisplaySafe(lang) !== null);

    const fullLabel = (lang: string) =>
        lang === ALL_LANGUAGES
            ? t('allLanguages')
            : (getLanguageDisplaySafe(lang) ?? lang.toUpperCase());

    const choose = (lang: string) => {
        onSelect(lang);
        setOpen(false);
    };

    // The same list in both shells: which language is on offer, and which one
    // is in force, does not depend on the size of the screen.
    const list = (
        <ul className="scrollbar-thin overflow-y-auto p-0">
            {/*
              First, and set apart: it is not one of the forty, it is the way
              back out of them. Without it a filter, once set, could not be
              cleared at all — every row here narrows the catalogue and none
              widened it.
            */}
            <li className="mb-1 border-b border-border pb-1">
                <button
                    type="button"
                    onClick={() => choose(ALL_LANGUAGES)}
                    aria-current={selected === ALL_LANGUAGES ? 'true' : undefined}
                    className={cn(
                        'flex min-h-12 w-full items-center justify-between gap-3 rounded px-4 text-left text-base',
                        'sm:min-h-10 sm:px-3 sm:text-sm',
                        'hover:bg-accent',
                        'outline-none focus-visible:ring-[3px] focus-visible:ring-focus',
                        selected === ALL_LANGUAGES && 'font-medium',
                    )}
                >
                    {t('allLanguages')}
                    {selected === ALL_LANGUAGES && <Check className="size-4 flex-none" />}
                </button>
            </li>
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
                            'outline-none focus-visible:ring-[3px] focus-visible:ring-focus',
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
            {renderTrigger({
                open: () => setOpen(true),
                label: `${t('booksLanguage')}: ${fullLabel(selected || ALL_LANGUAGES)}`,
                title: t('booksLanguage'),
            })}

            {isMobile ? (
                <Drawer open={open} onOpenChange={setOpen}>
                    <DrawerContent className="max-h-[85vh]">
                        <DrawerTitle className="px-4 pb-1 text-base font-medium">
                            {t('booksLanguage')}
                        </DrawerTitle>
                        <DrawerDescription className="sr-only">
                            {t('booksLanguage')}
                        </DrawerDescription>
                        <div className="overflow-y-auto pb-6">{list}</div>
                    </DrawerContent>
                </Drawer>
            ) : (
                <Dialog open={open} onOpenChange={setOpen}>
                    <DialogContent closeLabel={t('close')} className="sm:max-w-sm">
                        <DialogTitle>{t('booksLanguage')}</DialogTitle>
                        <DialogDescription className="sr-only">
                            {t('booksLanguage')}
                        </DialogDescription>
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
