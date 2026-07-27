import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { ArrowLeft, Lock, Mail, Ticket, User } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import CenteredBox from '@/features/auth/CenteredBox';

const Registration: React.FC = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [email, setEmail] = useState('');
    const [invite, setInvite] = useState('');
    const [regError, setRegError] = useState('');
    const [isRegistered, setIsRegistered] = useState(false);
    const navigate = useNavigate();
    const { t } = useTranslation();

    // Derived, not mirrored into state: an effect recomputing this could only
    // ever agree with the render that follows it.
    const isFormValid = username !== '' && password !== '' && email !== '' && invite !== '';

    const handleRegister = async (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        setRegError('');

        try {
            await authApi.register({ username, email, password, invite });
            setIsRegistered(true);
        } catch (error) {
            const errorMessages: Record<string, string> = {
                bad_invite: t('badInvite'),
                bad_form: t('badForm'),
                user_exists: t('userExists'),
                'CSRF token invalid': t('csrfTokenInvalid'),
            };
            const body = isApiError(error)
                ? (error.body as { error?: string; message?: string } | undefined)
                : undefined;
            setRegError(
                errorMessages[body?.error ?? ''] ||
                    errorMessages[body?.message ?? ''] ||
                    t('registrationError'),
            );
        }
    };

    const backButton = (
        <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={() => navigate('/login')}
            aria-label={t('BackButton')}
            title={t('BackButton')}
        >
            <ArrowLeft className="size-4" />
        </Button>
    );

    // Nothing is left to fill in once the mail is out, so the form goes rather
    // than sitting there inviting a second account.
    if (isRegistered) {
        return (
            <CenteredBox>
                <div className="flex flex-col gap-4">
                    <p className="text-center">{t('registrationSuccess')}</p>
                    <div className="flex items-center">{backButton}</div>
                </div>
            </CenteredBox>
        );
    }

    return (
        <CenteredBox>
            <form onSubmit={handleRegister} className="flex flex-col gap-4">
                <h1 className="text-center text-xl font-semibold">{t('registration')}</h1>

                {regError && (
                    <Alert variant="destructive">
                        <AlertDescription>{regError}</AlertDescription>
                    </Alert>
                )}

                <div className="flex flex-col gap-1.5">
                    <label
                        htmlFor="registration-username"
                        className="text-xs text-muted-foreground"
                    >
                        {t('usernameRegistration')}
                    </label>
                    <div className="relative">
                        <User
                            aria-hidden="true"
                            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
                        />
                        <Input
                            id="registration-username"
                            name="username"
                            autoComplete="username"
                            value={username}
                            onChange={(event) => setUsername(event.target.value)}
                            className="pl-9"
                        />
                    </div>
                </div>

                <div className="flex flex-col gap-1.5">
                    <label htmlFor="registration-email" className="text-xs text-muted-foreground">
                        {t('email')}
                    </label>
                    <div className="relative">
                        <Mail
                            aria-hidden="true"
                            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
                        />
                        <Input
                            id="registration-email"
                            name="email"
                            type="email"
                            autoComplete="email"
                            value={email}
                            onChange={(event) => setEmail(event.target.value)}
                            className="pl-9"
                        />
                    </div>
                </div>

                <div className="flex flex-col gap-1.5">
                    <label
                        htmlFor="registration-password"
                        className="text-xs text-muted-foreground"
                    >
                        {t('password')}
                    </label>
                    <div className="relative">
                        <Lock
                            aria-hidden="true"
                            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
                        />
                        <Input
                            id="registration-password"
                            name="new-password"
                            type="password"
                            autoComplete="new-password"
                            value={password}
                            onChange={(event) => setPassword(event.target.value)}
                            className="pl-9"
                        />
                    </div>
                </div>

                <div className="flex flex-col gap-1.5">
                    <label htmlFor="registration-invite" className="text-xs text-muted-foreground">
                        {t('inviteCode')}
                    </label>
                    <div className="relative">
                        <Ticket
                            aria-hidden="true"
                            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
                        />
                        <Input
                            id="registration-invite"
                            name="invite"
                            autoComplete="off"
                            value={invite}
                            onChange={(event) => setInvite(event.target.value)}
                            className="pl-9"
                        />
                    </div>
                </div>

                <div className="flex items-center justify-between pt-2">
                    {backButton}
                    <Button type="submit" size="sm" disabled={!isFormValid}>
                        {t('registerButton')}
                    </Button>
                </div>
            </form>
        </CenteredBox>
    );
};

export default Registration;
