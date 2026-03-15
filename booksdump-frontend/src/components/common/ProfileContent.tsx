import React, { useState } from 'react';
import {
    Alert,
    Box,
    Button,
    Chip,
    Collapse,
    IconButton,
    Link,
    Snackbar,
    Tab,
    Tabs,
    Typography,
} from '@mui/material';
import CheckCircleIcon from '@mui/icons-material/CheckCircle';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import LogoutIcon from '@mui/icons-material/Logout';
import { Trans, useTranslation } from 'react-i18next';
import { StyledTextField } from '../StyledDataItems';
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

// ─── Section card wrapper ────────────────────────────────────────────────────

const SectionCard: React.FC<React.PropsWithChildren<{ sx?: object }>> = ({ children, sx }) => (
    <Box
        sx={(theme) => ({
            border: '1px solid',
            borderColor: theme.palette.mode === 'dark' ? 'rgba(255,255,255,0.15)' : 'divider',
            borderRadius: 3,
            overflow: 'hidden',
            boxShadow: theme.palette.mode === 'dark' ? '0 1px 4px rgba(0,0,0,0.5)' : theme.shadows[1],
            ...sx,
        })}
    >
        {children}
    </Box>
);

const SectionHeader: React.FC<React.PropsWithChildren> = ({ children }) => (
    <Box
        sx={(theme) => ({
            px: 2,
            py: 1.5,
            borderBottom: '1px solid',
            borderColor: theme.palette.mode === 'dark' ? 'rgba(255,255,255,0.15)' : 'divider',
            bgcolor: theme.palette.mode === 'dark' ? 'rgba(255,255,255,0.06)' : 'rgba(0,0,0,0.03)',
        })}
    >
        {children}
    </Box>
);

const SectionBody: React.FC<React.PropsWithChildren> = ({ children }) => (
    <Box sx={{ p: 2 }}>
        {children}
    </Box>
);

// ─── General tab ─────────────────────────────────────────────────────────────

type GeneralTabProps = {
    firstName: string;
    lastName: string;
    oldPassword: string;
    newPassword: string;
    showPasswordFields: boolean;
    setFirstName: (v: string) => void;
    setLastName: (v: string) => void;
    setOldPassword: (v: string) => void;
    setNewPassword: (v: string) => void;
    handleUserChange: () => Promise<boolean>;
    handleDropSessions: () => void;
    togglePasswordFields: () => void;
    onClose: () => void;
    resetFields: () => void;
    handleLogout: () => void;
    user: { username?: string; date_joined?: string } | null | undefined;
};

const GeneralTab: React.FC<GeneralTabProps> = ({
    firstName,
    lastName,
    oldPassword,
    newPassword,
    showPasswordFields,
    setFirstName,
    setLastName,
    setOldPassword,
    setNewPassword,
    handleUserChange,
    handleDropSessions,
    togglePasswordFields,
    onClose,
    resetFields,
    handleLogout,
    user,
}) => {
    const { t } = useTranslation();

    const formatDate = (dateStr?: string) => {
        if (!dateStr) return '';
        try {
            return new Date(dateStr).toLocaleDateString();
        } catch {
            return '';
        }
    };

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

    return (
        <Box display="flex" flexDirection="column" gap={2}>
            {/* Header row: username + date + logout */}
            <Box display="flex" alignItems="center" justifyContent="space-between" gap={1}>
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
                <IconButton
                    size="small"
                    onClick={handleLogoutClick}
                    title={t('logoutButton')}
                    sx={{ flexShrink: 0, color: 'text.secondary' }}
                >
                    <LogoutIcon fontSize="small" />
                </IconButton>
            </Box>

            {/* Personal data */}
            <SectionCard>
                <SectionHeader>
                    <Typography variant="caption" color="text.secondary" textTransform="uppercase" letterSpacing={0.8} fontWeight={600}>
                        {t('profileSection.personalData')}
                    </Typography>
                </SectionHeader>
                <SectionBody>
                    <Box display="flex" gap={1.5} sx={{ flexDirection: { xs: 'column', sm: 'row' } }}>
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
                    <Box display="flex" justifyContent="flex-end" gap={1} mt={1.5}>
                        <Button
                            size="small"
                            sx={{ color: 'text.secondary', minWidth: 0, px: 1.5, fontSize: '0.8125rem' }}
                            onClick={handleClose}
                        >
                            {t('cancel')}
                        </Button>
                        <Button
                            size="small"
                            variant="contained"
                            disableElevation
                            sx={{ minWidth: 0, px: 2, fontSize: '0.8125rem' }}
                            onClick={handleSave}
                        >
                            {t('save')}
                        </Button>
                    </Box>
                </SectionBody>
            </SectionCard>

            {/* Security */}
            <SectionCard>
                <SectionHeader>
                    <Typography variant="caption" color="text.secondary" textTransform="uppercase" letterSpacing={0.8} fontWeight={600}>
                        {t('profileSection.security')}
                    </Typography>
                </SectionHeader>
                <SectionBody>
                    {/* Change password — collapsible with arrow */}
                    <Box
                        onClick={togglePasswordFields}
                        sx={{
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'space-between',
                            cursor: 'pointer',
                            userSelect: 'none',
                            mb: showPasswordFields ? 1.5 : 0,
                        }}
                    >
                        <Typography variant="body2" fontWeight={500}>
                            {t('changePassword')}
                        </Typography>
                        <ExpandMoreIcon
                            fontSize="small"
                            sx={{
                                color: 'text.secondary',
                                transition: 'transform 0.2s',
                                transform: showPasswordFields ? 'rotate(180deg)' : 'rotate(0deg)',
                            }}
                        />
                    </Box>
                    <Collapse in={showPasswordFields}>
                        <Box display="flex" flexDirection="column" gap={1.5}>
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
                            <Box display="flex" justifyContent="flex-end">
                                <Button
                                    size="small"
                                    variant="contained"
                                    disableElevation
                                    sx={{ minWidth: 0, px: 2, fontSize: '0.8125rem' }}
                                    onClick={handleSave}
                                >
                                    {t('save')}
                                </Button>
                            </Box>
                        </Box>
                    </Collapse>

                    {/* Drop sessions */}
                    <Box sx={(theme) => ({ mt: 2, pt: 1.5, borderTop: '1px solid', borderColor: theme.palette.mode === 'dark' ? 'rgba(255,255,255,0.15)' : 'divider' })}>
                        <Button
                            size="small"
                            variant="outlined"
                            color="error"
                            onClick={handleDropSessions}
                            sx={{ fontSize: '0.8125rem' }}
                        >
                            {t('dropSessions')}
                        </Button>
                    </Box>
                </SectionBody>
            </SectionCard>
        </Box>
    );
};

// ─── Telegram tab ─────────────────────────────────────────────────────────────

type TelegramTabProps = {
    botToken: string;
    botConnected: boolean;
    botLoading: boolean;
    setBotToken: (v: string) => void;
    handleSetBotToken: () => void;
    handleRemoveBotToken: () => void;
};

const TelegramTab: React.FC<TelegramTabProps> = ({
    botToken,
    botConnected,
    botLoading,
    setBotToken,
    handleSetBotToken,
    handleRemoveBotToken,
}) => {
    const { t } = useTranslation();
    const [showInstruction, setShowInstruction] = useState(false);

    return (
        <Box display="flex" flexDirection="column" gap={2}>
            {/* Status chip */}
            <Box display="flex" alignItems="center" gap={1}>
                <Chip
                    icon={botConnected ? <CheckCircleIcon /> : <ErrorOutlineIcon />}
                    label={botConnected ? t('telegramBot.connected') : t('telegramBot.notConfigured')}
                    color={botConnected ? 'success' : 'default'}
                    size="small"
                    variant="outlined"
                />
            </Box>

            {botConnected ? (
                /* ── Connected state ── */
                <SectionCard>
                    <SectionBody>
                        <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>
                            {t('telegramBot.startHint')}
                        </Typography>
                        <Button
                            size="small"
                            color="error"
                            variant="outlined"
                            onClick={handleRemoveBotToken}
                            disabled={botLoading}
                            sx={{ fontSize: '0.8125rem' }}
                        >
                            {t('telegramBot.removeToken')}
                        </Button>
                    </SectionBody>
                </SectionCard>
            ) : (
                /* ── Not connected state ── */
                <SectionCard>
                    <SectionHeader>
                        <Typography variant="caption" color="text.secondary" textTransform="uppercase" letterSpacing={0.8} fontWeight={600}>
                            {t('profileSection.telegramBot')}
                        </Typography>
                    </SectionHeader>
                    <SectionBody>
                        {/* Token hint + instruction toggle */}
                        <Typography variant="caption" color="text.secondary" sx={{ fontSize: '0.75rem', display: 'block', mb: 0.5 }}>
                            <Trans i18nKey="telegramBot.tokenHint" components={{ botfather: <BotFatherLink /> }} />
                            {' '}
                            <Box
                                component="button"
                                onClick={() => setShowInstruction((v) => !v)}
                                sx={{
                                    background: 'none',
                                    border: 'none',
                                    padding: 0,
                                    cursor: 'pointer',
                                    color: 'primary.main',
                                    fontSize: 'inherit',
                                    fontFamily: 'inherit',
                                    textDecoration: 'underline',
                                    verticalAlign: 'baseline',
                                }}
                            >
                                {t('telegramBot.instructionToggle')}
                            </Box>
                        </Typography>

                        {/* Collapsible step-by-step instructions */}
                        <Collapse in={showInstruction}>
                            <Box
                                component="ol"
                                sx={{
                                    m: 0,
                                    mt: 0.75,
                                    mb: 1.25,
                                    pl: 2.5,
                                    '& li': {
                                        fontSize: '0.75rem',
                                        color: 'text.secondary',
                                        mb: 0.5,
                                        lineHeight: 1.5,
                                    },
                                }}
                            >
                                <li><Trans i18nKey="telegramBot.step1" components={{ botfather: <BotFatherLink /> }} /></li>
                                <li>{t('telegramBot.step2')}</li>
                                <li>{t('telegramBot.step3')}</li>
                                <li>{t('telegramBot.step4')}</li>
                            </Box>
                        </Collapse>

                        {/* Token input */}
                        <StyledTextField
                            margin="none"
                            placeholder={t('telegramBot.tokenPlaceholder')}
                            type="text"
                            fullWidth
                            size="small"
                            value={botToken}
                            onChange={(e) => setBotToken(e.target.value)}
                            disabled={botLoading}
                            sx={{ mb: 1.5 }}
                        />

                        {/* Explicit connect button */}
                        <Button
                            variant="contained"
                            disableElevation
                            size="small"
                            onClick={handleSetBotToken}
                            disabled={botLoading || !botToken.trim()}
                            sx={{ fontSize: '0.8125rem', minWidth: 100 }}
                        >
                            {botLoading ? t('telegramBot.connecting') : t('telegramBot.connectButton')}
                        </Button>
                    </SectionBody>
                </SectionCard>
            )}
        </Box>
    );
};

// ─── Root component ───────────────────────────────────────────────────────────

const ProfileContent: React.FC<ProfileContentProps> = ({ open, onClose }) => {
    const { t } = useTranslation();
    const [activeTab, setActiveTab] = useState(0);

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

    return (
        <Box display="flex" flexDirection="column" gap={0}>
            {/* Tabs */}
            <Tabs
                value={activeTab}
                onChange={(_, v) => setActiveTab(v)}
                variant="fullWidth"
                sx={{
                    minHeight: 40,
                    mb: 2,
                    borderBottom: '1px solid',
                    borderColor: 'divider',
                    '& .MuiTab-root': {
                        minHeight: 40,
                        fontSize: '0.8125rem',
                        fontWeight: 600,
                        textTransform: 'none',
                        py: 0.75,
                    },
                }}
            >
                <Tab label={t('profileTab.general')} />
                <Tab label={t('profileTab.telegram')} />
            </Tabs>

            {/* Tab panels */}
            <Box sx={{ px: 0 }}>
                {activeTab === 0 && (
                    <GeneralTab
                        firstName={firstName}
                        lastName={lastName}
                        oldPassword={oldPassword}
                        newPassword={newPassword}
                        showPasswordFields={showPasswordFields}
                        setFirstName={setFirstName}
                        setLastName={setLastName}
                        setOldPassword={setOldPassword}
                        setNewPassword={setNewPassword}
                        handleUserChange={handleUserChange}
                        handleDropSessions={handleDropSessions}
                        togglePasswordFields={togglePasswordFields}
                        onClose={onClose}
                        resetFields={resetFields}
                        handleLogout={handleLogout}
                        user={user}
                    />
                )}
                {activeTab === 1 && (
                    <TelegramTab
                        botToken={botToken}
                        botConnected={botConnected}
                        botLoading={botLoading}
                        setBotToken={setBotToken}
                        handleSetBotToken={handleSetBotToken}
                        handleRemoveBotToken={handleRemoveBotToken}
                    />
                )}
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