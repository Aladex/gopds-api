import React from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown } from 'lucide-react';

import { cn } from '@/shared/lib/utils';
import { ALL_LANGUAGES, languageMapping } from '@/shared/lib/languageUtils';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { useBookLanguage } from '@/features/catalogue/hooks/useBookLanguage';
import { PHONE_QUERY } from '@/shared/layout/breakpoints';
import LanguageSwitcher from '@/shared/layout/LanguageSwitcher';
import { useForeignLanguage } from '@/shared/layout/useForeignLanguage';

type BookLanguageProps = {
    /** The dark bar, the line over a list, or the top of the takeover. */
    where: 'bar' | 'line' | 'takeover';
    className?: string;
};

/**
 * BookLanguage is the catalogue's language filter where the reader meets it: a
 * name, in the nominative, so one pattern holds for all forty-odd languages and
 * for «Все языки» too. In the interface's own language it stays quiet. In any
 * other it is filled, because most of the catalogue is then out of sight.
 */
const BookLanguage: React.FC<BookLanguageProps> = ({ where, className }) => {
    const { t } = useTranslation();
    const loud = useForeignLanguage();
    const { languages, selected, choose } = useBookLanguage();
    const isPhone = useMediaQuery(PHONE_QUERY);

    const code = selected || ALL_LANGUAGES;
    const name =
        code === ALL_LANGUAGES
            ? t('allLanguages')
            : (languageMapping[code]?.name ?? code.toUpperCase());

    return (
        <LanguageSwitcher
            languages={languages}
            selected={selected}
            onSelect={choose}
            isMobile={isPhone}
            renderTrigger={({ open, label, title }) => (
                <button
                    type="button"
                    onClick={open}
                    aria-label={label}
                    title={title}
                    data-book-language=""
                    className={cn(
                        'flex h-10 min-w-10 flex-none items-center justify-center rounded text-[13px] outline-none',
                        'focus-visible:ring-[3px] focus-visible:ring-focus',
                        className,
                    )}
                >
                    <span
                        className={cn(
                            'flex items-center gap-[3px] whitespace-nowrap',
                            loud
                                ? 'h-7 rounded bg-primary px-2 font-medium text-primary-foreground'
                                : 'px-1',
                        )}
                    >
                        <bdi
                            className={cn(
                                !loud &&
                                    'underline decoration-1 underline-offset-3 ' +
                                        (where === 'bar'
                                            ? 'text-chrome-foreground decoration-chrome-muted'
                                            : 'text-foreground decoration-input'),
                            )}
                        >
                            {name}
                        </bdi>
                        <ChevronDown
                            aria-hidden="true"
                            className={cn(
                                'size-3 flex-none',
                                !loud && where === 'bar' && 'text-chrome-muted',
                                !loud && where !== 'bar' && 'text-muted-foreground',
                            )}
                        />
                    </span>
                </button>
            )}
        />
    );
};

export default BookLanguage;
