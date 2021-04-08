--
-- Name: favorite_books; Type: TABLE; Schema: public; Owner: gopds
--

CREATE TABLE public.covers (
                                       id integer NOT NULL,
                                       book_id integer NOT NULL,
                                       cover   TEXT    NOT NULL,
                                       content_type character varying(30) DEFAULT ''
);

ALTER TABLE public.covers OWNER TO gopds;

CREATE SEQUENCE public.covers_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER TABLE public.covers_id_seq OWNER TO gopds;
ALTER SEQUENCE public.covers_id_seq OWNED BY public.covers.id;

ALTER TABLE ONLY public.covers ALTER COLUMN id SET DEFAULT nextval('public.covers_id_seq'::regclass);

ALTER TABLE ONLY public.covers
    ADD CONSTRAINT covers_pkey PRIMARY KEY (id);

CREATE INDEX covers_book_id_index ON public.covers USING btree (book_id);
ALTER TABLE ONLY public.covers
    ADD CONSTRAINT covers_id_index_fk_opds_cata FOREIGN KEY (book_id) REFERENCES public.opds_catalog_book(id) DEFERRABLE INITIALLY DEFERRED;

