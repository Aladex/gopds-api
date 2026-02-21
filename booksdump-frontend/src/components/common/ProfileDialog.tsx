import React from 'react';
import { Dialog, DialogContent } from '@mui/material';
import ProfileContent from './ProfileContent';

type ProfileDialogProps = {
    open: boolean;
    onClose: () => void;
};

const ProfileDialog: React.FC<ProfileDialogProps> = ({ open, onClose }) => {
    return (
        <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth>
            <DialogContent>
                <ProfileContent open={open} onClose={onClose} />
            </DialogContent>
        </Dialog>
    );
};

export default ProfileDialog;
