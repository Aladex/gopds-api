import React, { useState } from 'react';
import {
    Alert,
    Box,
    Button,
    Chip,
    Collapse,
    Divider,
    IconButton,
    InputAdornment,
    Link,
    Snackbar,
    Typography,
} from '@mui/material';
import CheckCircleIcon from '@mui/icons-material/CheckCircle';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import LogoutIcon from '@mui/icons-material/Logout';
import { Trans, useTranslation } from 'react-i18next';
import { StyledTextField } from '../StyledDataItems';
import { buttonLinkSx } from '../commonStyles';
import { useProfileForm } from '../hooks/useProfileForm';

type ProfileContentProps = {
    open: boolean;
    onClose: () => void;
};

const BotFatherLink = (props: React.PropsWithChildren) => (
    <Link href="https://t.me/BotFather" target="_blank" rel="noopener noreferrer" underline="hover">
        {props.children}
    </Link>
);

const ProfileContent: React.FC<ProfileContentProps> = ({ open, onClose }) => {
    const { t } = useTranslation();
    const [showInstruction, setShowInstruction] = useState(false);

    const {
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
        setFirstName,
        setLastName,
        setOldPassword,
        setNewPassword,
        setBotToken,
        handleUserChange,
        handleDropSessions,
        handleLogout,
        handleSetBotToken,
        handleRemoveBotToken,
        togglePasswordFields,
        resetFields,
        closeSnackbar,
    } = useProfileForm(open);

    const handleSave = async () => {
        const success = await handleUserChange();
        if (success) {
            onClose();
        }
    };

    const handleClose = () => {
        resetFields();
        onClose();
    };

    const handleLogoutClick = () => {
        resetFields();
        handleLogout();
    };

    const formatDate = (dateStr?: string) => {
        if (!dateStr) return '';
        try {
            return new Date(dateStr).toLocaleDateString();
        } catch {
            return '';
        }
    };

    return (
        <Box display="flex" flexDirection="column" gap={1}>
            {/* Header */}
            <Box display="flex" alignItems="center" justifyContent="space-between">
                <Box display="flex" alignItems="baseline" gap={1} flexWrap="wrap" sx={{ minWidth: 0 }}>
                    <Typography variant="subtitle1" fontWeight={600} sx={{ lineHeight: 1.3 }} noWrap>
                        {user?.username}
                    </Typography>
                    {user?.date_joined && (
                        <Typography variant="caption" color="text.secondary" noWrap>
                            {t('memberSince', { date: formatDate(user.date_joined) })}
                        </Typography>
                    )}
                </Box>
                <IconButton size="small" onClick={handleLogoutClick} title={t('logoutButton')}>
                    <LogoutIcon fontSize="small" />
                </IconButton>
            </Box>

            <Divider />

            {/* Personal data */}
            <Typography variant="caption" color="text.secondary" textTransform="uppercase" letterSpacing={1}>
                {t('profileSection.personalData')}
            </Typography>
            <Box display="flex" gap={1} sx={{ flexDirection: { xs: 'column', sm: 'row' } }}>
                <StyledTextField
                    margin="none"
                    label={t('firstName')}
                    type="text"
                    fullWidth
                    size="small"
                    value={firstName}
                    onChange={(e) => setFirstName(e.target.value)}
                />
                <StyledTextField
                    margin="none"
                    label={t('lastName')}
                    type="text"
                    fullWidth
                    size="small"
                    value={lastName}
                    onChange={(e) => setLastName(e.target.value)}
                />
            </Box>

            <Divider />

            {/* Telegram bot */}
            <Box display="flex" alignItems="center" justifyContent="space-between">
                <Typography variant="caption" color="text.secondary" textTransform="uppercase" letterSpacing={1}>
                    {t('profileSection.telegramBot')}
                </Typography>
                <Chip
                    icon={botConnected ? <CheckCircleIcon /> : <ErrorOutlineIcon />}
                    label={botConnected ? t('telegramBot.connected') : t('telegramBot.notConfigured')}
                    color={botConnected ? 'success' : 'default'}
                    size="small"
                    variant="outlined"
                />
            </Box>

            {botConnected ? (
                <Box>
                    <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.7rem', display: 'block', mb: 0.5 }}>
                        {t('telegramBot.startHint')}
                    </Typography>
                    <Button
                        size="small"
                        color="error"
                        variant="outlined"
                        onClick={handleRemoveBotToken}
                        disabled={botLoading}
                    >
                        {t('telegramBot.removeToken')}
                    </Button>
                </Box>
            ) : (
                <Box>
                    <StyledTextField
                        margin="none"
                        placeholder={t('telegramBot.tokenPlaceholder')}
                        type="text"
                        fullWidth
                        size="small"
                        value={botToken}
                        onChange={(e) => setBotToken(e.target.value)}
                        disabled={botLoading}
                        slotProps={{
                            input: {
                                endAdornment: (
                                    <InputAdornment position="end">
                                        <IconButton
                                            onClick={handleSetBotToken}
                                            disabled={botLoading || !botToken.trim()}
                                            edge="end"
                                            size="small"
                                        >
                                            <CheckCircleIcon />
                                        </IconButton>
                                    </InputAdornment>
                                ),
                            },
                        }}
                    />
                    <Box display="flex" alignItems="baseline" gap={0.5} flexWrap="wrap" mt={0.25}>
                        <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.7rem' }}>
                            <Trans i18nKey="telegramBot.tokenHint" components={{ botfather: <BotFatherLink /> }} />
                        </Typography>
                        <Typography
                            component="button"
                            variant="caption"
                            color="primary"
                            onClick={() => setShowInstruction((v) => !v)}
                            sx={{
                                fontSize: '0.7rem',
                                background: 'none',
                                border: 'none',
                                padding: 0,
                                cursor: 'pointer',
                                textDecoration: 'underline',
                                fontFamily: 'inherit',
                            }}
                        >
                            {t('telegramBot.instructionToggle')}
                        </Typography>
                    </Box>
                    <Collapse in={showInstruction}>
                        <Box component="ol" sx={{ m: 0, mt: 0.5, pl: 2.5, '& li': { fontSize: '0.7rem', color: 'text.secondary', mb: 0.25 } }}>
                            <li><Trans i18nKey="telegramBot.step1" components={{ botfather: <BotFatherLink /> }} /></li>
                            <li>{t('telegramBot.step2')}</li>
                            <li>{t('telegramBot.step3')}</li>
                            <li>{t('telegramBot.step4')}</li>
                        </Box>
                    </Collapse>
                </Box>
            )}

            <Divider />

            {/* Security */}
            <Typography variant="caption" color="text.secondary" textTransform="uppercase" letterSpacing={1}>
                {t('profileSection.security')}
            </Typography>
            <Box display="flex" justifyContent="space-between">
                <Box component="button" onClick={togglePasswordFields} sx={{ ...buttonLinkSx, fontSize: '0.8125rem' }}>
                    {t('changePassword')}
                </Box>
                <Box component="button" onClick={handleDropSessions} sx={{ ...buttonLinkSx, fontSize: '0.8125rem' }}>
                    {t('dropSessions')}
                </Box>
            </Box>

            <Collapse in={showPasswordFields}>
                <Box display="flex" flexDirection="column" gap={1} mt={0.5}>
                    <StyledTextField
                        autoFocus
                        margin="none"
                        label={t('oldPassword')}
                        type="password"
                        fullWidth
                        size="small"
                        value={oldPassword}
                        onChange={(e) => setOldPassword(e.target.value)}
                    />
                    <StyledTextField
                        margin="none"
                        label={t('newPassword')}
                        type="password"
                        fullWidth
                        size="small"
                        value={newPassword}
                        onChange={(e) => setNewPassword(e.target.value)}
                    />
                </Box>
            </Collapse>

            <Divider />

            {/* Actions */}
            <Box display="flex" justifyContent="flex-end" gap={1}>
                <Button size="small" sx={{ color: 'text.secondary', minWidth: 0, px: 1, fontSize: '0.8125rem' }} onClick={handleClose}>
                    {t('cancel')}
                </Button>
                <Button size="small" variant="contained" disableElevation sx={{ minWidth: 0, px: 2, fontSize: '0.8125rem' }} onClick={handleSave}>
                    {t('save')}
                </Button>
            </Box>

            {/* Snackbar */}
            <Snackbar
                open={snackbar.open}
                autoHideDuration={4000}
                onClose={closeSnackbar}
                anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
            >
                <Alert onClose={closeSnackbar} severity={snackbar.severity} variant="filled">
                    {t(snackbar.message)}
                </Alert>
            </Snackbar>
        </Box>
    );
};

export default ProfileContent;