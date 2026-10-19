package secrets

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := NewBox("service secret")
	require.NoError(t, err)

	keyID, sealed := box.Seal(PurposeToken, []byte("123:token"))
	assert.Equal(t, CurrentKeyID, keyID)
	assert.False(t, bytes.Contains(sealed, []byte("123:token")), "the token is not readable in the sealed value")

	plain, err := box.Open(keyID, PurposeToken, sealed)
	require.NoError(t, err)
	assert.Equal(t, "123:token", string(plain))
}

func TestSealIsRandomized(t *testing.T) {
	box, _ := NewBox("service secret")
	_, a := box.Seal(PurposeToken, []byte("same"))
	_, b := box.Seal(PurposeToken, []byte("same"))
	assert.NotEqual(t, a, b)
}

// A value sealed as one thing cannot be opened as another, nor with a key
// derived from a different secret_key.
func TestOpenRefusesTheWrongPurposeKeyOrID(t *testing.T) {
	box, _ := NewBox("service secret")
	keyID, sealed := box.Seal(PurposeToken, []byte("123:token"))

	_, err := box.Open(keyID, PurposeWebhookSecret, sealed)
	assert.Error(t, err)

	other, _ := NewBox("another secret")
	_, err = other.Open(keyID, PurposeToken, sealed)
	assert.Error(t, err)

	_, err = box.Open(keyID+1, PurposeToken, sealed)
	assert.ErrorIs(t, err, ErrUnknownKey)

	_, err = box.Open(keyID, PurposeToken, sealed[:3])
	assert.Error(t, err)
}

func TestNewBoxNeedsASecret(t *testing.T) {
	_, err := NewBox("")
	assert.Error(t, err)
}

func TestTokenHashIsStable(t *testing.T) {
	assert.Equal(t, TokenHash("a"), TokenHash("a"))
	assert.NotEqual(t, TokenHash("a"), TokenHash("b"))
	assert.Len(t, TokenHash("a"), 32)
}
