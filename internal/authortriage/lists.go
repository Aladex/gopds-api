package authortriage

import (
	"strings"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm"
)

// placeholderPhrasesV1 are the search keys of displays that stand for no
// author, beside the normalizer's own vocabulary. Exact keys only: «Аноним
// X» is a person's handle.
var placeholderPhrasesV1 = setOf(
	"неизвестен автор", "неизвестный автор", "автор неизвестен", "неустановленный автор", "unknown",
	"unknown unknown", "unknown author", "anonymous", "аноним", "admin", "user", "guest", "автор", "без автора",
	"unbekannter autor", //nolint:misspell // a German placeholder phrase
	"невідомий автор", "неизвестен", "неизвестный", "автор не указан",
	"неизвестен неизвестен",
)

// collectivePhrasesV1 are the search keys of displays that name a collective
// the organization test does not see.
var collectivePhrasesV1 = setOf("коллектив авторов", "колектив авторів", "autoru kolektīvs", "народное творчество")

// placeholderKeys and placeholderPrefixes are the normalizer's placeholder
// vocabulary (authornorm.PlaceholderVocabulary).
var placeholderKeys, placeholderPrefixes = func() (map[string]bool, []string) {
	keys, prefixes := authornorm.PlaceholderVocabulary()
	return setOf(keys...), prefixes
}()

func setOf(words ...string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

// displayKey is a display's search key, outer parentheses dropped.
func displayKey(display string) string {
	return strings.TrimSpace(strings.Trim(authornorm.SearchKey(display), "()"))
}

// PlaceholderPhrase reports a display that stands for no author: its key is a
// key of the normalizer's placeholder vocabulary or of placeholderPhrasesV1,
// or it is one word that starts with a placeholder prefix («Неизвестного»).
// A longer name with a placeholder word in it is a name.
func PlaceholderPhrase(display string) bool {
	key := displayKey(display)
	if key == "" {
		return false
	}
	if placeholderKeys[key] || placeholderPhrasesV1[key] {
		return true
	}
	if strings.Contains(key, " ") {
		return false
	}
	for _, p := range placeholderPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// collectiveName reports the display of an organization or a collective
// phrase.
func collectiveName(display string) bool {
	return authorentity.OrganizationName(display) || collectivePhrasesV1[displayKey(display)]
}
