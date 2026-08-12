package models

import (
	"fmt"
	"net/url"
)

// PreviewManifestResponse is the JSON document returned by the preview endpoint.
// It contains the revision, the number of chunks, the table of contents, and
// image references. The first chunk is included inline.
type PreviewManifestResponse struct {
	Revision   string            `json:"revision"`
	ChunkCount int               `json:"chunk_count"`
	TOC        []PreviewTOCEntry `json:"toc"`
	Images     []PreviewImageRef `json:"images"`
	FirstChunk string            `json:"first_chunk"`
}

// PreviewTOCEntry is one row of the manifest's table of contents.
type PreviewTOCEntry struct {
	Title  string `json:"title"`
	Depth  int    `json:"depth"`
	Chunk  int    `json:"chunk"`
	Anchor string `json:"anchor"`
}

// PreviewImageRef is the manifest's record of one prepared image.
type PreviewImageRef struct {
	Ordinal int    `json:"ordinal"`
	MIME    string `json:"mime"`
	Bytes   int    `json:"bytes"`
}

// PreviewChunkResponse is the response for a chunk request.
type PreviewChunkResponse struct {
	Chunk string `json:"chunk"`
}

// The address of a prepared picture is spelled in exactly one place. The
// renderer prints it into the HTML and the router registers it; when those
// were two separate spellings, the rendered <img> pointed at an address
// nothing served, and every picture in every preview silently failed to load
// while both sides looked correct on their own.
const (
	// PreviewImageRoutePattern is the gin pattern, relative to the books
	// group, under which prepared pictures are served.
	PreviewImageRoutePattern = "/preview/:id/image/:n"

	// previewImageURLFormat is the same address as a printable string:
	// {books group}/preview/{book}/image/{ordinal}?revision={revision}.
	previewImageURLFormat = "/api/books/preview/%d/image/%d?revision=%s"
)

// PreviewImageURL is the address the rendered HTML points a picture at. It
// must resolve to PreviewImageRoutePattern under the books group — the
// end-to-end test asserts exactly that, by taking a src out of real rendered
// HTML and asking the registered router for it.
func PreviewImageURL(bookID int64, revision string, ordinal int) string {
	return fmt.Sprintf(previewImageURLFormat, bookID, ordinal, url.QueryEscape(revision))
}
