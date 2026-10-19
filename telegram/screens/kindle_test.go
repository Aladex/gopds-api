package screens

import (
	"testing"

	"gopds-api/services/kindlesend"

	"github.com/stretchr/testify/assert"
)

// Every code the send can answer has its own words.
func TestEveryKindleOutcomeHasWords(t *testing.T) {
	seen := map[string]bool{}
	for _, code := range []string{"", KindleSending, kindlesend.CodeNotConfigured, kindlesend.CodeNotFound,
		kindlesend.CodeTooLarge, kindlesend.CodeConversionFailed, kindlesend.CodeSendFailed,
		kindlesend.CodeInProgress, kindlesend.CodeBusy} {
		text := KindleText(code)
		assert.NotEmpty(t, text, code)
		assert.False(t, seen[text], "%q shares its words", code)
		seen[text] = true
	}
	assert.Equal(t, KindleText(kindlesend.CodeSendFailed), KindleText("something new"))
}

// The confirmation names the book and the address, with «Отправить» and
// «Отмена».
func TestTheKindleConfirmation(t *testing.T) {
	s := KindleConfirmScreen("Солярис", "reader@kindle.com", env(), env())
	assert.Equal(t, "Отправить «Солярис» на Kindle reader@kindle.com?", s.Doc.Plain())
	assert.Equal(t, [][]string{{ButtonKindleSend, ButtonCancel}}, texts(&s))
}

// A missing address leads to the site's setup.
func TestAKindleStatusWithoutAnAddressLeadsToTheSite(t *testing.T) {
	s := KindleStatusScreen("Солярис", kindlesend.CodeNotConfigured, "https://site/?profile=kindle", env(), env())
	assert.Equal(t, "https://site/?profile=kindle", s.Keys[0][0].URL)
	sent := KindleStatusScreen("Солярис", "", "https://site/?profile=kindle", env(), env())
	assert.Contains(t, sent.Doc.Plain(), "Отправлено на Kindle")
	assert.Equal(t, [][]string{{ButtonToBook, ButtonHome}}, texts(&sent))
}

// «Kindle» sits beside the favorite, only with an address.
func TestTheCardOffersKindleWithAnAddress(t *testing.T) {
	k := *testCardKeys
	k.Kindle = env()
	without, _ := CardScreen(&Card{Book: inlineBook(), DefaultFormat: FormatEPUB}, &k)
	assert.Equal(t, []string{ButtonNotFavorite}, texts(&without)[1])
	with, _ := CardScreen(&Card{Book: inlineBook(), DefaultFormat: FormatEPUB, Kindle: true}, &k)
	assert.Equal(t, []string{ButtonNotFavorite, ButtonKindle}, texts(&with)[1])
}
