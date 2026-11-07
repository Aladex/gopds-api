import React from 'react';
import { Trans, useTranslation } from 'react-i18next';

import { useProfileOpener, type ProfileSection } from '@/features/profile/ProfileOpenerContext';
import { CONTENT_COLUMN } from '@/shared/layout/breakpoints';
import { cn } from '@/shared/lib/utils';

type FooterProps = {
    /** Whether there is any way to donate, so the sentence has anything behind it. */
    hasDonate: boolean;
    onOpenDonate: () => void;
};

const LINKS: { section: ProfileSection; labelKey: string }[] = [
    { section: 'opds', labelKey: 'foot.opds' },
    { section: 'telegram', labelKey: 'foot.telegram' },
    { section: 'kindle', labelKey: 'foot.kindle' },
];

/*
 * Looks like a link and is a button: each opens a sheet and goes nowhere. The
 * padding is hit area, half a text gap either side, so a tap between two links
 * lands on one of them. 40px for a finger up to the phone boundary.
 */
const footLink = cn(
    'inline-block h-10 whitespace-nowrap rounded px-[.5em] leading-10 text-chrome-foreground',
    'underline decoration-chrome-muted underline-offset-3 hover:decoration-current',
    'outline-none focus-visible:ring-[3px] focus-visible:ring-chrome-muted',
    'min-[601px]:h-8 min-[601px]:leading-8',
);

/**
 * Footer is where every page ends, at every width: the header's dark band again,
 * with the ways to take books elsewhere, and the one sentence about what keeps
 * the library going. It is in the flow; nothing is fixed to the foot of a screen.
 *
 * The rows are the same at every width and never break: at 13px the widest row
 * in the widest font measured is 22.6em, so below that the type scales with the
 * column rather than letting a word fall to a line of its own. A reader's own
 * larger text is the one case left, and there a whole link or a word drops
 * rather than being cut off.
 *
 * Keeping the padding of the sentence's row and its link's margin in step holds
 * the link at the row's full height without the line growing around it.
 */
const Footer: React.FC<FooterProps> = ({ hasDonate, onOpenDonate }) => {
    const { t } = useTranslation();
    const { openProfile } = useProfileOpener();

    return (
        <footer className="mt-6 flex-none bg-chrome px-4 text-[length:min(13px,calc((100vw_-_2rem)/22.6))] leading-[18px] text-chrome-foreground">
            <div
                className={cn(CONTENT_COLUMN, 'pt-2 pb-[calc(0.5rem+env(safe-area-inset-bottom))]')}
            >
                <nav aria-label={t('profileTab.connections')} className="-mx-[.5em] flex flex-wrap">
                    {LINKS.map(({ section, labelKey }) => (
                        <button
                            key={section}
                            type="button"
                            onClick={() => openProfile(section)}
                            className={footLink}
                        >
                            {t(labelKey)}
                        </button>
                    ))}
                </nav>
                {hasDonate && (
                    <p className="m-0 py-[11px] text-chrome-muted min-[601px]:py-[7px]">
                        <Trans
                            i18nKey="foot.sentence"
                            components={{
                                donate: (
                                    <button
                                        type="button"
                                        onClick={onOpenDonate}
                                        className={cn(
                                            footLink,
                                            '-mx-[.5em] -my-[11px] min-[601px]:-my-[7px]',
                                        )}
                                    />
                                ),
                            }}
                        />
                    </p>
                )}
            </div>
        </footer>
    );
};

export default Footer;
