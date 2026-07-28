import React, { useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Mail } from 'lucide-react';

import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import CenteredBox from '@/features/auth/CenteredBox';
import { AuthField, AuthForm, BackToLogin } from '@/features/auth/AuthForm';

const ForgotPassword: React.FC = () => {
    const [email, setEmail] = useState('');
    const navigate = useNavigate();
    const [resetError, setResetError] = useState('');
    const { t } = useTranslation();

    const handleReset = async (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        setResetError('');

        try {
            await authApi.requestPasswordChange(email);
            // The next step happens in the reader's inbox, so this screen has
            // to say so before it disappears.
            toast.success(t('resetLinkSent'));
            navigate('/login');
        } catch (error) {
            const errorMessages: Record<string, string> = {
                bad_form: t('badForm'),
                invalid_user: t('invalidUser'),
                'CSRF token invalid': t('csrfTokenInvalid'),
            };
            const body = isApiError(error)
                ? (error.body as { error?: string; message?: string } | undefined)
                : undefined;
            setResetError(
                errorMessages[body?.error ?? ''] ||
                    errorMessages[body?.message ?? ''] ||
                    t('resetError'),
            );
        }
    };

    return (
        <CenteredBox>
            <AuthForm
                title={t('forgotPassword')}
                error={resetError}
                onSubmit={handleReset}
                submitLabel={t('resetPasswordButton')}
                submitDisabled={!email}
                secondaryAction={<BackToLogin />}
            >
                <AuthField
                    id="forgot-password-email"
                    name="email"
                    label={t('email')}
                    icon={Mail}
                    type="email"
                    autoComplete="email"
                    value={email}
                    onChange={setEmail}
                />
            </AuthForm>
        </CenteredBox>
    );
};

export default ForgotPassword;
