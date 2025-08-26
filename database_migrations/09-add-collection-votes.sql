-- Remove the rating column from book collections
ALTER TABLE public.book_collections
    DROP COLUMN rating;

-- Create table for collection votes
CREATE TABLE public.collection_votes (
                                         id SERIAL PRIMARY KEY,
                                         user_id INTEGER NOT NULL,
                                         collection_id INTEGER NOT NULL,
                                         vote BOOLEAN NOT NULL,
                                         created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                                         updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
                                         UNIQUE (user_id, collection_id)
);

-- Add foreign key constraints
ALTER TABLE public.collection_votes
    ADD CONSTRAINT collection_votes_user_id_fk FOREIGN KEY (user_id) REFERENCES public.auth_user(id) DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT collection_votes_collection_id_fk FOREIGN KEY (collection_id) REFERENCES public.book_collections(id) DEFERRABLE INITIALLY DEFERRED;

--- Create trigger to update updated_at column on update
CREATE INDEX opds_catalog_author_full_name_trgm_idx ON public.opds_catalog_author USING GIN (lower(full_name) gin_trgm_ops);