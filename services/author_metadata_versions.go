package services

import "gopds-api/config"

// AuthorMetadataExtractorVersion identifies the metadata-only extractor contract.
// Live dual write and backfill must use the same value so they produce the same
// snapshot key for the same file.
const AuthorMetadataExtractorVersion = "fb2-metadata-v1"

// AuthorMetadataMaxBytes bounds the decoded metadata window read from one FB2
// entry (description including annotation; body and binaries are never counted).
// It is the configuration default, so the live dual write and the default
// backfill read the same window.
const AuthorMetadataMaxBytes = config.AuthorMetadataMaxBytes
