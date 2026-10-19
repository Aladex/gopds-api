package cbv2

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	key  = []byte("navigation key of the test bot..")
	bind = BindCtx{TGBotID: 123456, OwnerTGID: 7001, ChatID: 7001}
)

func full() *Envelope {
	return &Envelope{Op: OpPage, EntityID: 1<<62 + 5, ViewID: 99, Page: maxPage, Arg: 255, ReturnID: 4242,
		Generation: 65535, Revision: 65534}
}

func TestEnvelopeRoundTripsWithinTelegramsLimit(t *testing.T) {
	s, err := Encode(full(), key, bind)
	require.NoError(t, err)
	assert.Len(t, s, 62)
	assert.LessOrEqual(t, len(s), 64)
	got, err := Decode(s, key, bind)
	require.NoError(t, err)
	assert.Equal(t, *full(), got)
}

func TestAButtonDoesNotVerifyElsewhere(t *testing.T) {
	s, err := Encode(full(), key, bind)
	require.NoError(t, err)
	for name, other := range map[string]BindCtx{
		"another bot":   {TGBotID: 1, OwnerTGID: bind.OwnerTGID, ChatID: bind.ChatID},
		"another owner": {TGBotID: bind.TGBotID, OwnerTGID: 1, ChatID: bind.ChatID},
		"another chat":  {TGBotID: bind.TGBotID, OwnerTGID: bind.OwnerTGID, ChatID: 1},
	} {
		_, otherErr := Decode(s, key, other)
		assert.ErrorIs(t, otherErr, ErrTag, name)
	}
	_, err = Decode(s, []byte("another key"), bind)
	assert.ErrorIs(t, err, ErrTag)
}

func TestEveryFlippedCharacterIsRefused(t *testing.T) {
	s, err := Encode(full(), key, bind)
	require.NoError(t, err)
	for i := len(prefix); i < len(s); i++ {
		flipped := []byte(s)
		if flipped[i] == 'A' {
			flipped[i] = 'B'
		} else {
			flipped[i] = 'A'
		}
		_, err := Decode(string(flipped), key, bind)
		assert.Error(t, err, "position %d", i)
	}
}

func TestShapesThatAreNotButtons(t *testing.T) {
	_, err := Decode("download:epub:5", key, bind)
	assert.ErrorIs(t, err, ErrVersion)
	_, err = Decode("2.short", key, bind)
	assert.ErrorIs(t, err, ErrMalformed)
	_, err = Decode("2."+strings.Repeat("!", 60), key, bind)
	assert.ErrorIs(t, err, ErrMalformed)
}

func TestOutOfRangeFieldsAreNotTruncated(t *testing.T) {
	for name, e := range map[string]*Envelope{
		"page":   {Op: OpPage, Page: maxPage + 1},
		"entity": {Op: OpOpenBook, EntityID: 1 << 63},
		"op":     {Op: opEnd},
		"zero":   {},
	} {
		_, err := Encode(e, key, bind)
		assert.ErrorIs(t, err, ErrMalformed, name)
	}
}
