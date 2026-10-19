// Package ru holds the Russian-language helpers of the bot's screens: plural
// forms, cutting text on character boundaries, language names.
package ru

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Plural picks the form of a counted noun: one for 1, 21, 101…; few for 2–4,
// 22–24…; many for the rest, 11–14 included.
//
//nolint:mnd // the numbers are the Russian plural rule itself
func Plural(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	mod100 := n % 100
	if mod100 >= 11 && mod100 <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

// Books is «N книга/книги/книг».
func Books(n int) string {
	return strconv.Itoa(n) + " " + Plural(n, "книга", "книги", "книг")
}

// Truncate cuts s to at most limit characters, ending a cut text with «…».
func Truncate(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return strings.TrimRightFunc(string(runes[:limit-1]), isSpace) + "…"
}

// TruncateSentence cuts s to at most limit characters at the last sentence end
// inside the limit, or at a word boundary when there is none, ending with «…».
func TruncateSentence(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	head := string(runes[:limit-1])
	if i := strings.LastIndexAny(head, ".!?…"); i > len(head)/2 {
		return head[:i+1] + " …"
	}
	if i := strings.LastIndexFunc(head, isSpace); i > len(head)/2 {
		return head[:i] + "…"
	}
	return head + "…"
}

func isSpace(r rune) bool { return r == ' ' || r == '\n' || r == '\t' }

// languages names the catalogue's common book languages the way a sentence
// does: «Язык: русский».
var languages = map[string]string{
	"ru": "русский", "en": "английский", "uk": "украинский", "be": "белорусский", "de": "немецкий",
	"fr": "французский", "es": "испанский", "it": "итальянский", "pl": "польский", "cs": "чешский",
	"bg": "болгарский", "sr": "сербский", "pt": "португальский", "nl": "нидерландский", "sv": "шведский",
	"fi": "финский", "lt": "литовский", "lv": "латышский", "et": "эстонский", "ka": "грузинский",
	"hy": "армянский", "kk": "казахский", "uz": "узбекский", "tt": "татарский", "he": "иврит",
	"la": "латынь", "eo": "эсперанто", "ja": "японский", "zh": "китайский",
}

// Language names a language code in Russian; an unknown code is shown as is,
// in capitals.
func Language(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if name, ok := languages[code]; ok {
		return name
	}
	return strings.ToUpper(code)
}
