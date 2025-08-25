// Test for demonstrating unsupported language handling
import { getLanguageInfo, getLanguageDisplay, isLanguageSupported } from './languageUtils';

// Testing examples
console.log('=== Supported languages ===');
console.log('ru:', getLanguageDisplay('ru')); // 🇷🇺 Russian
console.log('en:', getLanguageDisplay('en')); // 🇺🇸 English
console.log('de:', getLanguageDisplay('de')); // 🇩🇪 Deutsch

console.log('\n=== Unsupported languages ===');
console.log('xyz:', getLanguageDisplay('xyz')); // 🏳️ XYZ
console.log('unknown:', getLanguageDisplay('unknown')); // 🏳️ UNKNOWN
console.log('test123:', getLanguageDisplay('test123')); // 🏳️ TEST123

console.log('\n=== Support check ===');
console.log('ru is supported:', isLanguageSupported('ru')); // true
console.log('xyz is supported:', isLanguageSupported('xyz')); // false

// Demonstration of safe handling of undefined/null
console.log('\n=== Handling incorrect values ===');
console.log('empty string:', getLanguageDisplay('')); // 🏳️
console.log('undefined:', getLanguageDisplay(undefined as any)); // 🏳️ UNDEFINED
console.log('null:', getLanguageDisplay(null as any)); // 🏳️ NULL
