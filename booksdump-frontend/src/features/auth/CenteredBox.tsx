import React from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';

import { Card } from '@/shared/ui/card';
import InterfaceLanguageToggle from '@/shared/layout/InterfaceLanguageToggle';
import ClassicBackdrop from '@/features/auth/ClassicBackdrop';
import type { Moment } from '@/features/auth/passages';

/**
 * CenteredBox is the frame every authentication screen sits in: one card in the
 * middle of an otherwise empty page.
 *
 * The picture pinned to the corner is the way in to registration. It is an
 * easter egg rather than a button, so the link carries the name and the image
 * itself is hidden from assistive technology — announcing it twice would be the
 * only thing that added.
 *
 * A passage of verse drifts behind all of it where one is given. It sits at the
 * very bottom of the stack, under both the picture and the card, and takes no
 * pointer — the corner of the screen it shares with the picture belongs to the
 * picture.
 */
const CenteredBox: React.FC<React.PropsWithChildren<{ moment?: Moment }>> = ({
    moment,
    children,
}) => {
    const { t } = useTranslation();

    return (
        <div className="relative flex min-h-screen items-center justify-center p-4">
            {/*
              Until this existed there was no way to change the interface
              language before signing in: the header lives behind
              authentication, so an English browser meant an English login
              screen and nothing to be done about it.
            */}
            {moment && <ClassicBackdrop moment={moment} />}

            <InterfaceLanguageToggle className="fixed top-3 right-3 z-10" />
            {/*
              The decoration comes first in the markup on purpose. Both it and
              the card are positioned, so painting order follows the document
              and the card ends up on top without anyone naming a z-index.
            */}
            <Link
                to="/registration"
                aria-label={t('registration')}
                className="fixed right-0 bottom-0 opacity-60 transition-opacity duration-300 hover:opacity-90"
            >
                <img
                    src="/devito_back.png"
                    alt=""
                    aria-hidden="true"
                    className="block h-[120px] w-[180px] select-none sm:h-[360px] sm:w-[540px]"
                />
            </Link>

            <Card className="relative w-full max-w-[400px] gap-0 p-6">{children}</Card>
        </div>
    );
};

export default CenteredBox;
