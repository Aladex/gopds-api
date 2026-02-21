import { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../../context/AuthContext';
import { fetchWithAuth } from '../../api/config';

interface SnackbarState {
    open: boolean;
    message: string;
    severity: 'success' | 'error';
}

export const useProfileForm = (open: boolean) => {
    const { logout, updateUser, user } = useAuth();
    const navigate = useNavigate();

    const [firstName, setFirstName] = useState('');
    const [lastName, setLastName] = useState('');
    const [oldPassword, setOldPassword] = useState('');
    const [newPassword, setNewPassword] = useState('');
    const [showPasswordFields, setShowPasswordFields] = useState(false);

    // Telegram bot state
    const [botToken, setBotToken] = useState('');
    const [botConnected, setBotConnected] = useState(user?.has_bot_token || false);
    const [botLoading, setBotLoading] = useState(false);

    // Snackbar
    const [snackbar, setSnackbar] = useState<SnackbarState>({
        open: false,
        message: '',
        severity: 'success',
    });

    useEffect(() => {
        if (user && open) {
            setFirstName(user.first_name || '');
            setLastName(user.last_name || '');
            setBotConnected(user.has_bot_token || false);
        }
    }, [user, open]);

    useEffect(() => {
        if (open) {
            fetchBotStatus();
        }
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open]);

    const fetchBotStatus = useCallback(async () => {
        try {
            const response = await fetchWithAuth.get('/telegram/bot/status');
            if (response.status === 200 && response.data) {
                setBotConnected(response.data.has_bot || false);
            }
        } catch {
            // Silently fail - use has_bot_token from user
        }
    }, []);

    const resetFields = useCallback(() => {
        setShowPasswordFields(false);
        setOldPassword('');
        setNewPassword('');
        setBotToken('');
    }, []);

    const closeSnackbar = useCallback(() => {
        setSnackbar((prev) => ({ ...prev, open: false }));
    }, []);

    const handleUserChange = useCallback(async () => {
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
                updateUser(response.data);
                setSnackbar({ open: true, message: 'profileSaved', severity: 'success' });
                setShowPasswordFields(false);
                setOldPassword('');
                setNewPassword('');
                return true;
            }
        } catch {
            setSnackbar({ open: true, message: 'profileSaveError', severity: 'error' });
        }
        return false;
    }, [user, firstName, lastName, newPassword, oldPassword, updateUser]);

    const handleDropSessions = useCallback(async () => {
        try {
            const response = await fetchWithAuth('/drop-sessions');
            if (response.status === 200) {
                logout();
                navigate('/login');
            }
        } catch (error) {
            console.error('Error dropping sessions:', error);
        }
    }, [logout, navigate]);

    const handleLogout = useCallback(() => {
        logout();
        navigate('/login');
    }, [logout, navigate]);

    const handleSetBotToken = useCallback(async () => {
        if (!botToken.trim()) return;
        setBotLoading(true);
        try {
            const response = await fetchWithAuth.post('/telegram/bot', { token: botToken.trim() });
            if (response.status === 200) {
                setBotConnected(true);
                setBotToken('');
                setSnackbar({ open: true, message: 'telegramBot.tokenSet', severity: 'success' });
                // Update user context
                if (user) {
                    updateUser({ ...user, has_bot_token: true });
                }
            }
        } catch {
            setSnackbar({ open: true, message: 'telegramBot.tokenError', severity: 'error' });
        } finally {
            setBotLoading(false);
        }
    }, [botToken, user, updateUser]);

    const handleRemoveBotToken = useCallback(async () => {
        setBotLoading(true);
        try {
            const response = await fetchWithAuth.delete('/telegram/bot');
            if (response.status === 200) {
                setBotConnected(false);
                setSnackbar({ open: true, message: 'telegramBot.tokenRemoved', severity: 'success' });
                if (user) {
                    updateUser({ ...user, has_bot_token: false });
                }
            }
        } catch {
            setSnackbar({ open: true, message: 'telegramBot.removeError', severity: 'error' });
        } finally {
            setBotLoading(false);
        }
    }, [user, updateUser]);

    const togglePasswordFields = useCallback(() => {
        setShowPasswordFields((prev) => !prev);
    }, []);

    return {
        // State
        firstName,
        lastName,
        oldPassword,
        newPassword,
        showPasswordFields,
        botToken,
        botConnected,
        botLoading,
        snackbar,
        user,

        // Setters
        setFirstName,
        setLastName,
        setOldPassword,
        setNewPassword,
        setBotToken,

        // Actions
        handleUserChange,
        handleDropSessions,
        handleLogout,
        handleSetBotToken,
        handleRemoveBotToken,
        togglePasswordFields,
        resetFields,
        closeSnackbar,
    };
};
