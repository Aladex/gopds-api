// Language utilities - flags and names
export interface LanguageInfo {
    code: string;
    name: string;
    flag: string;
}

// Mapping of ISO language codes to their names and flags
export const languageMapping: Record<string, LanguageInfo> = {
    'aa': { code: 'aa', name: 'Afar', flag: '🇪🇹' },
    'ab': { code: 'ab', name: 'Абхазский', flag: '🇬🇪' },
    'ae': { code: 'ae', name: 'Avestan', flag: '🇮🇷' },
    'af': { code: 'af', name: 'Afrikaans', flag: '🇿🇦' },
    'ak': { code: 'ak', name: 'Akan', flag: '🇬🇭' },
    'am': { code: 'am', name: 'አማርኛ', flag: '🇪🇹' },
    'an': { code: 'an', name: 'Aragonés', flag: '🇪🇸' },
    'ar': { code: 'ar', name: 'العربية', flag: '🇸🇦' },
    'as': { code: 'as', name: 'অসমীয়া', flag: '🇮🇳' },
    'av': { code: 'av', name: 'Аварский', flag: '🇷🇺' },
    'ay': { code: 'ay', name: 'Aymara', flag: '🇧🇴' },
    'az': { code: 'az', name: 'Azərbaycan', flag: '🇦🇿' },
    'ba': { code: 'ba', name: 'Башҡорт', flag: '🇷🇺' },
    'be': { code: 'be', name: 'Беларуская', flag: '🇧🇾' },
    'bg': { code: 'bg', name: 'Български', flag: '🇧🇬' },
    'bi': { code: 'bi', name: 'Bislama', flag: '🇻🇺' },
    'bm': { code: 'bm', name: 'Bambara', flag: '🇲🇱' },
    'bn': { code: 'bn', name: 'বাংলা', flag: '🇧🇩' },
    'bo': { code: 'bo', name: 'བོད་ཡིག', flag: '🇨🇳' },
    'br': { code: 'br', name: 'Brezhoneg', flag: '🇫🇷' },
    'bs': { code: 'bs', name: 'Bosanski', flag: '🇧🇦' },
    'ca': { code: 'ca', name: 'Català', flag: '🇪🇸' },
    'ce': { code: 'ce', name: 'Нохчийн', flag: '🇷🇺' },
    'ch': { code: 'ch', name: 'Chamoru', flag: '🇬🇺' },
    'co': { code: 'co', name: 'Corsu', flag: '🇫🇷' },
    'cr': { code: 'cr', name: 'Cree', flag: '🇨🇦' },
    'cs': { code: 'cs', name: 'Čeština', flag: '🇨🇿' },
    'cu': { code: 'cu', name: 'Словѣньскъ', flag: '🇷🇺' },
    'cv': { code: 'cv', name: 'Чӑваш', flag: '🇷🇺' },
    'cy': { code: 'cy', name: 'Cymraeg', flag: '🇬🇧' },
    'da': { code: 'da', name: 'Dansk', flag: '🇩🇰' },
    'de': { code: 'de', name: 'Deutsch', flag: '🇩🇪' },
    'dv': { code: 'dv', name: 'ދިވެހި', flag: '🇲🇻' },
    'dz': { code: 'dz', name: 'ཇོང་ཁ', flag: '🇧🇹' },
    'ee': { code: 'ee', name: 'Eʋegbe', flag: '🇹🇬' },
    'el': { code: 'el', name: 'Ελληνικά', flag: '🇬🇷' },
    'en': { code: 'en', name: 'English', flag: '🇬🇧' },
    'eo': { code: 'eo', name: 'Esperanto', flag: '🌐' },
    'es': { code: 'es', name: 'Español', flag: '🇪🇸' },
    'et': { code: 'et', name: 'Eesti', flag: '🇪🇪' },
    'eu': { code: 'eu', name: 'Euskara', flag: '🇪🇸' },
    'fa': { code: 'fa', name: 'فارسی', flag: '🇮🇷' },
    'ff': { code: 'ff', name: 'Fulfulde', flag: '🇸🇳' },
    'fi': { code: 'fi', name: 'Suomi', flag: '🇫🇮' },
    'fj': { code: 'fj', name: 'Na Vosa Vakaviti', flag: '🇫🇯' },
    'fo': { code: 'fo', name: 'Føroyskt', flag: '🇫🇴' },
    'fr': { code: 'fr', name: 'Français', flag: '🇫🇷' },
    'fy': { code: 'fy', name: 'Frysk', flag: '🇳🇱' },
    'ga': { code: 'ga', name: 'Gaelige', flag: '🇮🇪' },
    'gd': { code: 'gd', name: 'Gàidhlig', flag: '🇬🇧' },
    'gl': { code: 'gl', name: 'Galego', flag: '🇪🇸' },
    'gn': { code: 'gn', name: 'Avañeʼẽ', flag: '🇵🇾' },
    'gu': { code: 'gu', name: 'ગુજરાતી', flag: '🇮🇳' },
    'gv': { code: 'gv', name: 'Gaelg', flag: '🇮🇲' },
    'ha': { code: 'ha', name: 'Hausa', flag: '🇳🇬' },
    'he': { code: 'he', name: 'עברית', flag: '🇮🇱' },
    'hi': { code: 'hi', name: 'हिन्दी', flag: '🇮🇳' },
    'ho': { code: 'ho', name: 'Hiri Motu', flag: '🇵🇬' },
    'hr': { code: 'hr', name: 'Hrvatski', flag: '🇭🇷' },
    'ht': { code: 'ht', name: 'Kreyòl Ayisyen', flag: '🇭🇹' },
    'hu': { code: 'hu', name: 'Magyar', flag: '🇭🇺' },
    'hy': { code: 'hy', name: 'Հայերեն', flag: '🇦🇲' },
    'hz': { code: 'hz', name: 'Otjiherero', flag: '🇳🇦' },
    'ia': { code: 'ia', name: 'Interlingua', flag: '🌐' },
    'id': { code: 'id', name: 'Bahasa Indonesia', flag: '🇮🇩' },
    'ie': { code: 'ie', name: 'Interlingue', flag: '🌐' },
    'ig': { code: 'ig', name: 'Igbo', flag: '🇳🇬' },
    'ii': { code: 'ii', name: 'ꆈꌠ꒿', flag: '🇨🇳' },
    'ik': { code: 'ik', name: 'Iñupiaq', flag: '🇺🇸' },
    'io': { code: 'io', name: 'Ido', flag: '🌐' },
    'is': { code: 'is', name: 'Íslenska', flag: '🇮🇸' },
    'it': { code: 'it', name: 'Italiano', flag: '🇮🇹' },
    'iu': { code: 'iu', name: 'ᐃᓄᒃᑎᑐᑦ', flag: '🇨🇦' },
    'ja': { code: 'ja', name: '日本語', flag: '🇯🇵' },
    'jv': { code: 'jv', name: 'Basa Jawa', flag: '🇮🇩' },
    'ka': { code: 'ka', name: 'ქართული', flag: '🇬🇪' },
    'kg': { code: 'kg', name: 'Kikongo', flag: '🇨🇩' },
    'ki': { code: 'ki', name: 'Gikuyu', flag: '🇰🇪' },
    'kj': { code: 'kj', name: 'Kuanyama', flag: '🇳🇦' },
    'kk': { code: 'kk', name: 'Қазақша', flag: '🇰🇿' },
    'kl': { code: 'kl', name: 'Kalaallisut', flag: '🇬🇱' },
    'km': { code: 'km', name: 'ខ្មែរ', flag: '🇰🇭' },
    'kn': { code: 'kn', name: 'ಕನ್ನಡ', flag: '🇮🇳' },
    'ko': { code: 'ko', name: '한국어', flag: '🇰🇷' },
    'kr': { code: 'kr', name: 'Kanuri', flag: '🇳🇬' },
    'ks': { code: 'ks', name: 'कश्मीरी', flag: '🇮🇳' },
    'ku': { code: 'ku', name: 'Kurdî', flag: '🇮🇶' },
    'kv': { code: 'kv', name: 'Коми', flag: '🇷🇺' },
    'kw': { code: 'kw', name: 'Kernewek', flag: '🇬🇧' },
    'ky': { code: 'ky', name: 'Кыргызча', flag: '🇰🇬' },
    'la': { code: 'la', name: 'Latina', flag: '🇻🇦' },
    'lb': { code: 'lb', name: 'Lëtzebuergesch', flag: '🇱🇺' },
    'lg': { code: 'lg', name: 'Luganda', flag: '🇺🇬' },
    'li': { code: 'li', name: 'Limburgs', flag: '🇳🇱' },
    'ln': { code: 'ln', name: 'Lingala', flag: '🇨🇩' },
    'lo': { code: 'lo', name: 'ລາວ', flag: '🇱🇦' },
    'lt': { code: 'lt', name: 'Lietuvių', flag: '🇱🇹' },
    'lu': { code: 'lu', name: 'Luba-Katanga', flag: '🇨🇩' },
    'lv': { code: 'lv', name: 'Latviešu', flag: '🇱🇻' },
    'mg': { code: 'mg', name: 'Malagasy', flag: '🇲🇬' },
    'mh': { code: 'mh', name: 'Kajin M̧ajeļ', flag: '🇲🇭' },
    'mi': { code: 'mi', name: 'Māori', flag: '🇳🇿' },
    'mk': { code: 'mk', name: 'Македонски', flag: '🇲🇰' },
    'ml': { code: 'ml', name: 'മലയാളം', flag: '🇮🇳' },
    'mn': { code: 'mn', name: 'Монгол', flag: '🇲🇳' },
    'mr': { code: 'mr', name: 'मराठी', flag: '🇮🇳' },
    'ms': { code: 'ms', name: 'Bahasa Melayu', flag: '🇲🇾' },
    'mt': { code: 'mt', name: 'Malti', flag: '🇲🇹' },
    'my': { code: 'my', name: 'မြန်မာ', flag: '🇲🇲' },
    'na': { code: 'na', name: 'Nauru', flag: '🇳🇷' },
    'nb': { code: 'nb', name: 'Norsk Bokmål', flag: '🇳🇴' },
    'nd': { code: 'nd', name: 'isiNdebele', flag: '🇿🇼' },
    'ne': { code: 'ne', name: '���ेपाली', flag: '🇳🇵' },
    'ng': { code: 'ng', name: 'Ndonga', flag: '🇳🇦' },
    'nl': { code: 'nl', name: 'Nederlands', flag: '🇳🇱' },
    'nn': { code: 'nn', name: 'Nynorsk', flag: '🇳🇴' },
    'no': { code: 'no', name: 'Norsk', flag: '🇳🇴' },
    'nr': { code: 'nr', name: 'isiNdebele', flag: '🇿🇦' },
    'nv': { code: 'nv', name: 'Diné bizaad', flag: '🇺🇸' },
    'ny': { code: 'ny', name: 'Chichewa', flag: '🇲🇼' },
    'oc': { code: 'oc', name: 'Occitan', flag: '🇫🇷' },
    'oj': { code: 'oj', name: 'Ojibwe', flag: '🇨🇦' },
    'om': { code: 'om', name: 'Oromoo', flag: '🇪🇹' },
    'or': { code: 'or', name: 'ଓଡ଼ିଆ', flag: '🇮🇳' },
    'os': { code: 'os', name: 'Ирон', flag: '🇬🇪' },
    'pa': { code: 'pa', name: 'ਪੰਜਾਬੀ', flag: '🇮🇳' },
    'pi': { code: 'pi', name: 'Pāli', flag: '🇮🇳' },
    'pl': { code: 'pl', name: 'Polski', flag: '🇵🇱' },
    'ps': { code: 'ps', name: 'پښتو', flag: '🇦🇫' },
    'pt': { code: 'pt', name: 'Português', flag: '🇵🇹' },
    'qu': { code: 'qu', name: 'Runa Simi', flag: '🇵🇪' },
    'rm': { code: 'rm', name: 'Rumantsch', flag: '🇨🇭' },
    'rn': { code: 'rn', name: 'Kirundi', flag: '🇧🇮' },
    'ro': { code: 'ro', name: 'Română', flag: '🇷🇴' },
    'ru': { code: 'ru', name: 'Русский', flag: '🇷🇺' },
    'rw': { code: 'rw', name: 'Kinyarwanda', flag: '🇷🇼' },
    'sa': { code: 'sa', name: 'संस्कृतम्', flag: '🇮🇳' },
    'sc': { code: 'sc', name: 'Sardu', flag: '🇮🇹' },
    'sd': { code: 'sd', name: 'سنڌي', flag: '🇵🇰' },
    'se': { code: 'se', name: 'Sámi', flag: '🇳🇴' },
    'sg': { code: 'sg', name: 'Sango', flag: '🇨🇫' },
    'si': { code: 'si', name: 'සිංහල', flag: '🇱🇰' },
    'sk': { code: 'sk', name: 'Slovenčina', flag: '🇸🇰' },
    'sl': { code: 'sl', name: 'Slovenščina', flag: '🇸🇮' },
    'sm': { code: 'sm', name: 'Gagana Samoa', flag: '🇼🇸' },
    'sn': { code: 'sn', name: 'Shona', flag: '🇿🇼' },
    'so': { code: 'so', name: 'Soomaali', flag: '🇸🇴' },
    'sq': { code: 'sq', name: 'Shqip', flag: '🇦🇱' },
    'sr': { code: 'sr', name: 'Српски', flag: '🇷🇸' },
    'ss': { code: 'ss', name: 'siSwati', flag: '🇸🇿' },
    'st': { code: 'st', name: 'Sesotho', flag: '🇱🇸' },
    'su': { code: 'su', name: 'Basa Sunda', flag: '🇮🇩' },
    'sv': { code: 'sv', name: 'Svenska', flag: '🇸🇪' },
    'sw': { code: 'sw', name: 'Kiswahili', flag: '🇹🇿' },
    'ta': { code: 'ta', name: 'தமிழ்', flag: '🇮🇳' },
    'te': { code: 'te', name: 'తెలుగు', flag: '🇮🇳' },
    'tg': { code: 'tg', name: 'Тоҷикӣ', flag: '🇹🇯' },
    'th': { code: 'th', name: 'ไทย', flag: '🇹🇭' },
    'ti': { code: 'ti', name: 'ትግርኛ', flag: '🇪🇷' },
    'tk': { code: 'tk', name: 'Türkmen', flag: '🇹🇲' },
    'tl': { code: 'tl', name: 'Filipino', flag: '🇵🇭' },
    'tn': { code: 'tn', name: 'Setswana', flag: '🇧🇼' },
    'to': { code: 'to', name: 'Lea Faka-Tonga', flag: '🇹🇴' },
    'tr': { code: 'tr', name: 'Türkçe', flag: '🇹🇷' },
    'ts': { code: 'ts', name: 'Xitsonga', flag: '🇿🇦' },
    'tt': { code: 'tt', name: 'Татар', flag: '🇷🇺' },
    'tw': { code: 'tw', name: 'Twi', flag: '🇬🇭' },
    'ty': { code: 'ty', name: 'Reo Tahiti', flag: '🇵🇫' },
    'ug': { code: 'ug', name: 'ئۇيغۇرچە', flag: '🇨🇳' },
    'uk': { code: 'uk', name: 'Українська', flag: '🇺🇦' },
    'ur': { code: 'ur', name: 'اردو', flag: '🇵🇰' },
    'uz': { code: 'uz', name: 'Oʻzbek', flag: '🇺🇿' },
    've': { code: 've', name: 'Tshivenda', flag: '🇿🇦' },
    'vi': { code: 'vi', name: 'Tiếng Việt', flag: '🇻🇳' },
    'vo': { code: 'vo', name: 'Volapük', flag: '🌐' },
    'wo': { code: 'wo', name: 'Wolof', flag: '🇸🇳' },
    'xh': { code: 'xh', name: 'isiXhosa', flag: '🇿🇦' },
    'yi': { code: 'yi', name: 'ייִדיש', flag: '🇮🇱' },
    'yo': { code: 'yo', name: 'Yorùbá', flag: '🇳🇬' },
    'za': { code: 'za', name: 'Zhuang', flag: '🇨🇳' },
    'zh': { code: 'zh', name: '中文', flag: '🇨🇳' },
    'zu': { code: 'zu', name: 'isiZulu', flag: '🇿🇦' },
};

/**
 * Get language information by ISO code
 */
export const getLanguageInfo = (code: string): LanguageInfo => {
    // Safe handling of incorrect values
    if (!code) {
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
 * Check if language code is in valid format (lowercase, 2-3 characters, ISO standard)
 */
export const isValidLanguageCodeFormat = (code: string): boolean => {
    if (!code) {
        return false;
    }

    // Check that the code consists only of lowercase letters
    if (code !== code.toLowerCase()) {
        return false;
    }

    // Check length (ISO codes are usually 2-3 characters)
    if (code.length < 2 || code.length > 3) {
        return false;
    }

    // Check that the code consists only of letters
    if (!/^[a-z]+$/.test(code)) {
        return false;
    }

    // Exclude explicitly incorrect codes
    const invalidCodes = ['unknown', 'ukr']; // ukr is not a standard ISO code
    if (invalidCodes.includes(code)) {
        return false;
    }

    return true;
};

/**
 * Check if language is supported
 */
export const isLanguageSupported = (code: string): boolean => {
    if (!code) {
        return false;
    }

    // First check format
    if (!isValidLanguageCodeFormat(code)) {
        return false;
    }

    const normalizedCode = code.toLowerCase().trim();
    return normalizedCode in languageMapping;
};

/**
 * Filter array of language codes to only include supported ones
 */
export const filterSupportedLanguages = (languages: string[]): string[] => {
    return languages.filter(lang => isLanguageSupported(lang));
};

/**
 * Get language display only if supported, otherwise return null
 */
export const getLanguageDisplaySafe = (code: string): string | null => {
    if (!code || !isLanguageSupported(code)) {
        return null;
    }
    const info = getLanguageInfo(code);
    return `${info.flag} ${info.name}`;
};
