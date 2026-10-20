package screens

import (
	"strings"
	"testing"

	"gopds-api/config"
	"gopds-api/telegram/cbv2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// texts are a screen's button labels, row by row.
func texts(s *Screen) [][]string {
	out := make([][]string, 0, len(s.Keys))
	for _, row := range s.Keys {
		var line []string
		for _, b := range row {
			line = append(line, b.Text)
		}
		out = append(out, line)
	}
	return out
}

func setEnv(string) *cbv2.Envelope { return env() }

// A value to copy is written out and has a copy button; a link is a URL
// button; nothing else carries the value.
func TestDonateShowsValuesAndCopyButtons(t *testing.T) {
	s := DonateScreen([]config.DonateMethod{
		{ID: "btc", Label: "Bitcoin", Kind: "address", Value: "bc1qexample"},
		{ID: "boosty", Label: "Boosty", Kind: "link", Value: "https://boosty.to/x"},
	}, env())
	assert.Contains(t, s.Doc.Plain(), "<code>bc1qexample</code>")
	require.Len(t, s.Keys, 3)
	assert.Equal(t, "bc1qexample", s.Keys[0][0].CopyText)
	assert.Equal(t, "https://boosty.to/x", s.Keys[1][0].URL)
	assert.Equal(t, []string{ButtonToSettings}, texts(&s)[2])
}

// The language pager: ten a page, two a row, paged, and a way back.
func TestTheLanguagePagerPages(t *testing.T) {
	codes := []string{"ru", "en", "uk", "de", "fr", "es", "it", "pl", "be", "kk", "ja", "zh"}
	first := LanguagePagerScreen(codes, 0, "ru", setEnv, func(int) *cbv2.Envelope { return env() }, Button{Text: ButtonBack, Env: env()})
	labels := texts(&first)
	assert.Equal(t, []string{"✓ Русский", "English"}, labels[0])
	assert.Len(t, labels, 5+2, "five rows of two, the pager, the way back")
	assert.Contains(t, first.Doc.Plain(), "Страница 1 из 2")
	second := LanguagePagerScreen(codes, 5, "", setEnv, func(int) *cbv2.Envelope { return env() }, Button{Text: ButtonBack, Env: env()})
	assert.Contains(t, second.Doc.Plain(), "Страница 2 из 2", "a page past the end is the last")
}

// Settings name their values; «Поддержать проект» only with donation methods.
func TestSettingsShowTheirValues(t *testing.T) {
	k := &SettingsKeys{Format: env(), Language: env(), Style: env(), Inline: env(), Recent: env(), Home: env()}
	s := SettingsScreen("fb2", "", true, false, true, "", k)
	plain := s.Doc.Plain()
	for _, want := range []string{
		"Основной формат: FB2", "Язык поиска: все языки", "Вид карточек: простой", "Подсказки при поиске: выключены",
		"Kindle: не настроен", "Недавние отправки: сохранять",
	} {
		assert.Contains(t, plain, want)
	}
	for _, row := range texts(&s) {
		assert.NotContains(t, row, ButtonDonate)
	}
	k.Donate = env()
	s = SettingsScreen("epub", "ru", false, true, false, "reader@kindle.com", k)
	assert.Contains(t, s.Doc.Plain(), "Подсказки при поиске: включены")
	assert.Contains(t, s.Doc.Plain(), "Недавние отправки: не сохранять")
	assert.Contains(t, texts(&s), []string{ButtonRecent})
	assert.Contains(t, s.Doc.Plain(), "Язык поиска: русский")
	assert.Contains(t, texts(&s), []string{ButtonDonate})
}

// The inline instructions keep the command inside code.
func TestTheInlineInstructionsKeepTheCommandInCode(t *testing.T) {
	s := InlineScreen("my_library_bot", InlineOff, &InlineKeys{Check: env(), Back: env(), Home: env()})
	plain := s.Doc.Plain()
	assert.Contains(t, plain, "<code>/setinline</code>")
	assert.Contains(t, plain, "@my_library_bot")
	assert.Equal(t, 1, strings.Count(plain, "/setinline"), "never a bare, tappable command")
}

// WithBack puts the way back before «Домой».
func TestWithBackPrependsToTheLastRow(t *testing.T) {
	s := Notice(CopyCollectionGone, env()).WithBack(&Button{Text: ButtonToCollections, Env: env()})
	assert.Equal(t, [][]string{{ButtonToCollections, ButtonHome}}, texts(&s))
	same := Notice(CopyCollectionGone, env()).WithBack(nil)
	assert.Equal(t, [][]string{{ButtonHome}}, texts(&same))
}

// With inline mode off: the steps and «Проверить»; on: how to use it; just
// found on: that, and the way home.
func TestTheInlineScreenFollowsTheMode(t *testing.T) {
	k := &InlineKeys{Check: env(), Back: env(), Home: env()}
	off := InlineScreen("lib_bot", InlineOff, k)
	assert.Contains(t, texts(&off), []string{ButtonCheckInline})
	assert.Contains(t, off.Doc.Plain(), "нажми «Проверить»")
	on := InlineScreen("lib_bot", InlineOn, k)
	assert.Contains(t, on.Doc.Plain(), "Подсказки при поиске включены")
	assert.Contains(t, on.Doc.Plain(), "@lib_bot")
	assert.Equal(t, [][]string{{ButtonToSettings}}, texts(&on))
	just := InlineScreen("lib_bot", InlineJustOn, k)
	assert.Equal(t, CopyInlineJustOn, strings.TrimSpace(just.Doc.Plain()))
	assert.Equal(t, [][]string{{ButtonHome}}, texts(&just))
}

// The home offers inline search first, only while it is on.
func TestTheHomeOffersInlineSearchWhenOn(t *testing.T) {
	k := &HomeKeys{Favorites: env(), Recent: env(), Collections: env(), Settings: env()}
	off := HomeScreen(false, false, k)
	assert.Equal(t, [][]string{{ButtonFavorites, ButtonRecent}, {ButtonCollections, ButtonSettings}}, texts(&off))
	on := HomeScreen(false, true, k)
	require.True(t, on.Keys[0][0].SwitchInline)
	assert.Equal(t, ButtonInlineSearch, on.Keys[0][0].Text)
	assert.Nil(t, on.Keys[0][0].Env)
}
