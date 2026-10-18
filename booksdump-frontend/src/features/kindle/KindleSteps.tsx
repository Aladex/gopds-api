import React from 'react';
import { Trans } from 'react-i18next';

/**
 * Amazon's device settings. The page has moved between deep links more than
 * once, and a stale anchor lands the reader on a sign-in and then nowhere in
 * particular; the root of "Manage Your Content and Devices" survives that.
 */
export const AMAZON_DEVICES_URL = 'https://www.amazon.com/mycd';

const AmazonLink = (props: React.PropsWithChildren) => (
    <a
        href={AMAZON_DEVICES_URL}
        target="_blank"
        rel="noopener noreferrer"
        className="text-primary underline-offset-2 hover:underline"
    >
        {props.children}
    </a>
);

/**
 * KindleSteps is the setup in three steps, the same words in the setup dialog
 * and in the profile.
 */
export const KindleSteps: React.FC = () => (
    <ol className="list-decimal pl-5 text-sm text-muted-foreground [&>li]:mb-1.5">
        <li>
            <Trans i18nKey="kindle.step1" components={{ amazon: <AmazonLink /> }} />
        </li>
        <li>
            <Trans i18nKey="kindle.step2" />
        </li>
        <li>
            <Trans i18nKey="kindle.step3" />
        </li>
    </ol>
);
