import React, { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { BouncingDots } from '@/shared/ui/bouncing-dots';
import * as authApi from '@/api/auth';
import { isApiError } from '@/api/errors';
import CenteredBox from '@/features/auth/CenteredBox';

/** How long the reader is given to read the good news before being moved on. */
const REDIRECT_DELAY_MS = 5000;
const PROGRESS_TICK_MS = 100;

type ActivationState = 'activating' | 'activated' | 'failed';

/**
 * Activation is what the link in the registration mail lands on. There is no
 * form here — the token in the URL is the whole submission — so the screen is
 * three states and the route each one leads to.
 */
const Activation: React.FC = () => {
    const navigate = useNavigate();
    const { t } = useTranslation();
    const { token } = useParams<{ token: string }>();
    const [state, setState] = useState<ActivationState>('activating');
    const [progress, setProgress] = useState(0);

    useEffect(() => {
        let abandoned = false;

        const confirmToken = async () => {
            try {
                // An empty password is how this backend spells "confirm the
                // token and change nothing else".
                await authApi.changePassword({ token: token ?? '', password: '' });
                if (!abandoned) {
                    setState('activated');
                }
            } catch (error) {
                // A missing or spent token is the expected failure, and the
                // not-found page is the honest answer to a dead link. Anything
                // else — the network, a broken backend — is not the link's
                // fault and must not be reported as one.
                if (isApiError(error) && error.isNotFound) {
                    navigate('/404');
                    return;
                }
                if (!abandoned) {
                    setState('failed');
                }
            }
        };

        confirmToken();
        return () => {
            abandoned = true;
        };
    }, [token, navigate]);

    // The countdown only means something once the account is actually live.
    useEffect(() => {
        if (state !== 'activated') {
            return;
        }

        const step = (100 * PROGRESS_TICK_MS) / REDIRECT_DELAY_MS;
        const tick = setInterval(() => {
            setProgress((value) => Math.min(value + step, 100));
        }, PROGRESS_TICK_MS);
        const redirect = setTimeout(() => navigate('/login'), REDIRECT_DELAY_MS);

        return () => {
            clearInterval(tick);
            clearTimeout(redirect);
        };
    }, [state, navigate]);

    return (
        <CenteredBox>
            <div className="flex flex-col gap-4">
                <h1 className="text-center text-lg font-semibold">{t('activation')}</h1>

                {state === 'activating' && (
                    <div className="flex flex-col items-center gap-3" role="status">
                        <p>{t('activating')}</p>
                        <BouncingDots size="md" className="text-primary" />
                    </div>
                )}

                {state === 'activated' && (
                    <div className="flex flex-col gap-3 text-center">
                        <p>{t('activationSuccess')}</p>
                        <div
                            role="progressbar"
                            aria-label={t('activation')}
                            aria-valuemin={0}
                            aria-valuemax={100}
                            aria-valuenow={Math.round(progress)}
                            className="h-1 w-full overflow-hidden rounded-full bg-muted"
                        >
                            <div
                                className="h-full bg-primary ease-linear"
                                style={{ width: `${progress}%` }}
                            />
                        </div>
                    </div>
                )}

                {state === 'failed' && (
                    <Alert variant="destructive">
                        <AlertDescription>{t('activationError')}</AlertDescription>
                    </Alert>
                )}
            </div>
        </CenteredBox>
    );
};

export default Activation;
