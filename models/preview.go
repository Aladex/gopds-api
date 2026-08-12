package models

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
