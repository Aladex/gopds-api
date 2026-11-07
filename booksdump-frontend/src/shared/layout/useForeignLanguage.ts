import { useTranslation } from 'react-i18next';

import { useSearchBar } from '@/context/SearchBarContext';
import { ALL_LANGUAGES } from '@/shared/lib/languageUtils';

/** Whether the books language is one other than the language of the interface. */
export function useForeignLanguage(): boolean {
    const { i18n } = useTranslation();
    const { selectedLanguage } = useSearchBar();
    return (selectedLanguage || ALL_LANGUAGES) !== i18n.language;
}
