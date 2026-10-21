import React from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { Card } from '@/shared/ui/card';
import InterfaceLanguageToggle from '@/shared/layout/InterfaceLanguageToggle';
import AuthScene from '@/features/auth/scene/AuthScene';

/**
 * CenteredBox is the frame every authentication screen sits in: one card in
 * the middle of a dark reading room.
 *
 * The room is the same behind every screen — login, registration, the
 * password ones, activation, the error page — except that on the error page
 * the open book has a page torn out of it. It is dark whatever theme the reader has
 * chosen, so the frame sets the dark theme on itself and the warmer tokens of
 * `.auth-room` over it. The card is smoked glass: the scene shows through it,
 * blurred, and its text stays readable on whatever drifts behind.
 *
 * The scene is not a way in to anything except by accident: the golden post
 * flying in it can be caught, and it is worth an invite — it opens
 * registration, with a line saying why. The real way to registration is the
 * link on the sign-in card.
 *
 * On a phone held upright the card sits higher than the middle, so the desk
 * with the book stays in view below it.
 */
type CenteredBoxProps = React.PropsWithChildren<{
    /** The error page's room: the open book has a page torn out of it. */
    torn?: boolean;
}>;

const CenteredBox: React.FC<CenteredBoxProps> = ({ torn = false, children }) => {
    const { t } = useTranslation();
    const navigate = useNavigate();

    const onGolden = () => {
        toast(t('goldenPost'));
        navigate('/registration');
    };

    return (
        <div
            data-theme="dark"
            className="auth-room relative flex min-h-dvh items-center justify-center p-4 text-foreground max-[520px]:pb-[20dvh]"
        >
            {/*
              First in the markup on purpose: the scene and the card are both
              positioned, so painting order follows the document and the card
              ends up on top without anyone naming a z-index.
            */}
            <AuthScene onGolden={onGolden} torn={torn} />

            {/*
              Until this existed there was no way to change the interface
              language before signing in: the header lives behind
              authentication, so an English browser meant an English login
              screen and nothing to be done about it.
            */}
            <InterfaceLanguageToggle className="fixed top-3 right-3 z-10" />

            <Card className="relative w-full max-w-[400px] gap-0 p-6 shadow-lg backdrop-blur-md backdrop-saturate-[1.1]">
                {children}
            </Card>
        </div>
    );
};

export default CenteredBox;
