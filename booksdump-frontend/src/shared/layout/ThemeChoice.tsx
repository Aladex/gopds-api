import React from 'react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/shared/lib/utils';
import { useTheme, type ThemeMode } from '@/context/ThemeContext';

const OPTIONS: { mode: ThemeMode; labelKey: string }[] = [
    { mode: 'light', labelKey: 'themeLight' },
    { mode: 'dark', labelKey: 'themeDark' },
];

/** The theme as the profile offers it: built as the interface language beside it. */
const ThemeChoice: React.FC = () => {
    const { t } = useTranslation();
    const { mode, setThemeMode } = useTheme();

    return (
        <div
            role="group"
            aria-label={t('theme')}
            className="inline-flex items-center gap-1 text-xs"
        >
            {OPTIONS.map((option) => (
                <button
                    key={option.mode}
                    type="button"
                    onClick={() => setThemeMode(option.mode)}
                    aria-pressed={mode === option.mode}
                    className={cn(
                        'rounded px-2 py-1 transition-colors',
                        'outline-none focus-visible:ring-[3px] focus-visible:ring-focus',
                        mode === option.mode
                            ? 'bg-accent font-medium text-accent-foreground'
                            : 'text-muted-foreground hover:text-foreground',
                    )}
                >
                    {t(option.labelKey)}
                </button>
            ))}
        </div>
    );
};

export default ThemeChoice;
