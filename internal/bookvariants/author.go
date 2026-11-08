package bookvariants

import (
	"regexp"
	"strings"
)

var (
	// genreSuffix is the « -- Кулинария» some placeholder names carry.
	genreSuffix = regexp.MustCompile(`\s+-{1,2}\s+.*$`)

	// placeholderWords are the words a name made only of is nobody in
	// particular: two books by «Автор неизвестен» are not by one author.
	placeholderWords = wordSet(
		"unknown", "unknow", "anonymous", "admin", "user", "author",
		"неизвестен", "неизвестный", "неизвестно", "неизвестна", "автор", "авторов", "аноним", "анонимус",
		"без", "коллектив", "группа", "неустановленный", "невідомий", "колектив", "авторів",
	)
)

// IsPlaceholderAuthor says whether an entity's name stands for no one: Unknown,
// Admin, «Автор неизвестен», «Коллектив авторов» and the like, with or without
// a genre after « -- ». A name with no word at all is one too.
func IsPlaceholderAuthor(displayName string) bool {
	words := strings.Fields(fold(genreSuffix.ReplaceAllString(displayName, "")))
	for _, w := range words {
		if !placeholderWords[w] {
			return false
		}
	}
	return true
}
