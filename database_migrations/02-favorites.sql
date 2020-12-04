--
-- Name: favorite_books; Type: TABLE; Schema: public; Owner: gopds
--

CREATE TABLE public.favorite_books (
    id integer NOT NULL,
    user_id integer NOT NULL,
    book_id integer NOT NULL
);

ALTER TABLE public.favorite_books OWNER TO gopds;

CREATE SEQUENCE public.favorite_books_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER TABLE public.favorite_books_id_seq OWNER TO gopds;
ALTER SEQUENCE public.favorite_books_id_seq OWNED BY public.favorite_books.id;

ALTER TABLE ONLY public.favorite_books ALTER COLUMN id SET DEFAULT nextval('public.favorite_books_id_seq'::regclass);

ALTER TABLE ONLY public.favorite_books
    ADD CONSTRAINT favorite_books_pkey PRIMARY KEY (id);

CREATE INDEX favorite_books_user_id_index ON public.favorite_books USING btree (user_id);
CREATE INDEX favorite_books_book_id_index ON public.favorite_books USING btree (book_id);
ALTER TABLE ONLY public.favorite_books
    ADD CONSTRAINT favorite_books_user_id_index_fk_opds_cata FOREIGN KEY (user_id) REFERENCES public.auth_user(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE ONLY public.favorite_books
    ADD CONSTRAINT favorite_books_user_id_index_fk_opds_catalog_book_id FOREIGN KEY (book_id) REFERENCES public.opds_catalog_book(id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.favorite_books
    ADD UNIQUE (user_id, book_id);