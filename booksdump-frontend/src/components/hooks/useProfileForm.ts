import { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useAuth } from '../../context/AuthContext';
import * as authApi from '@/api/auth';
import * as telegramApi from '@/api/telegram';

export const useProfileForm = (open: boolean) => {
    const { logout, updateUser, user } = useAuth();
    const navigate = useNavigate();
    const { t } = useTranslation();

    const [firstName, setFirstName] = useState('');
    const [lastName, setLastName] = useState('');
    const [oldPassword, setOldPassword] = useState('');
    const [newPassword, setNewPassword] = useState('');
    const [showPasswordFields, setShowPasswordFields] = useState(false);

    // Telegram bot state
    const [botToken, setBotToken] = useState('');
    const [botConnected, setBotConnected] = useState(user?.has_bot_token || false);
    const [botLoading, setBotLoading] = useState(false);

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
            const status = await telegramApi.getBotStatus();
            setBotConnected(status?.has_bot_token || false);
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
    }, [user, firstName, lastName, newPassword, oldPassword, updateUser, notify]);

    const handleDropSessions = useCallback(async () => {
        try {
            await authApi.dropSessions();
            logout();
            navigate('/login');
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
            await telegramApi.setBotToken(botToken.trim());
            setBotConnected(true);
            setBotToken('');
            notify('telegramBot.tokenSet');
            // Update user context
            if (user) {
                updateUser({ ...user, has_bot_token: true });
            }
        } catch {
            notify('telegramBot.tokenError', false);
        } finally {
            setBotLoading(false);
        }
    }, [botToken, user, updateUser, notify]);

    const handleRemoveBotToken = useCallback(async () => {
        setBotLoading(true);
        try {
            await telegramApi.removeBotToken();
            {
                setBotConnected(false);
                notify('telegramBot.tokenRemoved');
                if (user) {
                    updateUser({ ...user, has_bot_token: false });
                }
            }
        } catch {
            notify('telegramBot.removeError', false);
        } finally {
            setBotLoading(false);
        }
    }, [user, updateUser, notify]);

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
    };
};
