import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import * as authApi from '@/api/auth';
import { useAuth } from '@/context/AuthContext';
import {
    browserLanguage,
    isInterfaceLanguage,
    languageFromBooks,
    readStoredLanguage,
    storeLanguage,
    type InterfaceLanguage,
} from '@/shared/lib/interfaceLanguage';

/**
 * The one authority on which locale the interface is speaking.
 *
 * It used to be decided by LanguageInitializer from the books language, which
 * meant the two could not disagree — and so a reader could not have a Russian
 * interface over a Ukrainian shelf. Here the locale has its own value, its own
 * storage, and its own control.
 */

interface InterfaceLanguageContextValue {
    language: InterfaceLanguage;
    setLanguage: (language: InterfaceLanguage) => void;
}

const InterfaceLanguageContext = createContext<InterfaceLanguageContextValue | undefined>(
    undefined,
);

/**
 * Whether the reader picked a language by hand since this page was loaded.
 *
 * It lives outside React deliberately. It has to survive a remount, because a
 * choice made on the login screen must still be a choice a moment later when
 * the account arrives — but it must not survive a reload, because someone who
 * reopens the site and touches nothing has expressed no preference this time,
 * and their account should lead.
 */
let chosenThisSession = false;

/** Test seam: a fresh page load begins with nothing chosen. */
export const resetChosenThisSession = () => {
    chosenThisSession = false;
};

export const InterfaceLanguageProvider: React.FC<React.PropsWithChildren> = ({ children }) => {
    const { user, updateUser } = useAuth();
    const { i18n } = useTranslation();

    // Resolved synchronously: storage and navigator both answer at once, so the
    // first paint is already in the right language and there is nothing for the
    // application to wait on.
    const [language, setLanguageState] = useState<InterfaceLanguage>(
        () => readStoredLanguage() ?? browserLanguage(),
    );

    useEffect(() => {
        void i18n.changeLanguage(language);
    }, [i18n, language]);

    const persist = useCallback(
        async (next: InterfaceLanguage) => {
            try {
                const updated = await authApi.updateCurrentUser({ interface_lang: next });
                updateUser(updated);
            } catch (error) {
                // The locale still holds here and in storage; only the account
                // is behind, and the next change will try again.
                console.error('Error updating interface language', error);
            }
        },
        [updateUser],
    );

    const setLanguage = useCallback(
        (next: InterfaceLanguage) => {
            chosenThisSession = true;
            storeLanguage(next);
            setLanguageState(next);
            if (user) {
                void persist(next);
            }
        },
        [persist, user],
    );

    // Reconciling with the account happens once per sign-in, not on every change
    // to the user object — persisting updates it, and a second pass would read
    // back what it had just written.
    const reconciled = useRef(false);
    const current = useRef(language);
    current.current = language;

    useEffect(() => {
        if (!user) {
            reconciled.current = false;
            return;
        }
        if (reconciled.current) {
            return;
        }
        reconciled.current = true;

        const fromAccount = isInterfaceLanguage(user.interface_lang) ? user.interface_lang : null;

        // A choice just made by hand outranks the stored one: undoing it a
        // moment after the reader made it is the worst of the options.
        if (chosenThisSession) {
            if (fromAccount !== current.current) {
                void persist(current.current);
            }
            return;
        }

        if (fromAccount) {
            storeLanguage(fromAccount);
            setLanguageState(fromAccount);
            return;
        }

        // No locale on the account yet. Write down what was already happening,
        // so that shipping this moves nobody's interface.
        const derived = languageFromBooks(user.books_lang);
        storeLanguage(derived);
        setLanguageState(derived);
        void persist(derived);
    }, [user, persist]);

    const value = useMemo(() => ({ language, setLanguage }), [language, setLanguage]);

    return (
        <InterfaceLanguageContext.Provider value={value}>
            {children}
        </InterfaceLanguageContext.Provider>
    );
};

export const useInterfaceLanguage = (): InterfaceLanguageContextValue => {
    const context = useContext(InterfaceLanguageContext);
    if (!context) {
        throw new Error('useInterfaceLanguage must be used within an InterfaceLanguageProvider');
    }
    return context;
};
