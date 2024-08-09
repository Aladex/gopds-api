-- Create table for book collections
CREATE TABLE public.book_collections (
                                         id SERIAL PRIMARY KEY,
                                         user_id INTEGER NOT NULL,
                                         name VARCHAR(255) NOT NULL,
                                         is_public BOOLEAN DEFAULT FALSE,
                                         created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create table for many-to-many relation between books and book collections
CREATE TABLE public.book_collection_books (
                                              id SERIAL PRIMARY KEY,
                                              book_collection_id INTEGER NOT NULL,
                                              book_id INTEGER NOT NULL
);

-- Add foreign key constraints
ALTER TABLE public.book_collections
    ADD CONSTRAINT book_collections_user_id_fk FOREIGN KEY (user_id) REFERENCES public.auth_user(id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE public.book_collection_books
    ADD CONSTRAINT book_collection_books_collection_id_fk FOREIGN KEY (book_collection_id) REFERENCES public.book_collections(id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE public.book_collection_books
    ADD CONSTRAINT book_collection_books_book_id_fk FOREIGN KEY (book_id) REFERENCES public.opds_catalog_book(id) DEFERRABLE INITIALLY DEFERRED;

-- Create indexes for faster lookups
CREATE INDEX book_collections_user_id_index ON public.book_collections (user_id);
CREATE INDEX book_collection_books_collection_id_index ON public.book_collection_books (book_collection_id);
CREATE INDEX book_collection_books_book_id_index ON public.book_collection_books (book_id);

-- Ensure unique book per collection
ALTER TABLE public.book_collection_books
    ADD CONSTRAINT book_collection_books_unique UNIQUE (book_collection_id, book_id);