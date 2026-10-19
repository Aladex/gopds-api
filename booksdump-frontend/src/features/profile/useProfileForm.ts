import { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useAuth } from '@/context/AuthContext';
import * as authApi from '@/api/auth';

/*
 * Only notify stays wrapped: it is named in a dependency list below.
 * Everything else is called from a click handler, so a fresh function each
 * render is observed by nobody and the React Compiler memoises what is worth
 * memoising. The Telegram connection has its own state, in TelegramSettings.
 */
export const useProfileForm = (open: boolean) => {
    const { logout, updateUser, user } = useAuth();
    const navigate = useNavigate();
    const { t } = useTranslation();

    const [firstName, setFirstName] = useState('');
    const [lastName, setLastName] = useState('');
    const [oldPassword, setOldPassword] = useState('');
    const [newPassword, setNewPassword] = useState('');
    const [showPasswordFields, setShowPasswordFields] = useState(false);

    // Outcomes are announced through the application's toaster rather than a
    // snackbar owned by this hook.
    const notify = useCallback(
        (key: string, ok = true) => (ok ? toast.success(t(key)) : toast.error(t(key))),
        [t],
    );

    useEffect(() => {
        if (user && open) {
            setFirstName(user.first_name || '');
            setLastName(user.last_name || '');
        }
    }, [user, open]);

    const resetFields = () => {
        setShowPasswordFields(false);
        setOldPassword('');
        setNewPassword('');
    };

    const handleUserChange = async () => {
        try {
            const userData = {
                username: user?.username,
                first_name: firstName,
                last_name: lastName,
                new_password: newPassword,
                password: oldPassword,
                books_lang: user?.books_lang,
            };
            const updated = await authApi.updateCurrentUser(userData);
            updateUser(updated);
            notify('profileSaved');
            setShowPasswordFields(false);
            setOldPassword('');
            setNewPassword('');
            return true;
        } catch {
            notify('profileSaveError', false);
        }
        return false;
    };

    const handleDropSessions = async () => {
        try {
            await authApi.dropSessions();
            logout();
            navigate('/login');
        } catch (error) {
            console.error('Error dropping sessions:', error);
        }
    };

    const handleLogout = () => {
        logout();
        navigate('/login');
    };

    const togglePasswordFields = () => {
        setShowPasswordFields((prev) => !prev);
    };

    return {
        // State
        firstName,
        lastName,
        oldPassword,
        newPassword,
        showPasswordFields,
        user,

        // Setters
        setFirstName,
        setLastName,
        setOldPassword,
        setNewPassword,

        // Actions
        handleUserChange,
        handleDropSessions,
        handleLogout,
        togglePasswordFields,
        resetFields,
    };
};
