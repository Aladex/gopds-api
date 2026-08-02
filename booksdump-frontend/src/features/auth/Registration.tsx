import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Lock, Mail, Ticket, User } from 'lucide-react';

import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import CenteredBox from '@/features/auth/CenteredBox';
import { AuthField, AuthForm, BackToLogin } from '@/features/auth/AuthForm';

const Registration: React.FC = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [email, setEmail] = useState('');
    const [invite, setInvite] = useState('');
    const [regError, setRegError] = useState('');
    const [isRegistered, setIsRegistered] = useState(false);
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

    // Nothing is left to fill in once the mail is out, so the form goes rather
    // than sitting there inviting a second account.
    if (isRegistered) {
        return (
            <CenteredBox moment="beginning">
                <div className="flex flex-col gap-4">
                    <p className="text-center">{t('registrationSuccess')}</p>
                    <div className="flex items-center">
                        <BackToLogin />
                    </div>
                </div>
            </CenteredBox>
        );
    }

    return (
        <CenteredBox moment="beginning">
            <AuthForm
                title={t('registration')}
                error={regError}
                onSubmit={handleRegister}
                submitLabel={t('registerButton')}
                submitDisabled={!isFormValid}
                secondaryAction={<BackToLogin />}
            >
                <AuthField
                    id="registration-username"
                    name="username"
                    label={t('usernameRegistration')}
                    icon={User}
                    autoComplete="username"
                    value={username}
                    onChange={setUsername}
                />
                <AuthField
                    id="registration-email"
                    name="email"
                    label={t('email')}
                    icon={Mail}
                    type="email"
                    autoComplete="email"
                    value={email}
                    onChange={setEmail}
                />
                {/*
                  new-password, not password: it is what makes a manager offer
                  to generate one and then save it, instead of trying to fill an
                  existing account's secret into a brand new account.
                */}
                <AuthField
                    id="registration-password"
                    name="new-password"
                    label={t('password')}
                    icon={Lock}
                    type="password"
                    autoComplete="new-password"
                    value={password}
                    onChange={setPassword}
                />
                <AuthField
                    id="registration-invite"
                    name="invite"
                    label={t('inviteCode')}
                    icon={Ticket}
                    autoComplete="off"
                    value={invite}
                    onChange={setInvite}
                />
            </AuthForm>
        </CenteredBox>
    );
};

export default Registration;
