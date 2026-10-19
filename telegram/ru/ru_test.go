package ru

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestPluralForms(t *testing.T) {
	for n, want := range map[int]string{
		0: "0 книг", 1: "1 книга", 2: "2 книги", 4: "4 книги", 5: "5 книг", 11: "11 книг", 12: "12 книг",
		14: "14 книг", 21: "21 книга", 22: "22 книги", 101: "101 книга", 111: "111 книг", 1024: "1024 книги",
	} {
		assert.Equal(t, want, Books(n), "n=%d", n)
	}
}

func TestTruncateCountsCharactersNotBytes(t *testing.T) {
	assert.Equal(t, "Основание", Truncate("Основание", 9))
	got := Truncate("Основание и Империя", 10)
	assert.Equal(t, "Основание…", got)
	assert.Equal(t, 10, utf8.RuneCountInString(got))
	assert.Equal(t, "", Truncate("x", 0))
}

func TestTruncateSentenceEndsAtASentence(t *testing.T) {
	s := "Первое предложение аннотации. Второе предложение, длинное и без конца"
	assert.Equal(t, "Первое предложение аннотации. …", TruncateSentence(s, 50))
	assert.Equal(t, s, TruncateSentence(s, 500))
}

func TestLanguageNames(t *testing.T) {
	assert.Equal(t, "русский", Language("ru"))
	assert.Equal(t, "английский", Language(" EN "))
	assert.Equal(t, "XX", Language("xx"))
}

func TestLanguageIDsRoundTrip(t *testing.T) {
	assert.Equal(t, uint8(0), LangID(""))
	assert.Equal(t, "", LangCode(0))
	for _, code := range []string{"ru", "en", "uk", "zu", "aa"} {
		assert.Equal(t, code, LangCode(LangID(code)), code)
	}
	assert.Equal(t, uint8(0), LangID("xx"))
	assert.Less(t, len(langCodes), 255)
}

// The table is append-only: these ids are in buttons already sent.
func TestLanguageIDsAreFrozen(t *testing.T) {
	assert.Equal(t, uint8(1), LangID("aa"))
	assert.Equal(t, uint8(37), LangID("en"))
	assert.Equal(t, uint8(135), LangID("ru"))
}
