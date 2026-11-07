import React from 'react';
import { useTranslation } from 'react-i18next';

import { useTheme } from '@/context/ThemeContext';

type PhoneFootProps = {
    /** Whether there is any way to donate, so the sentence has anything behind it. */
    hasDonate: boolean;
};

/**
 * PhoneFoot is where a page ends on a phone: the header's dark band again, so
 * the page has a top and a bottom, and with it the one sentence about what keeps
 * the library going and the theme, which the header no longer has room for.
 *
 * It is in the flow and stays there. Nothing is fixed to the foot of a phone's
 * screen, and the pill is a tap away at the top of the page, so nothing here
 * asks for money twice.
 */
const PhoneFoot: React.FC<PhoneFootProps> = ({ hasDonate }) => {
    const { t } = useTranslation();
    const { mode, toggleTheme } = useTheme();

    return (
        <footer className="mt-6 flex-none bg-chrome px-4 pt-3.5 pb-[calc(0.25rem+env(safe-area-inset-bottom))] text-chrome-foreground">
            {hasDonate && <p className="m-0 text-[13px] leading-[18px]">{t('foot.sentence')}</p>}
            <button
                type="button"
                onClick={toggleTheme}
                className="block min-h-10 rounded text-[13px] text-chrome-muted underline decoration-chrome-muted underline-offset-3 outline-none focus-visible:ring-[3px] focus-visible:ring-chrome-muted"
            >
                {mode === 'light' ? t('foot.themeLight') : t('foot.themeDark')}
            </button>
        </footer>
    );
};

export default PhoneFoot;
