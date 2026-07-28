import { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { useAuth } from '@/context/AuthContext';
import * as authApi from '@/api/auth';
import * as telegramApi from '@/api/telegram';

/*
 * Only the two whose identity is read stay wrapped: notify and fetchBotStatus
 * are named in dependency lists below. Everything else is called from a click
 * handler, so a fresh function each render is observed by nobody and the React
 * Compiler memoises what is worth memoising.
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

    // Declared before the effect that calls it: reaching forward to a const
    // happens to work, because the effect runs after the whole body, but it is
    // a temporal dead zone away from a crash if the call ever moves.
    const fetchBotStatus = useCallback(async () => {
        try {
            const status = await telegramApi.getBotStatus();
            setBotConnected(status?.has_bot_token || false);
        } catch {
            // The user's own has_bot_token stands in when the status is unknown.
        }
    }, []);

    useEffect(() => {
        if (open) {
            fetchBotStatus();
        }
    }, [open, fetchBotStatus]);

    const resetFields = () => {
        setShowPasswordFields(false);
        setOldPassword('');
        setNewPassword('');
        setBotToken('');
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

    const handleSetBotToken = async () => {
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
    };

    const handleRemoveBotToken = async () => {
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
