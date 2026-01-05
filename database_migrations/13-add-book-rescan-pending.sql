-- Create book_rescan_pending table for storing pending book rescan results
CREATE TABLE IF NOT EXISTS book_rescan_pending (
    id BIGSERIAL PRIMARY KEY,
    book_id BIGINT NOT NULL REFERENCES opds_catalog_book(id) ON DELETE CASCADE,
    title VARCHAR(512),
    annotation TEXT,
    lang VARCHAR(16),
    docdate VARCHAR(32),
    cover_data BYTEA,
    cover_updated BOOLEAN DEFAULT FALSE,
    authors_json JSONB,
    series_json JSONB,
    tags_json JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    created_by_user_id INTEGER REFERENCES auth_user(id) ON DELETE SET NULL
);

-- Create index for quick lookup by book_id
CREATE INDEX IF NOT EXISTS idx_book_rescan_pending_book_id
ON book_rescan_pending(book_id);

-- Create index for finding recent rescans
CREATE INDEX IF NOT EXISTS idx_book_rescan_pending_created_at
ON book_rescan_pending(created_at DESC);
