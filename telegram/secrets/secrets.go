// Package secrets keeps the bots' credentials unreadable at rest.
//
// A bot token is control of the bot; the webhook secret and the key that
// signs the bot's buttons are what keeps others from speaking for Telegram or
// for the owner. All three are stored sealed with AES-256-GCM under a key
// derived from the service's secret_key (HKDF-SHA256), so a database dump
// alone gives none of them away.
//
// Each sealed value names the key that sealed it (key_id). Today there is one;
// rotating secret_key means adding the new derivation as the next id and
// re-sealing, never reading the old values with the wrong key.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// CurrentKeyID is the key new values are sealed with.
const CurrentKeyID int16 = 1

// What a sealed value is for. It is authenticated with the value, so a
// token cannot be passed off as a webhook secret by swapping columns.
const (
	PurposeToken         = "token"
	PurposeWebhookSecret = "webhook-secret"
	PurposeNavKey        = "nav-key"
)

// info separates these keys from anything else derived from secret_key.
const info = "gopds telegram v1"

// keyBytes is the AES-256 key size.
const keyBytes = 32

// ErrUnknownKey is returned for a value sealed under a key this box lacks.
var ErrUnknownKey = errors.New("secrets: unknown key id")

// Box seals and opens values.
type Box struct {
	keys map[int16]cipher.AEAD
}

// NewBox derives the key from secretKey.
func NewBox(secretKey string) (*Box, error) {
	if secretKey == "" {
		return nil, errors.New("secrets: empty secret key")
	}
	key, err := hkdf.Key(sha256.New, []byte(secretKey), nil, info, keyBytes)
	if err != nil {
		return nil, fmt.Errorf("secrets: deriving the key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{keys: map[int16]cipher.AEAD{CurrentKeyID: aead}}, nil
}

// Seal encrypts plain for purpose under the current key. The result is the
// nonce followed by the ciphertext.
func (b *Box) Seal(purpose string, plain []byte) (keyID int16, sealed []byte) {
	aead := b.keys[CurrentKeyID]
	nonce := Random(aead.NonceSize())
	return CurrentKeyID, aead.Seal(nonce, nonce, plain, []byte(purpose))
}

// Open decrypts a value Seal produced for purpose under keyID.
func (b *Box) Open(keyID int16, purpose string, sealed []byte) ([]byte, error) {
	aead, ok := b.keys[keyID]
	if !ok {
		return nil, ErrUnknownKey
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("secrets: sealed value too short")
	}
	nonce, ct := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ct, []byte(purpose))
	if err != nil {
		return nil, fmt.Errorf("secrets: opening a %s: %w", purpose, err)
	}
	return plain, nil
}

// Random returns n bytes from the system's cryptographic source.
func Random(n int) []byte {
	b := make([]byte, n)
	// crypto/rand.Read never returns an error on supported platforms.
	_, _ = rand.Read(b)
	return b
}

// TokenHash is the lookup key of a token: its SHA-256.
func TokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
