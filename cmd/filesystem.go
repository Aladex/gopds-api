package main

import (
	"embed"
	assets "gopds-api"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// initializeDistFolders получает список папок в директории dist
func initializeDistFolders() error {
	err := fs.WalkDir(assets.Assets, "frontend_src/dist", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != "frontend_src/dist" {
			relativePath := strings.TrimPrefix(path, "frontend_src/dist/")
			distFolders = append(distFolders, "/"+relativePath+"/")
		}
		return nil
	})
	return err
}

var distFolders []string

// httpFS wraps an embed.FS to satisfy the http.FileSystem interface.
type httpFS struct {
	root http.FileSystem
}

// NewHTTPFS creates a new httpFS wrapper for an embed.FS.
func NewHTTPFS(root embed.FS) http.FileSystem {
	return httpFS{root: http.FS(root)}
}

// Open opens a file within the httpFS.
func (hfs httpFS) Open(name string) (http.File, error) {
	if name != "/" {
		name = path.Clean("/" + name)
	}
	return hfs.root.Open(name)
}
