import { useAuth } from '@/context/AuthContext';
import { useSearchBar } from '@/context/SearchBarContext';

/**
 * useBookLanguage is the catalogue's language filter: what is on offer, what is
 * in force, and choosing another. A choice is written to the account, so every
 * control that offers it has to go through here or the two come apart.
 */
export function useBookLanguage() {
    const { languages, selectedLanguage, setSelectedLanguage } = useSearchBar();
    const { updateLang } = useAuth();

    const choose = (lang: string) => {
        updateLang(lang);
        setSelectedLanguage(lang);
    };

    return { languages, selected: selectedLanguage, choose };
}
