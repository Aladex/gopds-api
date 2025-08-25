// Language utilities - flags and names
export interface LanguageInfo {
    code: string;
    name: string;
    flag: string;
}

// Mapping of ISO language codes to their names and flags
export const languageMapping: Record<string, LanguageInfo> = {
    'ru': { code: 'ru', name: 'Русский', flag: '🇷🇺' },
    'en': { code: 'en', name: 'English', flag: '🇺🇸' },
    'de': { code: 'de', name: 'Deutsch', flag: '🇩🇪' },
    'fr': { code: 'fr', name: 'Français', flag: '🇫🇷' },
    'es': { code: 'es', name: 'Español', flag: '🇪🇸' },
    'it': { code: 'it', name: 'Italiano', flag: '🇮🇹' },
    'pt': { code: 'pt', name: 'Português', flag: '🇵🇹' },
    'pl': { code: 'pl', name: 'Polski', flag: '🇵🇱' },
    'cs': { code: 'cs', name: 'Čeština', flag: '🇨🇿' },
    'sk': { code: 'sk', name: 'Slovenčina', flag: '🇸🇰' },
    'uk': { code: 'uk', name: 'Українська', flag: '🇺🇦' },
    'be': { code: 'be', name: 'Беларуская', flag: '🇧🇾' },
    'bg': { code: 'bg', name: 'Български', flag: '🇧🇬' },
    'hr': { code: 'hr', name: 'Hrvatski', flag: '🇭🇷' },
    'sr': { code: 'sr', name: 'Српски', flag: '🇷🇸' },
    'sl': { code: 'sl', name: 'Slovenščina', flag: '🇸🇮' },
    'mk': { code: 'mk', name: 'Македонски', flag: '🇲🇰' },
    'sq': { code: 'sq', name: 'Shqip', flag: '🇦🇱' },
    'et': { code: 'et', name: 'Eesti', flag: '🇪🇪' },
    'lv': { code: 'lv', name: 'Latviešu', flag: '🇱🇻' },
    'lt': { code: 'lt', name: 'Lietuvių', flag: '🇱🇹' },
    'fi': { code: 'fi', name: 'Suomi', flag: '🇫🇮' },
    'sv': { code: 'sv', name: 'Svenska', flag: '🇸🇪' },
    'no': { code: 'no', name: 'Norsk', flag: '🇳🇴' },
    'da': { code: 'da', name: 'Dansk', flag: '🇩🇰' },
    'nl': { code: 'nl', name: 'Nederlands', flag: '🇳🇱' },
    'hu': { code: 'hu', name: 'Magyar', flag: '🇭🇺' },
    'ro': { code: 'ro', name: 'Română', flag: '🇷🇴' },
    'el': { code: 'el', name: 'Ελληνικά', flag: '🇬🇷' },
    'tr': { code: 'tr', name: 'Türkçe', flag: '🇹🇷' },
    'ar': { code: 'ar', name: 'العربية', flag: '🇸🇦' },
    'he': { code: 'he', name: 'עברית', flag: '🇮🇱' },
    'hi': { code: 'hi', name: 'हिन्दी', flag: '🇮🇳' },
    'zh': { code: 'zh', name: '中文', flag: '🇨🇳' },
    'ja': { code: 'ja', name: '日本語', flag: '🇯🇵' },
    'ko': { code: 'ko', name: '한국어', flag: '🇰🇷' },
    'th': { code: 'th', name: 'ไทย', flag: '🇹🇭' },
    'vi': { code: 'vi', name: 'Tiếng Việt', flag: '🇻🇳' },
    'id': { code: 'id', name: 'Bahasa Indonesia', flag: '🇮🇩' },
    'ms': { code: 'ms', name: 'Bahasa Melayu', flag: '🇲🇾' },
    'tl': { code: 'tl', name: 'Filipino', flag: '🇵🇭' },
    'fa': { code: 'fa', name: 'فارسی', flag: '🇮🇷' },
    'ur': { code: 'ur', name: 'اردو', flag: '🇵🇰' },
    'bn': { code: 'bn', name: 'বাংলা', flag: '🇧🇩' },
    'ta': { code: 'ta', name: 'தமிழ்', flag: '🇮🇳' },
    'te': { code: 'te', name: 'తెలుగు', flag: '🇮🇳' },
    'ml': { code: 'ml', name: 'മലയാളം', flag: '🇮🇳' },
    'kn': { code: 'kn', name: 'ಕನ್ನಡ', flag: '🇮🇳' },
    'gu': { code: 'gu', name: 'ગુજરાતી', flag: '🇮🇳' },
    'pa': { code: 'pa', name: 'ਪੰਜਾਬੀ', flag: '🇮🇳' },
    'or': { code: 'or', name: 'ଓଡ଼ିଆ', flag: '🇮🇳' },
    'as': { code: 'as', name: 'অসমীয়া', flag: '🇮🇳' },
    'mr': { code: 'mr', name: 'मराठी', flag: '🇮🇳' },
    'ne': { code: 'ne', name: 'नेपाली', flag: '🇳🇵' },
    'si': { code: 'si', name: 'සිංහල', flag: '🇱🇰' },
    'my': { code: 'my', name: 'မြန်မာ', flag: '🇲🇲' },
    'km': { code: 'km', name: 'ខ្មែរ', flag: '🇰🇭' },
    'lo': { code: 'lo', name: 'ລາວ', flag: '🇱🇦' },
    'ka': { code: 'ka', name: 'ქართული', flag: '🇬🇪' },
    'hy': { code: 'hy', name: 'Հայերեն', flag: '🇦🇲' },
    'az': { code: 'az', name: 'Azərbaycan', flag: '🇦🇿' },
    'kk': { code: 'kk', name: 'Қазақша', flag: '🇰🇿' },
    'ky': { code: 'ky', name: 'Кыргызча', flag: '🇰🇬' },
    'uz': { code: 'uz', name: 'O\'zbek', flag: '🇺🇿' },
    'tg': { code: 'tg', name: 'Тоҷикӣ', flag: '🇹🇯' },
    'tk': { code: 'tk', name: 'Türkmen', flag: '🇹🇲' },
    'mn': { code: 'mn', name: 'Монгол', flag: '🇲🇳' },
    'tt': { code: 'tt', name: 'Татар', flag: '🇷🇺' },
    'ba': { code: 'ba', name: 'Башҡорт', flag: '🇷🇺' },
    'cv': { code: 'cv', name: 'Чӑваш', flag: '🇷🇺' },
    'udm': { code: 'udm', name: 'Удмурт', flag: '🇷🇺' },
    'chm': { code: 'chm', name: 'Марий', flag: '🇷🇺' },
};

/**
 * Get language information by ISO code
 */
export const getLanguageInfo = (code: string): LanguageInfo => {
    // Safe handling of incorrect values
    if (!code || typeof code !== 'string') {
        return {
            code: 'unknown',
            name: 'UNKNOWN',
            flag: '🏳️'
        };
    }

    // Convert to lowercase for search
    const normalizedCode = code.toLowerCase().trim();

    return languageMapping[normalizedCode] || {
        code: normalizedCode,
        name: normalizedCode.toUpperCase(),
        flag: '🏳️'
    };
};

/**
 * Get formatted language display with flag
 */
export const getLanguageDisplay = (code: string): string => {
    const info = getLanguageInfo(code);
    return `${info.flag} ${info.name}`;
};

/**
 * Get list of all available languages with information
 */
export const getAllLanguagesInfo = (): LanguageInfo[] => {
    return Object.values(languageMapping);
};

/**
 * Check if language is supported
 */
export const isLanguageSupported = (code: string): boolean => {
    return code in languageMapping;
};
