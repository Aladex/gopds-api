import { createContext, useContext } from 'react';

/**
 * The parts of the profile a page can open it on: the Kindle settings, where
 * the book card's setup dialog sends the reader, and the Telegram ones, where
 * the bot's «Open profile» button does.
 */
export type ProfileSection = 'kindle' | 'telegram';

export interface ProfileOpener {
    openProfile: (section?: ProfileSection) => void;
}

/*
 * The profile belongs to PrivateRoute, which decides between the sheet and the
 * dialog. A page inside it reaches the profile through this rather than a
 * prop handed down through the list and the card. Outside PrivateRoute — in a
 * test, say — there is no profile to open, and the default does nothing.
 */
export const ProfileOpenerContext = createContext<ProfileOpener>({ openProfile: () => {} });

export const useProfileOpener = () => useContext(ProfileOpenerContext);
