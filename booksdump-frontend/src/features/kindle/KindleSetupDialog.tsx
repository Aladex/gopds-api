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

import KindleVideo from '@/features/kindle/KindleVideo';
import { KindleSteps } from '@/features/kindle/KindleSteps';

type KindleSetupDialogProps = {
    open: boolean;
    onClose: () => void;
    /** Takes the reader to the Kindle settings in their profile. */
    onOpenSettings: () => void;
};

/**
 * KindleSetupDialog is what "To Kindle" opens before there is a Kindle to send
 * to: why a setup is needed at all, the video of doing it, the steps in words,
 * and the way to the one place they are done.
 */
const KindleSetupDialog: React.FC<KindleSetupDialogProps> = ({ open, onClose, onOpenSettings }) => {
    const { t } = useTranslation();

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
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};

export default KindleSetupDialog;
