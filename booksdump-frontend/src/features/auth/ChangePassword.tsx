import React, { useState, useEffect } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ArrowLeft, Loader2, Lock } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import CenteredBox from '@/features/auth/CenteredBox';

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
    // new password into and only then admitting the link is dead wastes the
    // one thing the reader came here to do.
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
            <form onSubmit={handleChangePassword} className="flex flex-col gap-4">
                <h1 className="text-center text-lg font-semibold">{t('changePassword')}</h1>

                {changeError && (
                    <Alert variant="destructive">
                        <AlertDescription>{changeError}</AlertDescription>
                    </Alert>
                )}

                <div className="flex flex-col gap-1.5">
                    <label
                        htmlFor="change-password-new"
                        className="text-xs text-muted-foreground"
                    >
                        {t('newPassword')}
                    </label>
                    <div className="relative">
                        <Lock
                            aria-hidden="true"
                            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
                        />
                        {/*
                          There is no current-password field to pair this with:
                          the token from the mail is what proves who is asking.
                        */}
                        <Input
                            id="change-password-new"
                            name="new-password"
                            type="password"
                            autoComplete="new-password"
                            value={newPassword}
                            onChange={(event) => setNewPassword(event.target.value)}
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
                    <Button type="submit" size="sm" disabled={!newPassword || isChanging}>
                        {isChanging && <Loader2 aria-hidden="true" className="size-4 animate-spin" />}
                        {t('changePasswordButton')}
                    </Button>
                </div>
            </form>
        </CenteredBox>
    );
};

export default ChangePassword;
