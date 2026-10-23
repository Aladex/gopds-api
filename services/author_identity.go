package services

import (
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// ErrAuthorNotFound is an author ID that names no author under the identity
// in force: an entity UUID while authors go by their legacy IDs, a legacy
// number while they go by their entities (design E, §4.2: no redirects, an
// old link is a 404), or an entity that is gone.
var ErrAuthorNotFound = errors.New("author not found")

// ErrInvalidAuthorID is an author ID that is neither a number nor a UUID.
var ErrInvalidAuthorID = errors.New("invalid author id")

// AuthorRef names an author as a client sent it: a legacy catalog author or
// an author entity, at most one of them; neither is no author.
type AuthorRef struct {
	Legacy int64
	Entity uuid.UUID
}

// IsZero reports whether the reference names no author.
func (r AuthorRef) IsZero() bool { return r.Legacy == 0 && r.Entity == uuid.Nil }

// noAuthor is how the feeds have always spelled "no author".
const noAuthor = "0"

// ParseAuthorRef reads a client's author ID under the identity in force:
// entities names authors by their entity UUIDs, otherwise by legacy numbers.
//
// The nil UUID, in any spelling — 32 zeros included — is an author that is
// not found under both: it is the code's "no entity", never "no author".
// Otherwise the legacy identity reads a decimal number exactly as it always
// did: a zero-padded ID is that ID, and any zero (0, 000, -0) is no author;
// a UUID there is not found. The entity identity reads a UUID spelling
// first — 32 hex digits may be all decimal — and its only "no author" is
// the literal 0 the feeds use; any other number is not found. An empty ID is
// no author under both.
func ParseAuthorRef(raw string, entities bool) (AuthorRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AuthorRef{}, nil
	}
	id, uuidErr := uuid.Parse(raw)
	if uuidErr == nil && id == uuid.Nil {
		return AuthorRef{}, ErrAuthorNotFound
	}
	if !entities {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return AuthorRef{Legacy: n}, nil
		}
		if uuidErr == nil {
			return AuthorRef{}, ErrAuthorNotFound
		}
		return AuthorRef{}, ErrInvalidAuthorID
	}
	switch {
	case raw == noAuthor:
		return AuthorRef{}, nil
	case uuidErr == nil:
		return AuthorRef{Entity: id}, nil
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return AuthorRef{}, ErrAuthorNotFound
	}
	return AuthorRef{}, ErrInvalidAuthorID
}
