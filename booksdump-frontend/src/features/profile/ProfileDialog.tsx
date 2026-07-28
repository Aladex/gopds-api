import React from 'react';
import { useTranslation } from 'react-i18next';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';

import ProfileContent, { focusFirstProfileField } from '@/features/profile/ProfileContent';

type ProfileDialogProps = {
    open: boolean;
    onClose: () => void;
};

/** ProfileDialog is the profile on a desktop, where there is room for a modal. */
const ProfileDialog: React.FC<ProfileDialogProps> = ({ open, onClose }) => {
    const { t } = useTranslation();

    return (
        <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
            <DialogContent
                onOpenAutoFocus={focusFirstProfileField}
                closeLabel={t('close')}
                className="max-h-[85vh] overflow-y-auto scrollbar-thin sm:max-w-lg"
            >
                {/* Named for a screen reader; on screen the profile leads with
                    the username instead of a title. */}
                <DialogTitle className="sr-only">{t('user')}</DialogTitle>
                <DialogDescription className="sr-only">
                    {t('profileSection.personalData')}
                </DialogDescription>
                <ProfileContent open={open} onClose={onClose} />
            </DialogContent>
        </Dialog>
    );
};

export default ProfileDialog;
