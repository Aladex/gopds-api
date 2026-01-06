package posters

import (
	"path"
	"path/filepath"
	"strings"
)

func sanitizeSegment(value string) string {
	return strings.ReplaceAll(value, ".", "-")
}

// RelativePath builds a URL/path segment for a poster file.
func RelativePath(bookPath, fileName string) string {
	bookPath = strings.TrimLeft(bookPath, "/\\")
	return path.Join(sanitizeSegment(bookPath), sanitizeSegment(fileName)+".jpg")
}

// FilePath builds an absolute filesystem path for a poster file.
func FilePath(baseDir, bookPath, fileName string) string {
	return filepath.Join(baseDir, RelativePath(bookPath, fileName))
}
