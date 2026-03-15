import React from 'react';
import { SwipeableDrawer, Box, IconButton } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import ProfileContent from './ProfileContent';

type ProfileDrawerProps = {
    open: boolean;
    onClose: () => void;
};

const ProfileDrawer: React.FC<ProfileDrawerProps> = ({ open, onClose }) => {
    return (
        <SwipeableDrawer
            anchor="bottom"
            open={open}
            onClose={onClose}
            onOpen={() => {}}
            disableSwipeToOpen={false}
            PaperProps={{
                sx: {
                    borderTopLeftRadius: 16,
                    borderTopRightRadius: 16,
                    px: { xs: 1.5, sm: 2 },
                    py: { xs: 1.5, sm: 2 },
                    backgroundColor: 'background.paper',
                    color: 'text.primary',
                },
            }}
        >
            <Box sx={{ maxHeight: '80vh', overflowY: 'auto' }}>
                {/* Swipe indicator + close button */}
                <Box display="flex" alignItems="center" justifyContent="space-between" sx={{ mb: 1 }}>
                    <Box sx={{ flex: 1 }} />
                    <Box
                        sx={{
                            width: 36,
                            height: 4,
                            borderRadius: 2,
                            bgcolor: 'text.secondary',
                            opacity: 0.3,
                        }}
                    />
                    <Box sx={{ flex: 1, display: 'flex', justifyContent: 'flex-end' }}>
                        <IconButton onClick={onClose} size="small" sx={{ color: 'text.secondary' }}>
                            <CloseIcon fontSize="small" />
                        </IconButton>
                    </Box>
                </Box>
                <ProfileContent open={open} onClose={onClose} />
            </Box>
        </SwipeableDrawer>
    );
};

export default ProfileDrawer;