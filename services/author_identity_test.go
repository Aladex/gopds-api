package services

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An author ID from a client is read as the identity in force names
// authors: a legacy number, or an entity's UUID. The other identity's ID is
// "not found" — never another author — and anything else is not an ID. No
// ID, or 0 (the feeds' "no author"), is no author under either.
func TestParseAuthorRef(t *testing.T) {
	entity := uuid.New()
	for _, tc := range []struct {
		raw      string
		entities bool
		want     AuthorRef
		err      error
	}{
		{raw: "", want: AuthorRef{}},
		{raw: "0", want: AuthorRef{}},
		{raw: "0", entities: true, want: AuthorRef{}},
		{raw: "42", want: AuthorRef{Legacy: 42}},
		{raw: "-3", want: AuthorRef{Legacy: -3}},
		{raw: " 42 ", want: AuthorRef{Legacy: 42}},
		{raw: entity.String(), entities: true, want: AuthorRef{Entity: entity}},
		{raw: entity.String(), err: ErrAuthorNotFound},
		{raw: "42", entities: true, err: ErrAuthorNotFound},
		{raw: "-3", entities: true, err: ErrAuthorNotFound},
		{raw: "Толстой", err: ErrInvalidAuthorID},
		{raw: "Толстой", entities: true, err: ErrInvalidAuthorID},
		{raw: "12ab", entities: true, err: ErrInvalidAuthorID},
		// The nil UUID is the "no entity" of the code, never an entity a
		// client can name: it is no author, not "no scope".
		{raw: uuid.Nil.String(), entities: true, err: ErrAuthorNotFound},
		{raw: uuid.Nil.String(), err: ErrAuthorNotFound},
	} {
		got, err := ParseAuthorRef(tc.raw, tc.entities)
		assert.ErrorIs(t, err, tc.err, "%q entities=%v", tc.raw, tc.entities)
		if tc.err == nil {
			assert.NoError(t, err, "%q entities=%v", tc.raw, tc.entities)
			assert.Equal(t, tc.want, got, "%q entities=%v", tc.raw, tc.entities)
		}
	}
	assert.True(t, AuthorRef{}.IsZero())
	assert.False(t, AuthorRef{Legacy: 1}.IsZero())
	assert.False(t, AuthorRef{Entity: entity}.IsZero())
}

// The nil UUID in any spelling — 32 zeros included, which as a number would
// be 0 — is an author that is not found under both identities, never the "no
// author" of the feeds. Otherwise the legacy identity reads a decimal number
// exactly as it always did (a zero-padded ID is still that ID, any zero is no
// author) and the entity identity reads a UUID spelling first, the literal
// "0" being its only "no author".
func TestEveryUUIDSpellingIsAUUID(t *testing.T) {
	live := uuid.MustParse("01a168d0-a113-741a-b69d-53b91f3c7eb9")
	spellings := func(id uuid.UUID) []string {
		canonical := id.String()
		compact := strings.ReplaceAll(canonical, "-", "")
		return []string{
			canonical, strings.ToUpper(canonical), "{" + canonical + "}",
			"urn:uuid:" + canonical, "URN:UUID:" + strings.ToUpper(canonical),
			compact, strings.ToUpper(compact),
		}
	}
	for _, raw := range spellings(uuid.Nil) {
		for _, entities := range []bool{true, false} {
			_, err := ParseAuthorRef(raw, entities)
			assert.ErrorIs(t, err, ErrAuthorNotFound, "%q entities=%v", raw, entities)
		}
	}
	for _, raw := range spellings(live) {
		got, err := ParseAuthorRef(raw, true)
		require.NoError(t, err, raw)
		assert.Equal(t, AuthorRef{Entity: live}, got, raw)
		_, err = ParseAuthorRef(raw, false)
		assert.ErrorIs(t, err, ErrAuthorNotFound, "%q under the legacy identity", raw)
	}

	got, err := ParseAuthorRef("0", true)
	require.NoError(t, err)
	assert.True(t, got.IsZero(), "the feeds' 0 is no author")

	// A legacy ID zero-padded to 32 digits is still that author under the
	// legacy identity; under entities it spells a UUID no entity has (the
	// surfaces' live check answers 404).
	padded := "00000000000000000000000000008671"
	got, err = ParseAuthorRef(padded, false)
	require.NoError(t, err)
	assert.Equal(t, AuthorRef{Legacy: 8671}, got)
	got, err = ParseAuthorRef(padded, true)
	require.NoError(t, err)
	assert.Equal(t, AuthorRef{Entity: uuid.MustParse(padded)}, got)
	for _, raw := range []string{"000", "-0", "+0"} {
		_, err = ParseAuthorRef(raw, true)
		assert.ErrorIs(t, err, ErrAuthorNotFound, "%q is a legacy number under entities", raw)
		got, err = ParseAuthorRef(raw, false)
		require.NoError(t, err, raw)
		assert.True(t, got.IsZero(), "%q reads as it always did under the legacy identity", raw)
	}
}
