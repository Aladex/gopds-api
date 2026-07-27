import React, { useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import { AlertCircle, CheckCircle2, ChevronDown, LogOut } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Expandable } from '@/components/ui/expandable';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';

import { useProfileForm } from '../hooks/useProfileForm';

type ProfileContentProps = {
    open: boolean;
    onClose: () => void;
};

/**
 * Where focus should land when the profile opens.
 *
 * Left to itself the dialog focuses its first focusable child, which here is
 * the logout button — a Space or Enter away from signing the reader out of
 * something they only opened to change their name.
 */
export const PROFILE_FIRST_FIELD_ID = 'profile-first-name';

export function focusFirstProfileField(event: Event) {
    event.preventDefault();
    document.getElementById(PROFILE_FIRST_FIELD_ID)?.focus();
}

const BotFatherLink = (props: React.PropsWithChildren) => (
    <a
        href="https://t.me/BotFather"
        target="_blank"
        rel="noopener noreferrer"
        className="text-primary underline-offset-2 hover:underline"
    >
        {props.children}
    </a>
);

/**
 * Group is a labelled band of the profile.
 *
 * There are only three of them and each holds a field or two, so they are set
 * apart by a heading and a rule rather than boxed in cards — a border around
 * every pair of inputs is more frame than content.
 */
const Group: React.FC<React.PropsWithChildren<{ title: React.ReactNode; first?: boolean }>> = ({
    title,
    first,
    children,
}) => (
    <section className={cn('pt-4', !first && 'mt-4 border-t border-border')}>
        <h3 className="mb-2 flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
            {title}
        </h3>
        {children}
    </section>
);

const Field: React.FC<{
    label?: string;
    id: string;
    type?: string;
    value: string;
    placeholder?: string;
    disabled?: boolean;
    onChange: (value: string) => void;
}> = ({ label, id, type = 'text', value, placeholder, disabled, onChange }) => (
    <div className="min-w-0 flex-1">
        {label && (
            <label htmlFor={id} className="mb-1 block text-xs text-muted-foreground">
                {label}
            </label>
        )}
        <Input
            id={id}
            type={type}
            value={value}
            placeholder={placeholder}
            disabled={disabled}
            onChange={(event) => onChange(event.target.value)}
        />
    </div>
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
    } = useProfileForm(open);

    const formatDate = (value?: string) => {
        if (!value) return '';
        const timestamp = Date.parse(value);
        return isNaN(timestamp) ? '' : new Date(timestamp).toLocaleDateString();
    };

    // The server takes the name and the password in one request, so the profile
    // offers one Save rather than a button per group pretending otherwise.
    const handleSave = async () => {
        if (await handleUserChange()) {
            onClose();
        }
    };

    const handleClose = () => {
        resetFields();
        onClose();
    };

    return (
        <div className="flex flex-col">
            <div className="flex items-center justify-between gap-2">
                <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                    <span className="truncate font-semibold">{user?.username}</span>
                    {user?.date_joined && (
                        <span className="truncate text-xs text-muted-foreground">
                            {t('memberSince', { date: formatDate(user.date_joined) })}
                        </span>
                    )}
                </div>
                <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => {
                        resetFields();
                        handleLogout();
                    }}
                    title={t('logoutButton')}
                    aria-label={t('logoutButton')}
                    className="shrink-0 text-muted-foreground"
                >
                    <LogOut className="size-4" />
                </Button>
            </div>

            <Group title={t('profileSection.personalData')} first>
                <div className="flex flex-col gap-3 sm:flex-row">
                    <Field
                        id={PROFILE_FIRST_FIELD_ID}
                        label={t('firstName')}
                        value={firstName}
                        onChange={setFirstName}
                    />
                    <Field
                        id="profile-last-name"
                        label={t('lastName')}
                        value={lastName}
                        onChange={setLastName}
                    />
                </div>
            </Group>

            <Group title={t('profileSection.security')}>
                <button
                    type="button"
                    onClick={togglePasswordFields}
                    aria-expanded={showPasswordFields}
                    className="flex w-full items-center justify-between text-sm font-medium"
                >
                    {t('changePassword')}
                    <ChevronDown
                        aria-hidden="true"
                        className={cn(
                            'size-4 text-muted-foreground transition-transform duration-200',
                            showPasswordFields && 'rotate-180',
                        )}
                    />
                </button>

                {/* fade off: this hides a form, not truncated prose. */}
                <Expandable open={showPasswordFields} peekLines={0} fade={false}>
                    <div className="flex flex-col gap-3 pt-3">
                        <Field
                            id="profile-old-password"
                            label={t('oldPassword')}
                            type="password"
                            value={oldPassword}
                            onChange={setOldPassword}
                        />
                        <Field
                            id="profile-new-password"
                            label={t('newPassword')}
                            type="password"
                            value={newPassword}
                            onChange={setNewPassword}
                        />
                    </div>
                </Expandable>

                <div className="mt-4">
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={handleDropSessions}
                        className="border-destructive/50 text-destructive hover:bg-destructive/10 hover:text-destructive"
                    >
                        {t('dropSessions')}
                    </Button>
                </div>
            </Group>

            <Group
                title={
                    <>
                        {t('profileSection.telegramBot')}
                        <span
                            className={cn(
                                'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] normal-case tracking-normal',
                                botConnected
                                    ? 'border-green-600/50 text-green-600 dark:border-green-400/50 dark:text-green-400'
                                    : 'text-muted-foreground',
                            )}
                        >
                            {botConnected ? (
                                <CheckCircle2 aria-hidden="true" className="size-3" />
                            ) : (
                                <AlertCircle aria-hidden="true" className="size-3" />
                            )}
                            {botConnected ? t('telegramBot.connected') : t('telegramBot.notConfigured')}
                        </span>
                    </>
                }
            >
                {botConnected ? (
                    <>
                        <p className="mb-3 text-sm text-muted-foreground">{t('telegramBot.startHint')}</p>
                        <Button
                            variant="outline"
                            size="sm"
                            disabled={botLoading}
                            onClick={handleRemoveBotToken}
                            className="border-destructive/50 text-destructive hover:bg-destructive/10 hover:text-destructive"
                        >
                            {t('telegramBot.removeToken')}
                        </Button>
                    </>
                ) : (
                    <>
                        <p className="mb-2 text-xs text-muted-foreground">
                            <Trans i18nKey="telegramBot.tokenHint" components={{ botfather: <BotFatherLink /> }} />{' '}
                            <button
                                type="button"
                                onClick={() => setShowInstruction((value) => !value)}
                                aria-expanded={showInstruction}
                                className="text-primary underline"
                            >
                                {t('telegramBot.instructionToggle')}
                            </button>
                        </p>

                        <Expandable open={showInstruction} peekLines={0} fade={false}>
                            <ol className="mb-3 list-decimal pl-5 text-xs text-muted-foreground [&>li]:mb-1">
                                <li>
                                    <Trans i18nKey="telegramBot.step1" components={{ botfather: <BotFatherLink /> }} />
                                </li>
                                <li>{t('telegramBot.step2')}</li>
                                <li>{t('telegramBot.step3')}</li>
                                <li>{t('telegramBot.step4')}</li>
                            </ol>
                        </Expandable>

                        <div className="mb-3">
                            <Field
                                id="profile-bot-token"
                                placeholder={t('telegramBot.tokenPlaceholder')}
                                value={botToken}
                                disabled={botLoading}
                                onChange={setBotToken}
                            />
                        </div>
                        <Button
                            size="sm"
                            disabled={botLoading || !botToken.trim()}
                            onClick={handleSetBotToken}
                        >
                            {botLoading ? t('telegramBot.connecting') : t('telegramBot.connectButton')}
                        </Button>
                    </>
                )}
            </Group>

            <div className="mt-5 flex justify-end gap-2 border-t border-border pt-4">
                <Button variant="ghost" onClick={handleClose}>
                    {t('close')}
                </Button>
                <Button onClick={handleSave}>{t('save')}</Button>
            </div>
        </div>
    );
};

export default ProfileContent;
