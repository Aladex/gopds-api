import React from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/shared/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import {
    Drawer,
    DrawerContent,
    DrawerDescription,
    DrawerFooter,
    DrawerTitle,
} from '@/shared/ui/drawer';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';

import KindleVideo from '@/features/kindle/KindleVideo';
import { KindleSteps } from '@/features/kindle/KindleSteps';

type KindleSetupDialogProps = {
    open: boolean;
    onClose: () => void;
    /** Takes the reader to the Kindle settings in their profile. */
    onOpenSettings: () => void;
};

/**
 * The two ways out: go to the settings, or close. The main one comes first in
 * the phone's stack and last — rightmost — in the desktop's row.
 */
const SetupActions: React.FC<Omit<KindleSetupDialogProps, 'open'> & { stacked: boolean }> = ({
    onClose,
    onOpenSettings,
    stacked,
}) => {
    const { t } = useTranslation();
    const settings = (
        <Button
            onClick={() => {
                onClose();
                onOpenSettings();
            }}
        >
            {t('kindleSetup.openSettings')}
        </Button>
    );
    const close = (
        <Button variant="ghost" onClick={onClose}>
            {t('close')}
        </Button>
    );
    return stacked ? (
        <>
            {settings}
            {close}
        </>
    ) : (
        <>
            {close}
            {settings}
        </>
    );
};

/**
 * KindleSetupDialog is what "To Kindle" opens before there is a Kindle to send
 * to: why a setup is needed at all, the video of doing it, the steps in words,
 * and the way to the one place they are done.
 *
 * A dialog on a desktop, a sheet from the bottom edge on a phone — the
 * breakpoint and the split the profile and the donate dialog use. A centred
 * modal on a phone left the video a strip between two margins and the steps
 * below the fold of a box that did not scroll as the page does.
 */
const KindleSetupDialog: React.FC<KindleSetupDialogProps> = ({ open, onClose, onOpenSettings }) => {
    const { t } = useTranslation();
    const isMobile = useMediaQuery('(max-width: 600px)');

    // The shell the donate dialog has: on a phone the title stays put above
    // a scrolling body, on a desktop the dialog itself scrolls under the same
    // 85vh cap. The phone's cap is the profile sheet's 92vh, as before.
    if (isMobile) {
        return (
            <Drawer open={open} onOpenChange={(next) => !next && onClose()}>
                {/* The height in the primitive's own variant form, as the
                    profile sheet has it: a plain max-h loses to the bottom
                    sheet's own cap on specificity. */}
                <DrawerContent className="data-[vaul-drawer-direction=bottom]:max-h-[92vh]">
                    <DrawerTitle className="px-4 pb-1 text-base font-medium">
                        {t('kindleSetup.title')}
                    </DrawerTitle>
                    <div
                        data-testid="kindle-setup-scroll"
                        className="scrollbar-thin flex min-h-0 flex-col gap-4 overflow-y-auto px-4"
                    >
                        <DrawerDescription className="text-sm text-muted-foreground">
                            {t('kindleSetup.intro')}
                        </DrawerDescription>
                        {/* The sheet follows a drag; the player's own scrubber
                            must not drag the sheet away. */}
                        <div data-vaul-no-drag>
                            <KindleVideo />
                        </div>
                        <KindleSteps />
                    </div>
                    {/* Stacked, full width, below the scroller: side by side
                        the two buttons needed 312px of a 320px screen's 288
                        and pushed Close off its left edge. */}
                    <DrawerFooter data-testid="kindle-setup-footer" className="pt-3">
                        <SetupActions stacked onClose={onClose} onOpenSettings={onOpenSettings} />
                    </DrawerFooter>
                </DrawerContent>
            </Drawer>
        );
    }

    return (
        <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
            <DialogContent
                closeLabel={t('close')}
                className="scrollbar-thin max-h-[85vh] overflow-y-auto sm:max-w-lg"
            >
                <DialogHeader>
                    <DialogTitle>{t('kindleSetup.title')}</DialogTitle>
                    <DialogDescription>{t('kindleSetup.intro')}</DialogDescription>
                </DialogHeader>

                <KindleVideo />

                <KindleSteps />

                <DialogFooter>
                    <SetupActions
                        stacked={false}
                        onClose={onClose}
                        onOpenSettings={onOpenSettings}
                    />
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};

export default KindleSetupDialog;
