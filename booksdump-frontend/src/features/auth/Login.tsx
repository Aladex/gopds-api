import React, { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { HelpCircle, Lock, User } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { useAuth } from '@/context/AuthContext';
import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import CenteredBox from '@/features/auth/CenteredBox';
import { AuthField, AuthForm } from '@/features/auth/AuthForm';

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
            <AuthForm
                title={t('login')}
                error={loginError}
                onSubmit={handleLogin}
                submitLabel={t('loginButton')}
                secondaryAction={
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
                }
            >
                <AuthField
                    id="login-username"
                    name="username"
                    label={t('username')}
                    icon={User}
                    autoComplete="username"
                    value={username}
                    onChange={setUsername}
                />
                <AuthField
                    id="login-password"
                    name="password"
                    label={t('password')}
                    icon={Lock}
                    type="password"
                    autoComplete="current-password"
                    value={password}
                    onChange={setPassword}
                />
            </AuthForm>
        </CenteredBox>
    );
};

export default Login;
