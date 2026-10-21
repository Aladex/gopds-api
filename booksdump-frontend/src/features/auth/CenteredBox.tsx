import React from 'react';

import { Card } from '@/shared/ui/card';
import InterfaceLanguageToggle from '@/shared/layout/InterfaceLanguageToggle';
import { useAuthRoom } from '@/features/auth/scene/AuthRoom';

/**
 * CenteredBox is the frame every authentication screen sits in: one card in
 * the middle of a dark reading room.
 *
 * The room is the same behind every screen — login, registration, the
 * password ones, activation, the error page — except that on the error page
 * the open book has a page torn out of it. The frame does not build the room:
 * it asks the one AuthRoomProvider above the routes for it, so going from one
 * of these screens to the next keeps the same scene instead of building a new
 * one each time.
 *
 * It is dark whatever theme the reader has chosen, so the frame sets the dark
 * theme on itself and the warmer tokens of `.auth-room` over it. The card is
 * smoked glass: the scene shows through it, blurred, and its text stays
 * readable on whatever drifts behind.
 *
 * The frame covers the whole screen above the room's canvas, so it lets the
 * pointer through and only the card and the language toggle take it back:
 * the golden post flying behind can be caught, and it is worth an invite. The
 * real way to registration is the link on the sign-in card.
 *
 * On a phone held upright the card sits higher than the middle, so the desk
 * with the book stays in view below it.
 */
type CenteredBoxProps = React.PropsWithChildren<{
    /** The error page's room: the open book has a page torn out of it. */
    torn?: boolean;
}>;

const CenteredBox: React.FC<CenteredBoxProps> = ({ torn = false, children }) => {
    useAuthRoom(torn);

    return (
        <div
            data-theme="dark"
            className="auth-room pointer-events-none relative flex min-h-dvh items-center justify-center p-4 text-foreground max-[520px]:pb-[20dvh]"
        >
            {/*
              Until this existed there was no way to change the interface
              language before signing in: the header lives behind
              authentication, so an English browser meant an English login
              screen and nothing to be done about it.
            */}
            <InterfaceLanguageToggle className="pointer-events-auto fixed top-3 right-3 z-10" />

            <Card className="pointer-events-auto relative w-full max-w-[400px] gap-0 p-6 shadow-lg backdrop-blur-md backdrop-saturate-[1.1]">
                {children}
            </Card>
        </div>
    );
};

export default CenteredBox;
