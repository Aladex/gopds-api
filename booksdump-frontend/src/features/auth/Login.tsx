import React, { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HelpCircle, Lock, User } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import { useAuth } from '@/context/AuthContext';
import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import CenteredBox from '@/features/auth/CenteredBox';

const Login: React.FC = () => {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const { t } = useTranslation();
    const { setUser, isAuthenticated, isLoaded } = useAuth();
    const navigate = useNavigate();
    const [loginError, setLoginError] = useState('');

    // Redirect if user is already authenticated
    useEffect(() => {
        if (isLoaded && isAuthenticated) {
            navigate('/books/page/1');
        }
    }, [isAuthenticated, isLoaded, navigate]);

    // Don't render the form if user is authenticated or still loading
    if (!isLoaded || isAuthenticated) {
        return null;
    }

    const handleLogin = async (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();

        // The submit button stays enabled on purpose: a disabled button gives a
        // reader nothing to press and no reason why, whereas submitting says it.
        if (!username || !password) {
            setLoginError(t('usernameAndPasswordRequired'));
            return;
        }

        setLoginError('');

        try {
            const userData = await authApi.login({ username, password });

            // The login response already carries the account, so there is no
            // reason to ask the server who just signed in.
            setUser({
                username: userData.username,
                first_name: userData.first_name,
                last_name: userData.last_name,
                is_superuser: userData.is_superuser,
                books_lang: userData.books_lang,
                have_favs: userData.have_favs,
            });
            navigate('/books/page/1');
        } catch (error) {
            if (isApiError(error) && error.status === 403) {
                const message = (error.body as { message?: string } | undefined)?.message;
                switch (message) {
                    case 'bad_credentials':
                        setLoginError(t('badCredentials'));
                        break;
                    case 'user not active':
                        setLoginError(t('userNotActive'));
                        break;
                    case 'bad password':
                        setLoginError(t('badPassword'));
                        break;
                    default:
                        setLoginError(t('loginOrPasswordIncorrect'));
                }
                return;
            }
            console.error('Error logging in:', error);
        }
    };

    return (
        <CenteredBox>
            <form onSubmit={handleLogin} className="flex flex-col gap-4">
                <h1 className="text-lg font-semibold">{t('login')}</h1>

                {loginError && (
                    <Alert variant="destructive">
                        <AlertDescription>{loginError}</AlertDescription>
                    </Alert>
                )}

                <div className="flex flex-col gap-1.5">
                    <label htmlFor="login-username" className="text-xs text-muted-foreground">
                        {t('username')}
                    </label>
                    <div className="relative">
                        <User
                            aria-hidden="true"
                            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
                        />
                        <Input
                            id="login-username"
                            name="username"
                            autoComplete="username"
                            value={username}
                            onChange={(event) => setUsername(event.target.value)}
                            className="pl-9"
                        />
                    </div>
                </div>

                <div className="flex flex-col gap-1.5">
                    <label htmlFor="login-password" className="text-xs text-muted-foreground">
                        {t('password')}
                    </label>
                    <div className="relative">
                        <Lock
                            aria-hidden="true"
                            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
                        />
                        <Input
                            id="login-password"
                            name="password"
                            type="password"
                            autoComplete="current-password"
                            value={password}
                            onChange={(event) => setPassword(event.target.value)}
                            className="pl-9"
                        />
                    </div>
                </div>

                <div className="flex items-center justify-between pt-2">
                    <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => navigate('/forgot-password')}
                        aria-label={t('forgotPassword')}
                        title={t('forgotPassword')}
                    >
                        <HelpCircle className="size-4" />
                    </Button>
                    <Button type="submit" size="sm">
                        {t('loginButton')}
                    </Button>
                </div>
            </form>
        </CenteredBox>
    );
};

export default Login;
