import React, { useRef } from 'react';
import { useTranslation } from 'react-i18next';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';

import type { ProfileSection } from '@/features/profile/ProfileOpenerContext';
import ProfileContent, { focusFirstProfileField } from '@/features/profile/ProfileContent';

type ProfileDialogProps = {
    open: boolean;
    /** The part of the profile to open on, when a page asked for one. */
    section?: ProfileSection;
    onClose: () => void;
};

/** ProfileDialog is the profile on a desktop, where there is room for a modal. */
const ProfileDialog: React.FC<ProfileDialogProps> = ({ open, section, onClose }) => {
    const { t } = useTranslation();
    // Nothing here is a Radix trigger, so nothing tells it where focus goes back to.
    const opener = useRef<HTMLElement | null>(null);

    const placeFocus = (event: Event) => {
        opener.current = document.activeElement as HTMLElement | null;
        if (!section) {
            focusFirstProfileField(event);
            return;
        }
        // Asked for a connection, the first field is in a hidden panel and takes
        // no focus, which left it on the page behind the dialog, with Tab walking
        // that page. The dialog itself focuses nothing a keypress could set off;
        // left to Radix it would be the logout button.
        event.preventDefault();
        (event.currentTarget as HTMLElement).focus();
    };

    const returnFocus = (event: Event) => {
        event.preventDefault();
        opener.current?.focus({ preventScroll: true });
    };

    return (
        <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
            <DialogContent
                onOpenAutoFocus={placeFocus}
                onCloseAutoFocus={returnFocus}
                closeLabel={t('close')}
                className="max-h-[85vh] overflow-y-auto scrollbar-thin sm:max-w-lg"
            >
                {/* Named for a screen reader; on screen the profile leads with
                    the username instead of a title. */}
                <DialogTitle className="sr-only">{t('user')}</DialogTitle>
                <DialogDescription className="sr-only">
                    {t('profileSection.personalData')}
                </DialogDescription>
                <ProfileContent open={open} section={section} onClose={onClose} />
            </DialogContent>
        </Dialog>
    );
};

export default ProfileDialog;
