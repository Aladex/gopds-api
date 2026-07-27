import React, { useState, useEffect } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Loader2, Lock } from 'lucide-react';

import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import CenteredBox from '@/features/auth/CenteredBox';
import { AuthField, AuthForm, BackToLogin } from '@/features/auth/AuthForm';

const ChangePassword: React.FC = () => {
    const [newPassword, setNewPassword] = useState('');
    const [isValidating, setIsValidating] = useState(true);
    const [isChanging, setIsChanging] = useState(false);
    const [changeError, setChangeError] = useState('');
    const { t } = useTranslation();
    const { token } = useParams<{ token: string }>();
    const navigate = useNavigate();

    useEffect(() => {
        const tokenValidation = async () => {
            setIsValidating(true);
            try {
                await authApi.activate(token ?? '');
            } catch (error) {
                // An unknown or spent token is what a 404 means here.
                if (!isApiError(error) || error.isNotFound) {
                    navigate('/404');
                }
            } finally {
                setIsValidating(false);
            }
        };
        tokenValidation();
    }, [token, navigate]);

    const handleChangePassword = async (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        setIsChanging(true);
        setChangeError('');

        try {
            await authApi.changePassword({ token: token ?? '', password: newPassword });
            // Landing back on the sign-in screen with nothing said looks like
            // the attempt was thrown away.
            toast.success(t('passwordChanged'));
            navigate('/login');
        } catch (error) {
            if (isApiError(error) && error.isNotFound) {
                navigate('/404');
                return;
            }
            const errorMessages: Record<string, string> = {
                bad_form: t('badForm'),
                invalid_user: t('invalidUser'),
                'CSRF token invalid': t('csrfTokenInvalid'),
            };
            const body = isApiError(error)
                ? (error.body as { error?: string; message?: string } | undefined)
                : undefined;
            setChangeError(
                errorMessages[body?.error ?? ''] ||
                    errorMessages[body?.message ?? ''] ||
                    t('changePasswordError'),
            );
        } finally {
            setIsChanging(false);
        }
    };

    // The token is checked before the field appears: offering a box to type a
    // new password into and only then admitting the link is dead wastes the one
    // thing the reader came here to do.
    if (isValidating) {
        return (
            <CenteredBox>
                <div className="flex flex-col items-center gap-4" role="status">
                    <p>{t('validatingToken')}</p>
                    <Loader2 aria-hidden="true" className="size-6 animate-spin text-primary" />
                </div>
            </CenteredBox>
        );
    }

    return (
        <CenteredBox>
            <AuthForm
                title={t('changePassword')}
                error={changeError}
                onSubmit={handleChangePassword}
                submitLabel={t('changePasswordButton')}
                submitDisabled={!newPassword || isChanging}
                busy={isChanging}
                secondaryAction={<BackToLogin />}
            >
                {/*
                  There is no current-password field to pair this with: the
                  token from the mail is what proves who is asking.
                */}
                <AuthField
                    id="change-password-new"
                    name="new-password"
                    label={t('newPassword')}
                    icon={Lock}
                    type="password"
                    autoComplete="new-password"
                    value={newPassword}
                    onChange={setNewPassword}
                />
            </AuthForm>
        </CenteredBox>
    );
};

export default ChangePassword;
