import { createContext, useContext } from 'react';

/**
 * The parts of the profile a page can open it on: the Kindle settings, where
 * the book card's setup dialog sends the reader, the Telegram ones, where the
 * bot's «Open profile» button does, and all three from the page's footer.
 */
export type ProfileSection = 'kindle' | 'telegram' | 'opds';

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
