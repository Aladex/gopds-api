import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';
import { getLanguageDisplaySafe } from '@/shared/lib/languageUtils';
import { cn } from '@/shared/lib/utils';

import { useAuth } from '@/context/AuthContext';
import { useSearchBar } from '@/context/SearchBarContext';

/**
 * LanguagePrompt asks a new reader which language to show books in.
 *
 * A freshly registered account has no language, and no language means no
 * filter — the catalogue arrives as the whole library at once, in this
 * instance eighty-six per cent Russian with forty other languages threaded
 * through it. Whichever language the reader wants, most of what they scroll is
 * unreadable.
 *
 * It is asked rather than guessed. A default would be a guess about the person
 * dressed as a decision, and a silent one: they would never learn that
 * something had been hidden from them, or that the choice was theirs. The
 * question costs one click and is asked once.
 *
 * The languages offered are the ones the library actually holds, in the order
 * the server returns them, which is by how many books are in each — so the
 * likely answers are the ones in reach.
 */

/** A library with no books has no languages to ask about. */
const EMPTY_LIBRARY_FALLBACK = 'en';

const LanguagePrompt: React.FC = () => {
    const { t } = useTranslation();
    const { user, updateLang } = useAuth();
    const { languages, languagesLoaded, setSelectedLanguage } = useSearchBar();

    const [answering, setAnswering] = useState(false);

    const asked = useRef(false);

    const needsLanguage = Boolean(user) && !user?.books_lang;
    const offered = languages.filter((lang) => getLanguageDisplaySafe(lang) !== null);

    /*
      A brand new instance has no books, so there is nothing to choose between
      and nothing to ask. English is the fallback there — not because it is the
      likely answer, but because some value has to stand until books arrive and
      the reader can pick properly.

      An empty list is exactly what an empty library looks like, which is why
      the loaded flag matters: without it, "no languages" and "not yet" are the
      same thing and the fallback can never fire.
    */
    useEffect(() => {
        if (!needsLanguage || !languagesLoaded || asked.current) {
            return;
        }
        if (offered.length === 0) {
            asked.current = true;
            updateLang(EMPTY_LIBRARY_FALLBACK);
            setSelectedLanguage(EMPTY_LIBRARY_FALLBACK);
        }
    }, [needsLanguage, languagesLoaded, offered.length, updateLang, setSelectedLanguage]);

    const choose = (lang: string) => {
        setAnswering(true);
        updateLang(lang);
        setSelectedLanguage(lang);
    };

    /*
      Closed by its own flag rather than by vanishing.

      This used to answer the question by returning null, which took the whole
      dialog out of the tree while Radix still believed it open: the closing
      sequence never ran, and tearing down the overlay and the scroll lock fell
      to unmount cleanup instead. Driving `open` lets it close the way every
      other dialog here does.
    */
    const asking = needsLanguage && languagesLoaded && offered.length > 0 && !answering;

    return (
        <Dialog open={asking}>
            {/*
              There is no way past it but to answer: leaving it unanswered is
              the state being fixed, and a dismissed question would be asked
              again on the next visit, which is worse than asking once.
            */}
            <DialogContent
                showCloseButton={false}
                onEscapeKeyDown={(event) => event.preventDefault()}
                onInteractOutside={(event) => event.preventDefault()}
                className="sm:max-w-sm"
            >
                <DialogTitle>{t('chooseBooksLanguage')}</DialogTitle>
                <DialogDescription>{t('chooseBooksLanguageHint')}</DialogDescription>

                <ul className="scrollbar-thin max-h-[60vh] overflow-y-auto p-0">
                    {offered.map((lang) => (
                        <li key={lang}>
                            <button
                                type="button"
                                onClick={() => choose(lang)}
                                className={cn(
                                    'flex min-h-12 w-full items-center gap-3 rounded px-4 text-left text-base',
                                    'sm:min-h-10 sm:px-3 sm:text-sm',
                                    'hover:bg-accent',
                                    'outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50',
                                )}
                            >
                                {getLanguageDisplaySafe(lang)}
                            </button>
                        </li>
                    ))}
                </ul>
            </DialogContent>
        </Dialog>
    );
};

export default LanguagePrompt;
