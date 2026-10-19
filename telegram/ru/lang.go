package ru

import "strings"

// langCodes is the frozen table of the book languages a button can name: a
// language's id is its position plus one, 0 meaning all languages. Append
// only: a button sent today may be pressed after the next release. The codes
// are the site's (booksdump-frontend languageMapping), in its order.
//
//nolint:goconst // a table of codes, not repeated words
var langCodes = []string{
	"aa", "ab", "ae", "af", "ak", "am", "an", "ar", "as", "av", "ay", "az", "ba", "be", "bg",
	"bi", "bm", "bn", "bo", "br", "bs", "ca", "ce", "ch", "co", "cr", "cs", "cu", "cv", "cy",
	"da", "de", "dv", "dz", "ee", "el", "en", "eo", "es", "et", "eu", "fa", "ff", "fi", "fj",
	"fo", "fr", "fy", "ga", "gd", "gl", "gn", "gu", "gv", "ha", "he", "hi", "ho", "hr", "ht",
	"hu", "hy", "hz", "ia", "id", "ie", "ig", "ii", "ik", "io", "is", "it", "iu", "ja", "jv",
	"ka", "kg", "ki", "kj", "kk", "kl", "km", "kn", "ko", "kr", "ks", "ku", "kv", "kw", "ky",
	"la", "lb", "lg", "li", "ln", "lo", "lt", "lu", "lv", "mg", "mh", "mi", "mk", "ml", "mn",
	"mr", "ms", "mt", "my", "na", "nb", "nd", "ne", "ng", "nl", "nn", "no", "nr", "nv", "ny",
	"oc", "oj", "om", "or", "os", "pa", "pi", "pl", "ps", "pt", "qu", "rm", "rn", "ro", "ru",
	"rw", "sa", "sc", "sd", "se", "sg", "si", "sk", "sl", "sm", "sn", "so", "sq", "sr", "ss",
	"st", "su", "sv", "sw", "ta", "te", "tg", "th", "ti", "tk", "tl", "tn", "to", "tr", "ts",
	"tt", "tw", "ty", "ug", "uk", "ur", "uz", "ve", "vi", "vo", "wo", "xh", "yi", "yo", "za",
	"zh", "zu",
}

// LangID is the button id of a language code; 0 for all languages, and for a
// code the table does not know — such a language is offered nowhere.
func LangID(code string) uint8 {
	code = strings.ToLower(strings.TrimSpace(code))
	for i, c := range langCodes {
		if c == code {
			return uint8(i + 1) // #nosec G115 -- the table has fewer than 255 codes
		}
	}
	return 0
}

// LangCode is LangID reversed; "" for all languages or an unknown id.
func LangCode(id uint8) string {
	if id == 0 || int(id) > len(langCodes) {
		return ""
	}
	return langCodes[id-1]
}

// KnownLanguage reports whether a code has a button id.
func KnownLanguage(code string) bool { return LangID(code) != 0 }
