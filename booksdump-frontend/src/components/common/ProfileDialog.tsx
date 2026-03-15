import React from 'react';
import { Dialog, DialogContent, DialogTitle, IconButton } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import ProfileContent from './ProfileContent';

type ProfileDialogProps = {
    open: boolean;
    onClose: () => void;
};

const ProfileDialog: React.FC<ProfileDialogProps> = ({ open, onClose }) => {
    return (
        <Dialog open={open} onClose={onClose} maxWidth="sm" fullWidth>
            <DialogTitle sx={{ m: 0, p: 1.5, pb: 0, display: 'flex', justifyContent: 'flex-end' }}>
                <IconButton
                    onClick={onClose}
                    size="small"
                    sx={{ color: 'text.secondary' }}
                >
                    <CloseIcon fontSize="small" />
                </IconButton>
            </DialogTitle>
            <DialogContent>
                <ProfileContent open={open} onClose={onClose} />
            </DialogContent>
        </Dialog>
    );
};

export default ProfileDialog;