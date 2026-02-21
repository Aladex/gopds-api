import React from 'react';
import { SwipeableDrawer, Box } from '@mui/material';
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
                <ProfileContent open={open} onClose={onClose} />
            </Box>
        </SwipeableDrawer>
    );
};

export default ProfileDrawer;
