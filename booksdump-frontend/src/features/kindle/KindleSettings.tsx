import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Check, Copy } from 'lucide-react';
import { toast } from 'sonner';

import * as authApi from '@/api/auth';
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

/**
 * KindleSettings is the profile's Kindle group: the sender address to approve
 * on Amazon, the steps and the video, the reader's Kindle address, and the two
 * switches for the book card's send buttons.
 *
 * Like the Telegram block beside it, every part applies on its own button —
 * the address on Save and Remove, a switch the moment it is flipped. The
 * profile's own Save is for the account tab and never sees any of this.
 */
const KindleSettings: React.FC = () => {
    const { t } = useTranslation();
    const { user, updateUser } = useAuth();
    const current = user?.kindle_email ?? '';
    const sender = user?.kindle_sender ?? '';

    const [address, setAddress] = useState(current);
    const [busy, setBusy] = useState(false);
    const [showVideo, setShowVideo] = useState(false);
    const [copied, setCopied] = useState(false);

    const typed = normalizeKindleAddress(address);
    // Say nothing while the field is empty: an empty field is not a mistake.
    const invalid = typed !== '' && !isKindleAddress(typed);
    const canSave = !busy && typed !== '' && !invalid && typed !== current;

    const save = async () => {
        setBusy(true);
        try {
            const updated = await kindleApi.setKindleEmail(typed);
            updateUser(updated);
            setAddress(updated.kindle_email ?? typed);
            toast.success(t('kindle.saved'));
        } catch {
            toast.error(t('kindle.saveError'));
        } finally {
            setBusy(false);
        }
    };

    const remove = async () => {
        setBusy(true);
        try {
            const updated = await kindleApi.clearKindleEmail();
            updateUser(updated);
            setAddress('');
            toast.success(t('kindle.removed'));
        } catch {
            toast.error(t('kindle.saveError'));
        } finally {
            setBusy(false);
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

    const toggleButton = async (
        field: (typeof BUTTON_SWITCHES)[number]['field'],
        value: boolean,
    ) => {
        if (!user) return;
        // Shown at once, and put back if the server disagrees.
        updateUser({ ...user, [field]: value });
        try {
            updateUser(await authApi.updateCurrentUser({ [field]: value }));
        } catch {
            updateUser(user);
            toast.error(t('kindle.buttonsSaveError'));
        }
    };

    return (
        <div className="flex flex-col gap-3">
            <div>
                <p className="mb-1 text-xs text-muted-foreground">{t('kindle.senderLabel')}</p>
                {sender ? (
                    <div className="flex items-center gap-2">
                        {/* Monospace for the address alone: it is copied
                            character for character into Amazon's form. */}
                        <code
                            data-testid="kindle-sender"
                            className="min-w-0 flex-1 rounded border border-border bg-muted px-2 py-1.5 font-mono text-xs break-all"
                        >
                            {sender}
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

            <div className="flex flex-col gap-2 border-t border-border pt-3">
                {BUTTON_SWITCHES.map(({ field, labelKey }) => {
                    const id = `profile-${field}`;
                    return (
                        <div key={field} className="flex items-center justify-between gap-3">
                            <label htmlFor={id} className="text-sm">
                                {t(labelKey)}
                            </label>
                            <Switch
                                id={id}
                                checked={user?.[field] !== false}
                                onCheckedChange={(value) => toggleButton(field, value)}
                            />
                        </div>
                    );
                })}
            </div>
        </div>
    );
};

export default KindleSettings;
