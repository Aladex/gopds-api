import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Check, Globe } from 'lucide-react';

import { Button } from '@/shared/ui/button';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from '@/shared/ui/drawer';
import { ALL_LANGUAGES, getLanguageDisplaySafe, languageMapping } from '@/shared/lib/languageUtils';
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
    /** Which shell the list opens in; the search card decides. */
    isMobile: boolean;
    disabled?: boolean;
};

const LanguageSwitcher: React.FC<LanguageSwitcherProps> = ({
    languages,
    selected,
    onSelect,
    isMobile,
    disabled,
}) => {
    const { t } = useTranslation();
    const [open, setOpen] = useState(false);

    // Only languages the interface knows how to name are offered.
    const supported = languages.filter((lang) => getLanguageDisplaySafe(lang) !== null);

    const fullLabel = (lang: string) =>
        lang === ALL_LANGUAGES
            ? t('allLanguages')
            : (getLanguageDisplaySafe(lang) ?? lang.toUpperCase());

    /*
      In the card the control sits between the search button and the favourites
      toggle, so a phone has room for a flag and a code but not for a name. The
      whole library has no flag to stand for it, so it gets the globe.
    */
    const triggerContent = () => {
        if (!selected || selected === ALL_LANGUAGES) {
            return (
                <>
                    <Globe className="size-4 shrink-0" />
                    <span className="hidden truncate sm:inline">{t('allLanguages')}</span>
                </>
            );
        }
        const info = languageMapping[selected];
        return (
            <>
                <span className="sm:hidden">
                    {info ? `${info.flag} ${selected.toUpperCase()}` : selected.toUpperCase()}
                </span>
                <span className="hidden truncate sm:inline">{fullLabel(selected)}</span>
            </>
        );
    };

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
                        'outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50',
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
            {/* It stands beside the favourites toggle now, so it is built like
                it: the same outline button, not the header's bare text. */}
            <Button
                type="button"
                variant="outline"
                /* The label replaces the content rather than adding to it, so
                   the language in force has to be named here or it is not
                   announced at all. */
                aria-label={`${t('booksLanguage')}: ${fullLabel(selected || ALL_LANGUAGES)}`}
                title={t('booksLanguage')}
                disabled={disabled}
                onClick={() => setOpen(true)}
                className="max-w-[9rem] gap-1.5"
            >
                {triggerContent()}
            </Button>

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
