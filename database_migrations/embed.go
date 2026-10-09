// Package migrations carries the SQL migrations inside the binary, so a
// server start can bring its own schema up to date: the image ships only the
// executable, not this directory.
package migrations

import (
	"embed"
	"io/fs"
)

// Dir is where the migrations sit inside FS: its root, as in the directory.
const Dir = "."

//go:embed *.sql
var files embed.FS

// FS returns the embedded migrations.
func FS() fs.FS { return files }
