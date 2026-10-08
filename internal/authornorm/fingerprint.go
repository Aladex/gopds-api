package authornorm

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

// Version constants of the byte contracts. The domain prefixes and the fixed
// field order below together form version 1 of each digest; any change to
// them is a new contract version, not an edit of these values.
const (
	sourceFingerprintDomainV1 = "author-source-v1"
	normalizationKeyDomainV1  = "author-normalization-key-v1"

	presenceNull    = 0x00
	presencePresent = 0x01
)

// SourceFingerprint derives the v1 source fingerprint of a canonical source
// value. The byte stream is the ASCII domain prefix "author-source-v1"
// followed by the five fields in fixed order — first, middle, last, nickname,
// source display name — each serialized as a presence byte (0x00 NULL, 0x01
// present), an unsigned 64-bit big-endian length of the UTF-8 payload and the
// payload itself; a NULL field carries length zero and no payload. Role,
// position, book, title, ISBN, source ID, archive and every external fact are
// excluded by construction: the value type does not carry them.
func SourceFingerprint(v SourceValue) [32]byte {
	stream := []byte(sourceFingerprintDomainV1)
	stream = appendNullableField(stream, v.first)
	stream = appendNullableField(stream, v.middle)
	stream = appendNullableField(stream, v.last)
	stream = appendNullableField(stream, v.nickname)
	stream = appendNullableField(stream, &v.displayName)
	return sha256.Sum256(stream)
}

func appendNullableField(stream []byte, field *string) []byte {
	if field == nil {
		return append(stream, presenceNull, 0, 0, 0, 0, 0, 0, 0, 0)
	}
	stream = append(stream, presencePresent)
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(*field)))
	stream = append(stream, length[:]...)
	return append(stream, *field...)
}

// NormalizationKey derives the v1 normalization key from a raw source
// fingerprint and the exact extractor and normalizer versions. The byte
// stream is the ASCII domain prefix "author-normalization-key-v1" followed by
// three length-prefixed elements — the raw 32-byte fingerprint, the extractor
// version, the normalizer version — each an unsigned 64-bit big-endian length
// plus its bytes. Either version changing produces a different key while the
// source fingerprint stays fixed.
func NormalizationKey(fingerprint [32]byte, extractorVersion, normalizerVersion string) ([32]byte, error) {
	for _, version := range []string{extractorVersion, normalizerVersion} {
		if strings.TrimSpace(version) == "" {
			return [32]byte{}, ErrEmptyVersion
		}
	}
	stream := []byte(normalizationKeyDomainV1)
	for _, element := range [][]byte{fingerprint[:], []byte(extractorVersion), []byte(normalizerVersion)} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(element)))
		stream = append(stream, length[:]...)
		stream = append(stream, element...)
	}
	return sha256.Sum256(stream), nil
}
