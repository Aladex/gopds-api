import React, { useState } from 'react';
import { Trans, useTranslation } from 'react-i18next';
import {
    AlertCircle,
    CheckCircle2,
    ChevronDown,
    LogOut,
    Plug,
    ShieldCheck,
    UserRound,
} from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { Expandable } from '@/shared/ui/expandable';
import { Input } from '@/shared/ui/input';
import { Tabs, TabsList, TabsTrigger } from '@/shared/ui/tabs';
import FadingTabPanel from '@/shared/components/FadingTabPanel';
import { cn } from '@/shared/lib/utils';

import { useProfileForm } from '@/features/profile/useProfileForm';
import { OpdsConnection } from '@/features/opds/OpdsInfo';
import InterfaceLanguageToggle from '@/shared/layout/InterfaceLanguageToggle';
import KindleSettings from '@/features/kindle/KindleSettings';
import type { ProfileSection } from '@/features/profile/ProfileOpenerContext';

type ProfileContentProps = {
    open: boolean;
    /** The part of the profile to open on, when a page asked for one. */
    section?: ProfileSection;
    /** Shown in the phone's bottom sheet rather than the desktop dialog. */
    inSheet?: boolean;
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

/**
 * The three faces of the profile, in the order they are asked for: who I am,
 * how I get in, and what else is plugged into the account.
 */
const TABS = [
    { value: 'account', labelKey: 'profileTab.account', icon: UserRound },
    { value: 'security', labelKey: 'profileTab.security', icon: ShieldCheck },
    // Both ways of getting the books out of here and into something of the
    // reader's own — a bot in their messenger, an application on their reader.
    // The OPDS instructions used to be a section of the site's navigation,
    // which gave a page read once during setup the same standing as the
    // catalogue itself.
    { value: 'connections', labelKey: 'profileTab.connections', icon: Plug },
] as const;

/** The parts of the connections tab, Kindle first: it is the one most asked for. */
const CONNECTION_TABS = [
    { value: 'kindle', labelKey: 'profileConnectionTab.kindle' },
    { value: 'telegram', labelKey: 'profileConnectionTab.telegram' },
    { value: 'opds', labelKey: 'profileConnectionTab.opds' },
] as const;

type ConnectionTab = (typeof CONNECTION_TABS)[number]['value'];

/*
 * The connection last chosen, for the rest of this page's life. The profile
 * is mounted afresh on every opening, so component state alone would forget
 * it at once; nothing is written to storage, so a reload starts on Kindle.
 */
let rememberedConnection: ConnectionTab = 'kindle';

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
    /** What a password manager should make of this field. */
    autoComplete?: string;
    onChange: (value: string) => void;
}> = ({ label, id, type = 'text', value, placeholder, disabled, autoComplete, onChange }) => (
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
            autoComplete={autoComplete}
            onChange={(event) => onChange(event.target.value)}
        />
    </div>
);

const ProfileContent: React.FC<ProfileContentProps> = ({
    open,
    section,
    inSheet = false,
    onClose,
}) => {
    const { t } = useTranslation();
    const [showInstruction, setShowInstruction] = useState(false);
    // Switching tabs unmounts fields but not their values: the form state lives
    // in useProfileForm, so a name typed here survives a look at the bot tab.
    // The dialog and the sheet both mount this afresh on every opening, so the
    // section asked for can simply choose the first tab.
    const [tab, setTab] = useState<string>(section === 'kindle' ? 'connections' : TABS[0].value);
    // Asked for the Kindle settings, the profile opens on them; otherwise on
    // the connection last looked at.
    const [connection, setConnection] = useState<ConnectionTab>(
        section === 'kindle' ? 'kindle' : rememberedConnection,
    );
    const chooseConnection = (value: ConnectionTab) => {
        rememberedConnection = value;
        setConnection(value);
    };

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
            <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 pr-8">
                <span className="truncate font-semibold">{user?.username}</span>
                {user?.date_joined && (
                    <span className="truncate text-xs text-muted-foreground">
                        {t('memberSince', { date: formatDate(user.date_joined) })}
                    </span>
                )}
            </div>

            {/*
              Three tabs rather than one column of five sections. Stacked, the
              profile ran half again as tall as a phone screen, so it opened on
              a form whose Save sat somewhere below the fold; split, the tallest
              of the three fits with room to spare.

              Each tab names itself with an icon and a short word, and in the
              phone's sheet the icon stands above the word in three equal
              columns: «Аккаунт · Безопасность · Подключения» side by side were
              cut to «Акка… · Безопасн… · Подключ…» at 320 and 360px, and a tab
              whose name is cut cannot be told from its neighbour.
            */}
            <Tabs value={tab} onValueChange={setTab} className="mt-3 grid grid-cols-1">
                <TabsList stacked={inSheet} className="row-start-1 w-full">
                    {TABS.map((entry) => (
                        <TabsTrigger
                            key={entry.value}
                            value={entry.value}
                            icon={entry.icon}
                            // On a desktop the three share the row by their words.
                            className={inSheet ? undefined : 'flex-auto'}
                        >
                            {t(entry.labelKey)}
                        </TabsTrigger>
                    ))}
                </TabsList>

                <FadingTabPanel value="account" active={tab === 'account'}>
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

                    <Group title={t('interfaceLanguage')}>
                        {/* It applies on click, like the books language does; the Save
                            at the foot is for the fields above it. */}
                        <InterfaceLanguageToggle />
                    </Group>
                </FadingTabPanel>

                <FadingTabPanel value="security" active={tab === 'security'}>
                    {/*
                      Password and sessions are separate groups on purpose. Together,
                      the two session buttons sat directly beneath the password fields
                      and read as the way to commit them — while the button that
                      actually does is the Save at the foot.
                    */}
                    <Group title={t('profileSection.password')} first>
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
                                    autoComplete="current-password"
                                    value={oldPassword}
                                    onChange={setOldPassword}
                                />
                                <Field
                                    id="profile-new-password"
                                    label={t('newPassword')}
                                    type="password"
                                    autoComplete="new-password"
                                    value={newPassword}
                                    onChange={setNewPassword}
                                />
                                <p className="text-xs text-muted-foreground">
                                    {t('passwordSavedWithProfile')}
                                </p>
                            </div>
                        </Expandable>
                    </Group>

                    {/*
                      Signing out and ending every session are the same act at two
                      scopes — this device, or all of them — so they sit together, the
                      wider one marked as the heavier.
                    */}
                    <Group title={t('profileSection.sessions')}>
                        <div className="flex flex-wrap gap-2">
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => {
                                    resetFields();
                                    handleLogout();
                                }}
                            >
                                <LogOut className="size-4" />
                                {t('logoutButton')}
                            </Button>
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
                </FadingTabPanel>

                <FadingTabPanel value="connections" active={tab === 'connections'}>
                    {/*
                      Three ways of getting the books out, each a tab of its
                      own: one long column of all three ran past a phone's
                      sheet several times over, and the Kindle settings — the
                      ones the book card sends the reader to — were the last.
                      The shared tabs' underline form, so they read as parts of
                      this tab rather than a second bar beside it; and the same
                      fading panels in one grid cell as the tabs above, so the
                      sheet keeps its height as the three are switched.
                    */}
                    <Tabs
                        value={connection}
                        onValueChange={(value) => chooseConnection(value as ConnectionTab)}
                        className="mt-3 grid grid-cols-1"
                    >
                        <TabsList
                            variant="underline"
                            aria-label={t('profileTab.connections')}
                            className="row-start-1"
                        >
                            {CONNECTION_TABS.map((entry) => (
                                <TabsTrigger key={entry.value} value={entry.value}>
                                    {t(entry.labelKey)}
                                </TabsTrigger>
                            ))}
                        </TabsList>

                        <FadingTabPanel value="kindle" active={connection === 'kindle'}>
                            <Group
                                first
                                title={
                                    <>
                                        {t('profileSection.kindle')}
                                        <span
                                            className={cn(
                                                'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] normal-case tracking-normal',
                                                user?.kindle_email
                                                    ? 'border-green-600/50 text-green-600 dark:border-green-400/50 dark:text-green-400'
                                                    : 'text-muted-foreground',
                                            )}
                                        >
                                            {user?.kindle_email ? (
                                                <CheckCircle2
                                                    aria-hidden="true"
                                                    className="size-3"
                                                />
                                            ) : (
                                                <AlertCircle
                                                    aria-hidden="true"
                                                    className="size-3"
                                                />
                                            )}
                                            {user?.kindle_email
                                                ? t('kindle.configured')
                                                : t('kindle.notConfigured')}
                                        </span>
                                    </>
                                }
                            >
                                <KindleSettings />
                            </Group>
                        </FadingTabPanel>

                        <FadingTabPanel value="telegram" active={connection === 'telegram'}>
                            <Group
                                first
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
                                                <CheckCircle2
                                                    aria-hidden="true"
                                                    className="size-3"
                                                />
                                            ) : (
                                                <AlertCircle
                                                    aria-hidden="true"
                                                    className="size-3"
                                                />
                                            )}
                                            {botConnected
                                                ? t('telegramBot.connected')
                                                : t('telegramBot.notConfigured')}
                                        </span>
                                    </>
                                }
                            >
                                {botConnected ? (
                                    <>
                                        <p className="mb-3 text-sm text-muted-foreground">
                                            {t('telegramBot.startHint')}
                                        </p>
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
                                            <Trans
                                                i18nKey="telegramBot.tokenHint"
                                                components={{ botfather: <BotFatherLink /> }}
                                            />{' '}
                                            <button
                                                type="button"
                                                onClick={() =>
                                                    setShowInstruction((value) => !value)
                                                }
                                                aria-expanded={showInstruction}
                                                className="text-primary underline"
                                            >
                                                {t('telegramBot.instructionToggle')}
                                            </button>
                                        </p>

                                        <Expandable
                                            open={showInstruction}
                                            peekLines={0}
                                            fade={false}
                                        >
                                            <ol className="mb-3 list-decimal pl-5 text-xs text-muted-foreground [&>li]:mb-1">
                                                <li>
                                                    <Trans
                                                        i18nKey="telegramBot.step1"
                                                        components={{
                                                            botfather: <BotFatherLink />,
                                                        }}
                                                    />
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
                                            {botLoading
                                                ? t('telegramBot.connecting')
                                                : t('telegramBot.connectButton')}
                                        </Button>
                                    </>
                                )}
                            </Group>
                        </FadingTabPanel>

                        <FadingTabPanel value="opds" active={connection === 'opds'}>
                            <Group first title={t('profileSection.opdsReader')}>
                                <OpdsConnection />
                            </Group>
                        </FadingTabPanel>
                    </Tabs>
                </FadingTabPanel>
            </Tabs>

            {/*
              Stuck to the bottom edge of whatever is scrolling this.
              Everything above can be read at leisure, but the profile is a form
              and these two are what one does with a form — on a phone they sat
              below the fold, so the sheet opened showing no way to save.
              The background is the surface's own, or the fields would show
              through as they pass beneath.

              Not on the connections tab, which has no Save to keep in reach:
              there the bar only covered the bottom of the Kindle group — its
              switches, in the part the setup dialog sends the reader to —
              while offering nothing the dialog's own close does not.
            */}
            <div
                data-testid="profile-footer"
                className={cn(
                    tab !== 'connections' && 'sticky bottom-0',
                    'mt-5 flex justify-end gap-2 border-t border-border',
                    'bg-background pt-4 pb-1',
                )}
            >
                <Button variant="ghost" onClick={handleClose}>
                    {t('close')}
                </Button>
                {/* Connections have nothing this button could save: the bot is
                    connected and disconnected on its own buttons, at once, the
                    Kindle settings likewise, and the OPDS section is
                    instructions. */}
                {tab !== 'connections' && <Button onClick={handleSave}>{t('save')}</Button>}
            </div>
        </div>
    );
};

export default ProfileContent;
