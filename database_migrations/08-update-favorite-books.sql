-- 08-update-favorite-books.sql

ALTER TABLE public.favorite_books ADD COLUMN folder_id integer DEFAULT NULL;
ALTER TABLE public.favorite_books ADD COLUMN public boolean DEFAULT false NOT NULL;

CREATE INDEX favorite_books_folder_id_index ON public.favorite_books USING btree (folder_id);

ALTER TABLE ONLY public.favorite_books
    ADD CONSTRAINT favorite_books_folder_id_fk FOREIGN KEY (folder_id) REFERENCES public.favorite_folders(id) DEFERRABLE INITIALLY DEFERRED;
