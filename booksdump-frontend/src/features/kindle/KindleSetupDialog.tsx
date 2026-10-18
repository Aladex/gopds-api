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
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from '@/shared/ui/drawer';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';

import KindleVideo from '@/features/kindle/KindleVideo';
import { KindleSteps } from '@/features/kindle/KindleSteps';

type KindleSetupDialogProps = {
    open: boolean;
    onClose: () => void;
    /** Takes the reader to the Kindle settings in their profile. */
    onOpenSettings: () => void;
};

/** The two ways out: close, or go to the settings. */
const SetupActions: React.FC<Omit<KindleSetupDialogProps, 'open'>> = ({
    onClose,
    onOpenSettings,
}) => {
    const { t } = useTranslation();
    return (
        <>
            <Button variant="ghost" onClick={onClose}>
                {t('close')}
            </Button>
            <Button
                onClick={() => {
                    onClose();
                    onOpenSettings();
                }}
            >
                {t('kindleSetup.openSettings')}
            </Button>
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

    if (isMobile) {
        return (
            <Drawer open={open} onOpenChange={(next) => !next && onClose()}>
                {/* The height in the primitive's own variant form, as the
                    profile sheet has it: a plain max-h loses to the bottom
                    sheet's own cap on specificity. */}
                <DrawerContent className="data-[vaul-drawer-direction=bottom]:max-h-[92vh]">
                    <div
                        data-testid="kindle-setup-scroll"
                        className="scrollbar-thin flex flex-col gap-4 overflow-y-auto px-4 pb-6"
                    >
                        <div>
                            <DrawerTitle className="text-base font-medium">
                                {t('kindleSetup.title')}
                            </DrawerTitle>
                            <DrawerDescription className="mt-1 text-sm text-muted-foreground">
                                {t('kindleSetup.intro')}
                            </DrawerDescription>
                        </div>
                        {/* The sheet follows a drag; the player's own scrubber
                            must not drag the sheet away. */}
                        <div data-vaul-no-drag>
                            <KindleVideo />
                        </div>
                        <KindleSteps />
                        <div className="flex justify-end gap-2">
                            <SetupActions onClose={onClose} onOpenSettings={onOpenSettings} />
                        </div>
                    </div>
                </DrawerContent>
            </Drawer>
        );
    }

    return (
        <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
            <DialogContent
                closeLabel={t('close')}
                className="max-h-[90vh] overflow-y-auto scrollbar-thin sm:max-w-lg"
            >
                <DialogHeader>
                    <DialogTitle>{t('kindleSetup.title')}</DialogTitle>
                    <DialogDescription>{t('kindleSetup.intro')}</DialogDescription>
                </DialogHeader>

                <KindleVideo />

                <KindleSteps />

                <DialogFooter>
                    <SetupActions onClose={onClose} onOpenSettings={onOpenSettings} />
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};

export default KindleSetupDialog;
