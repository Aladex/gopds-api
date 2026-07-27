import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ArrowLeft, Mail } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import CenteredBox from '@/features/auth/CenteredBox';

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
            <form onSubmit={handleReset} className="flex flex-col gap-4">
                <h1 className="text-center text-lg font-semibold">{t('forgotPassword')}</h1>

                {resetError && (
                    <Alert variant="destructive">
                        <AlertDescription>{resetError}</AlertDescription>
                    </Alert>
                )}

                <div className="flex flex-col gap-1.5">
                    <label
                        htmlFor="forgot-password-email"
                        className="text-xs text-muted-foreground"
                    >
                        {t('email')}
                    </label>
                    <div className="relative">
                        <Mail
                            aria-hidden="true"
                            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
                        />
                        <Input
                            id="forgot-password-email"
                            name="email"
                            type="email"
                            autoComplete="email"
                            value={email}
                            onChange={(event) => setEmail(event.target.value)}
                            className="pl-9"
                        />
                    </div>
                </div>

                <div className="flex items-center justify-between pt-2">
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
                    <Button type="submit" size="sm" disabled={!email}>
                        {t('resetPasswordButton')}
                    </Button>
                </div>
            </form>
        </CenteredBox>
    );
};

export default ForgotPassword;
