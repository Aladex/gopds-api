import React, { useEffect, useState } from 'react';
import { Box, Button, Dialog, DialogActions, DialogContent, DialogTitle } from '@mui/material';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useAuth } from '../../context/AuthContext';
import { fetchWithAuth } from '../../api/config';
import { StyledTextField } from '../StyledDataItems';
import { buttonLinkSx } from '../commonStyles';

type ProfileDialogProps = {
    open: boolean;
    onClose: () => void;
};

const ProfileDialog: React.FC<ProfileDialogProps> = ({ open, onClose }) => {
    const { logout, updateUser, user } = useAuth();
    const navigate = useNavigate();
    const { t } = useTranslation();
    const [showPasswordFields, setShowPasswordFields] = useState(false);
    const [firstName, setFirstName] = useState(user?.first_name || '');
    const [lastName, setLastName] = useState(user?.last_name || '');
    const [oldPassword, setOldPassword] = useState('');
    const [newPassword, setNewPassword] = useState('');

    useEffect(() => {
        if (user) {
            setFirstName(user.first_name || '');
            setLastName(user.last_name || '');
        }
    }, [user, open]);

    const handleClose = () => {
        setShowPasswordFields(false);
        setOldPassword('');
        setNewPassword('');
        onClose();
    };

    const handleLogout = () => {
        handleClose();
        logout();
        navigate('/login');
    };

    const togglePasswordFields = () => {
        setShowPasswordFields((prev) => !prev);
    };

    const handleUserChange = async () => {
        handleClose();
        try {
            const userData = {
                username: user?.username,
                first_name: firstName,
                last_name: lastName,
                new_password: newPassword,
                password: oldPassword,
                books_lang: user?.books_lang,
            };
            const response = await fetchWithAuth.post('/books/change-me', userData);

            if (response.status === 200) {
                const data = response.data;
                updateUser(data);
            } else {
                console.error('Failed to update user');
            }
        } catch (error) {
            console.error('Error updating user:', error);
        }
    };

    const handleDropSessions = async () => {
        try {
            const response = await fetchWithAuth(`/drop-sessions`);
            if (response.status === 200) {
                logout();
                navigate('/login');
            } else {
                console.error('Failed to drop sessions');
            }
        } catch (error) {
            console.error('Error dropping sessions:', error);
        }
    };

    return (
        <Dialog open={open} onClose={handleClose} maxWidth="sm" fullWidth>
            <DialogTitle>{t('userInfo')}</DialogTitle>
            <DialogContent>
                <Box display="flex" justifyContent="space-between" marginBottom={2}>
                    <Box component="button" onClick={togglePasswordFields} sx={buttonLinkSx}>
                        {t('changePassword')}
                    </Box>
                    <Box component="button" onClick={handleDropSessions} sx={buttonLinkSx}>
                        {t('dropSessions')}
                    </Box>
                </Box>
                {showPasswordFields && (
                    <>
                        <StyledTextField
                            autoFocus
                            margin="dense"
                            label={t('oldPassword')}
                            type="password"
                            sx={{ marginBottom: 2 }}
                            fullWidth
                            value={oldPassword}
                            onChange={(e) => setOldPassword(e.target.value)}
                        />
                        <StyledTextField
                            margin="dense"
                            label={t('newPassword')}
                            type="password"
                            sx={{ marginBottom: 2 }}
                            fullWidth
                            value={newPassword}
                            onChange={(e) => setNewPassword(e.target.value)}
                        />
                    </>
                )}
                <StyledTextField
                    margin="dense"
                    label={t('firstName')}
                    type="text"
                    fullWidth
                    sx={{ marginBottom: 2 }}
                    value={firstName}
                    onChange={(e) => setFirstName(e.target.value)}
                />
                <StyledTextField
                    margin="dense"
                    label={t('lastName')}
                    type="text"
                    fullWidth
                    value={lastName}
                    onChange={(e) => setLastName(e.target.value)}
                />
            </DialogContent>
            <DialogActions>
                <Button sx={{ color: 'text.primary' }} onClick={handleUserChange}>
                    {t('save')}
                </Button>
                <Button sx={{ color: 'text.primary' }} onClick={handleLogout}>
                    {t('logoutButton')}
                </Button>
            </DialogActions>
        </Dialog>
    );
};

export default ProfileDialog;
