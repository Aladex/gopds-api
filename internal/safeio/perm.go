package safeio

import "io/fs"

// The modes everything this service writes is created with.
//
// Covers and working directories used to be made world-readable, 0755 and
// 0644. They sit on a host directory shared with the machine, and nothing
// outside this process reads them: the application writes a cover and later
// serves it over HTTP itself. Group and other never needed the bit.
//
// These apply to what gets created from now on. Directories that already exist
// keep the mode they were made with, which is why the old tree stays as it is
// until something recreates it.
const (
	// DirMode is for directories this service creates.
	DirMode fs.FileMode = 0o750
	// FileMode is for files this service writes.
	FileMode fs.FileMode = 0o600
)
