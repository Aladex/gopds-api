import React from 'react';
import { useTranslation } from 'react-i18next';

import {
    Drawer,
    DrawerContent,
    DrawerDescription,
    DrawerTitle,
} from '@/shared/ui/drawer';

import ProfileContent, { focusFirstProfileField } from '@/features/profile/ProfileContent';

type ProfileDrawerProps = {
    open: boolean;
    onClose: () => void;
};

/**
 * ProfileDrawer is the profile on a phone: a sheet from the bottom edge, which
 * can be swiped away as well as dismissed.
 */
const ProfileDrawer: React.FC<ProfileDrawerProps> = ({ open, onClose }) => {
    const { t } = useTranslation();

    return (
        <Drawer open={open} onOpenChange={(next) => !next && onClose()}>
            <DrawerContent onOpenAutoFocus={focusFirstProfileField} className="max-h-[85vh]">
                {/* The heading names the sheet for a screen reader; on screen the
                    profile leads with the username instead. */}
                <DrawerTitle className="sr-only">{t('user')}</DrawerTitle>
                <DrawerDescription className="sr-only">{t('profileSection.personalData')}</DrawerDescription>
                <div className="scrollbar-thin overflow-y-auto px-4 pb-6">
                    <ProfileContent open={open} onClose={onClose} />
                </div>
            </DrawerContent>
        </Drawer>
    );
};

export default ProfileDrawer;
