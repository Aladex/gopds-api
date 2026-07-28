// Package safepath turns a path supplied by a request into a path on disk,
// or refuses to.
package safepath

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrEscapesBase reports that the resolved path left the directory it was
// supposed to stay inside.
var ErrEscapesBase = errors.New("path escapes its base directory")

// Resolve joins rel onto base and returns the result only if it stays inside
// base.
//
// The obvious guard — join, then check the result still starts with base — is
// wrong, and quietly: "/srv/posters-backup/x" begins with "/srv/posters" while
// belonging to a different directory. Containment is about path elements, not
// characters, so the comparison has to include the separator.
//
// rel is treated as relative whether or not it arrives with a leading slash.
// A router wildcard yields "/cover.png" and means the same thing as
// "cover.png"; taking the first as absolute would let it address the whole
// filesystem.
func Resolve(base, rel string) (string, error) {
	cleanBase := filepath.Clean(base)

	// Anchored before cleaning, so that ".." in rel is resolved against a root
	// and cannot climb out: Clean("/../x") is "/x", while Clean("../x") keeps
	// the "..".
	anchored := filepath.Clean(string(filepath.Separator) + rel)
	full := filepath.Join(cleanBase, anchored)

	if full != cleanBase && !strings.HasPrefix(full, cleanBase+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q is not inside %q", ErrEscapesBase, full, cleanBase)
	}

	return full, nil
}
