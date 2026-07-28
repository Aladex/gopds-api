import React from 'react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/shared/lib/utils';
import { useInterfaceLanguage } from '@/context/InterfaceLanguageContext';
import { INTERFACE_LANGUAGES } from '@/shared/lib/interfaceLanguage';
import { getLanguageInfo } from '@/shared/lib/languageUtils';

/**
 * The language the application speaks — two options, so a pair of buttons
 * rather than a list in a window.
 *
 * It is deliberately not in the header. The books language is a filter people
 * change often and it stays under the thumb; this one is answered once. What it
 * must be is reachable before signing in, because until now it was not
 * reachable at all: the header only exists behind authentication, so whoever
 * landed on the login page got whatever their browser reported and no way out.
 */
const InterfaceLanguageToggle: React.FC<{ className?: string }> = ({ className }) => {
    const { t } = useTranslation();
    const { language, setLanguage } = useInterfaceLanguage();

    return (
        <div
            role="group"
            aria-label={t('interfaceLanguage')}
            className={cn('inline-flex items-center gap-1 text-xs', className)}
        >
            {INTERFACE_LANGUAGES.map((option) => (
                <button
                    key={option}
                    type="button"
                    onClick={() => setLanguage(option)}
                    aria-pressed={language === option}
                    className={cn(
                        'rounded px-2 py-1 uppercase transition-colors',
                        'outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50',
                        language === option
                            ? 'bg-accent font-medium text-accent-foreground'
                            : 'text-muted-foreground hover:text-foreground',
                    )}
                >
                    {/* Two letters is all the corner of a login card affords, so
                        the name a screen reader announces comes from the mapping
                        the books switcher already uses. */}
                    <span className="sr-only">{getLanguageInfo(option).name}</span>
                    <span aria-hidden="true">{option}</span>
                </button>
            ))}
        </div>
    );
};

export default InterfaceLanguageToggle;
