import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Check, Copy } from 'lucide-react';
import { toast } from 'sonner';

import * as authApi from '@/api/auth';
import type { User } from '@/api/auth';
import * as kindleApi from '@/api/kindle';
import { useAuth } from '@/context/AuthContext';
import { Button } from '@/shared/ui/button';
import { Expandable } from '@/shared/ui/expandable';
import { Input } from '@/shared/ui/input';
import { Switch } from '@/shared/ui/switch';
import { copyText } from '@/shared/lib/copyText';

import KindleVideo from '@/features/kindle/KindleVideo';
import { KindleSteps } from '@/features/kindle/KindleSteps';
import { isKindleAddress, normalizeKindleAddress } from '@/features/kindle/kindleAddress';

export const KINDLE_EMAIL_FIELD_ID = 'profile-kindle-email';

/** The two card buttons a reader may hide, and the words for each switch. */
const BUTTON_SWITCHES = [
    { field: 'show_kindle_button', labelKey: 'kindle.showKindleButton' },
    { field: 'show_telegram_button', labelKey: 'kindle.showTelegramButton' },
] as const;

/*
 * Which settings change was asked for last. Held here, outside the component,
 * because the component does not outlive a closed profile: the busy flag
 * below serializes changes only within one opening, and a profile closed
 * while a change was slow and opened again could start another. The older
 * answer — or its rollback — then landed after the newer one and put the
 * whole user back as it had been. Only the latest change's answer is applied.
 */
//
// The latest request is not always the latest write, though: an older one may
// commit after it, and its answer — the truest row — is the one discarded.
// So a discarded answer marks the settings stale, and once the last change
// running has settled the user is read once more and shown as stored.
const settingsChanges = { latest: 0, running: 0, stale: false };

/** beginChange numbers a change; isLatest says whether it is still the last. */
const beginChange = () => {
    settingsChanges.running += 1;
    return ++settingsChanges.latest;
};
const isLatest = (change: number) => {
    const latest = change === settingsChanges.latest;
    settingsChanges.stale ||= !latest;
    return latest;
};

/** endChange re-reads the user after the last change, if an answer was set aside. */
const endChange = async (updateUser: (user: User) => void) => {
    settingsChanges.running -= 1;
    if (settingsChanges.running > 0 || !settingsChanges.stale) return;
    settingsChanges.stale = false;
    const asOf = settingsChanges.latest;
    try {
        const stored = await authApi.getCurrentUser();
        if (asOf === settingsChanges.latest) updateUser(stored);
    } catch {
        // The next change, or the next page load, will show the truth.
    }
};

/** SenderAddress offers a line break before the @ and nowhere else. */
const SenderAddress: React.FC<{ address: string }> = ({ address }) => {
    const at = address.lastIndexOf('@');
    if (at <= 0) return <>{address}</>;
    return (
        <>
            {address.slice(0, at)}
            <wbr />
            <span className="whitespace-nowrap">{address.slice(at)}</span>
        </>
    );
};

/**
 * KindleSettings is the profile's Kindle group: the sender address to approve
 * on Amazon, the steps and the video, and the reader's Kindle address.
 *
 * Like the Telegram block beside it, every part applies on its own button —
 * Save and Remove. The profile's own Save is for the account tab and never
 * sees any of this.
 */
const KindleSettings: React.FC = () => {
    const { t } = useTranslation();
    const { user, updateUser } = useAuth();
    const current = user?.kindle_email ?? '';
    const sender = user?.kindle_sender ?? '';

    const [address, setAddress] = useState(current);
    // One change at a time here. Each answer is the whole user, and it
    // replaces the one this page holds: two in flight could land out of
    // order, and the older answer would put back what the newer one had just
    // changed. A change made meanwhile by the card-button switches, in another
    // tab, is kept in order by the change counter above.
    const [busy, setBusy] = useState(false);
    const [showVideo, setShowVideo] = useState(false);
    const [copied, setCopied] = useState(false);

    const typed = normalizeKindleAddress(address);
    // Say nothing while the field is empty: an empty field is not a mistake.
    const invalid = typed !== '' && !isKindleAddress(typed);
    const canSave = !busy && typed !== '' && !invalid && typed !== current;

    const save = async () => {
        setBusy(true);
        const change = beginChange();
        try {
            const updated = await kindleApi.setKindleEmail(typed);
            if (!isLatest(change)) return;
            updateUser(updated);
            setAddress(updated.kindle_email ?? typed);
            toast.success(t('kindle.saved'));
        } catch {
            if (isLatest(change)) toast.error(t('kindle.saveError'));
        } finally {
            setBusy(false);
            void endChange(updateUser);
        }
    };

    const remove = async () => {
        setBusy(true);
        const change = beginChange();
        try {
            const updated = await kindleApi.clearKindleEmail();
            if (!isLatest(change)) return;
            updateUser(updated);
            setAddress('');
            toast.success(t('kindle.removed'));
        } catch {
            if (isLatest(change)) toast.error(t('kindle.saveError'));
        } finally {
            setBusy(false);
            void endChange(updateUser);
        }
    };

    const copySender = async () => {
        const done = await copyText(sender);
        setCopied(done);
        if (done) {
            setTimeout(() => setCopied(false), 1500);
        } else {
            toast.error(t('kindle.copyFailed'));
        }
    };

    return (
        <div className="flex flex-col gap-3">
            <div>
                <p className="mb-1 text-xs text-muted-foreground">{t('kindle.senderLabel')}</p>
                {sender ? (
                    <div className="flex flex-col items-start gap-2">
                        {/* Monospace for the address alone: it is copied
                            character for character into Amazon's form. A
                            line of its own, and only one place it may break —
                            before the @ — so a narrow sheet never shows the
                            domain cut in two as if it were two words. */}
                        <code
                            data-testid="kindle-sender"
                            className="w-full rounded border border-border bg-muted px-2 py-1.5 font-mono text-xs break-normal"
                        >
                            <SenderAddress address={sender} />
                        </code>
                        <Button
                            variant="outline"
                            size="sm"
                            onClick={copySender}
                            aria-label={t('kindle.copySender')}
                        >
                            {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
                            {copied ? t('kindle.copied') : t('kindle.copy')}
                        </Button>
                    </div>
                ) : (
                    <p className="text-sm text-muted-foreground">{t('kindle.senderUnavailable')}</p>
                )}
            </div>

            <KindleSteps />

            <div>
                <button
                    type="button"
                    onClick={() => setShowVideo((value) => !value)}
                    aria-expanded={showVideo}
                    className="text-xs text-primary underline"
                >
                    {showVideo ? t('kindle.hideVideo') : t('kindle.watchVideo')}
                </button>
                {/* fade off: this hides a player, not truncated prose. The
                    player is not even built until asked for, so the profile
                    does not fetch a video nobody opened. */}
                <Expandable open={showVideo} peekLines={0} fade={false}>
                    <div className="pt-2">{showVideo && <KindleVideo />}</div>
                </Expandable>
            </div>

            <div>
                <label
                    htmlFor={KINDLE_EMAIL_FIELD_ID}
                    className="mb-1 block text-xs text-muted-foreground"
                >
                    {t('kindle.addressLabel')}
                </label>
                <Input
                    id={KINDLE_EMAIL_FIELD_ID}
                    type="email"
                    inputMode="email"
                    autoComplete="off"
                    spellCheck={false}
                    value={address}
                    placeholder={t('kindle.addressPlaceholder')}
                    disabled={busy}
                    aria-invalid={invalid || undefined}
                    aria-describedby={`${KINDLE_EMAIL_FIELD_ID}-hint`}
                    onChange={(event) => setAddress(event.target.value)}
                />
                <p
                    id={`${KINDLE_EMAIL_FIELD_ID}-hint`}
                    className={
                        invalid
                            ? 'mt-1 text-xs text-destructive'
                            : 'mt-1 text-xs text-muted-foreground'
                    }
                >
                    {invalid ? t('kindle.addressInvalid') : t('kindle.addressHint')}
                </p>
                <div className="mt-2 flex flex-wrap gap-2">
                    <Button size="sm" disabled={!canSave} onClick={save}>
                        {t('kindle.save')}
                    </Button>
                    {current && (
                        <Button
                            variant="outline"
                            size="sm"
                            disabled={busy}
                            onClick={remove}
                            className="border-destructive/50 text-destructive hover:bg-destructive/10 hover:text-destructive"
                        >
                            {t('kindle.remove')}
                        </Button>
                    )}
                </div>
            </div>
        </div>
    );
};

/**
 * CardButtonSettings is the pair of switches for the book card's Kindle and
 * Telegram buttons. They are about the card, not about either connection, so
 * they sit on the profile's own tab, where a reader looks for how the site
 * looks; each applies the moment it is flipped.
 *
 * Here beside KindleSettings because the two share the change counter: a
 * switch flipped while an address is being saved, or the other way round,
 * must not let the older answer put the newer change back.
 */
export const CardButtonSettings: React.FC = () => {
    const { t } = useTranslation();
    const { user, updateUser } = useAuth();
    // Both switches wait for the one being saved, so their answers cannot
    // cross.
    const [busy, setBusy] = useState(false);

    const toggleButton = async (
        field: (typeof BUTTON_SWITCHES)[number]['field'],
        value: boolean,
    ) => {
        if (!user || busy) return;
        setBusy(true);
        const change = beginChange();
        // Shown at once, and put back if the server disagrees. Going back to
        // the user as it was is safe even if the address changed meanwhile:
        // that change's answer is then the one set aside, and the user is
        // read again once both have settled.
        updateUser({ ...user, [field]: value });
        try {
            const updated = await authApi.updateCurrentUser({ [field]: value });
            if (isLatest(change)) updateUser(updated);
        } catch {
            if (isLatest(change)) {
                updateUser(user);
                toast.error(t('kindle.buttonsSaveError'));
            }
        } finally {
            setBusy(false);
            void endChange(updateUser);
        }
    };

    return (
        <div className="flex flex-col gap-2">
            {BUTTON_SWITCHES.map(({ field, labelKey }) => {
                const id = `profile-${field}`;
                return (
                    <div key={field} className="flex items-center justify-between gap-3">
                        <label htmlFor={id} className="text-sm">
                            {t(labelKey)}
                        </label>
                        <Switch
                            id={id}
                            disabled={busy}
                            checked={user?.[field] !== false}
                            onCheckedChange={(value) => toggleButton(field, value)}
                        />
                    </div>
                );
            })}
        </div>
    );
};

export default KindleSettings;
