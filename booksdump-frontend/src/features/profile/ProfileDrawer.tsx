import React from 'react';
import { useTranslation } from 'react-i18next';

import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from '@/shared/ui/drawer';

import ProfileContent from '@/features/profile/ProfileContent';

type ProfileDrawerProps = {
    open: boolean;
    onClose: () => void;
};

/**
 * Opens the sheet without putting the caret in a text field.
 *
 * The dialog on a desktop starts in the first field, which saves a click. A
 * phone answers the same focus by raising the keyboard: half the screen goes,
 * the sheet is left showing a field and a half of a form nobody asked to edit,
 * and the reader has to dismiss the keyboard before they can even read it.
 *
 * Focus still has to land inside the sheet — leaving it on the trigger behind
 * the overlay would break the focus trap — so it goes to the sheet itself,
 * which holds it without asking for input.
 */
function keepKeyboardClosed(event: Event) {
    event.preventDefault();
    (event.currentTarget as HTMLElement | null)?.focus();
}

/**
 * ProfileDrawer is the profile on a phone: a sheet from the bottom edge, which
 * can be swiped away as well as dismissed.
 */
const ProfileDrawer: React.FC<ProfileDrawerProps> = ({ open, onClose }) => {
    const { t } = useTranslation();

    return (
        <Drawer open={open} onOpenChange={(next) => !next && onClose()}>
            {/*
              The height is written in the primitive's own variant form on
              purpose. DrawerContent caps a bottom sheet with
              data-[vaul-drawer-direction=bottom]:max-h-[80vh], and a plain
              max-h-* loses to it on specificity — the sheet stayed at 80vh
              while claiming 85, with the profile scrolling inside it for want
              of the last hundred pixels.
            */}
            <DrawerContent
                onOpenAutoFocus={keepKeyboardClosed}
                className="data-[vaul-drawer-direction=bottom]:max-h-[92vh]"
            >
                {/* The heading names the sheet for a screen reader; on screen the
                    profile leads with the username instead. */}
                <DrawerTitle className="sr-only">{t('user')}</DrawerTitle>
                <DrawerDescription className="sr-only">
                    {t('profileSection.personalData')}
                </DrawerDescription>
                <div className="scrollbar-thin overflow-y-auto px-4 pb-6">
                    <ProfileContent open={open} onClose={onClose} />
                </div>
            </DrawerContent>
        </Drawer>
    );
};

export default ProfileDrawer;
