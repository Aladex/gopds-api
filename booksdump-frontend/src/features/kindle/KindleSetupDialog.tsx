import React from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/shared/ui/button';
import ResponsiveSheet from '@/shared/components/ResponsiveSheet';

import KindleVideo from '@/features/kindle/KindleVideo';
import { KindleSteps } from '@/features/kindle/KindleSteps';

type KindleSetupDialogProps = {
    open: boolean;
    onClose: () => void;
    /** Takes the reader to the Kindle settings in their profile. */
    onOpenSettings: () => void;
};

/**
 * KindleSetupDialog is what "Kindle" opens before there is a Kindle to send
 * to: why a setup is needed at all, the video of doing it, the steps in words,
 * and the way to the one place they are done.
 *
 * A dialog on a desktop, a sheet from the bottom edge on a phone — the shared
 * shell, so the title stays above the scrolling video and steps and the two
 * buttons below them. A centred modal on a phone left the video a strip
 * between two margins and the steps below the fold of a box that did not
 * scroll as the page does.
 */
const KindleSetupDialog: React.FC<KindleSetupDialogProps> = ({ open, onClose, onOpenSettings }) => {
    const { t } = useTranslation();

    return (
        <ResponsiveSheet
            data-testid="kindle-setup"
            open={open}
            onOpenChange={(next) => !next && onClose()}
            title={t('kindleSetup.title')}
            description={t('kindleSetup.intro')}
            closeLabel={t('close')}
            dialogClassName="sm:max-w-lg"
            primary={
                <Button
                    onClick={() => {
                        onClose();
                        onOpenSettings();
                    }}
                >
                    {t('kindleSetup.openSettings')}
                </Button>
            }
            secondary={
                <Button variant="ghost" onClick={onClose}>
                    {t('close')}
                </Button>
            }
        >
            {/* The sheet follows a drag; the player's own scrubber must not
                drag the sheet away. */}
            <div data-vaul-no-drag>
                <KindleVideo />
            </div>
            <KindleSteps />
        </ResponsiveSheet>
    );
};

export default KindleSetupDialog;
