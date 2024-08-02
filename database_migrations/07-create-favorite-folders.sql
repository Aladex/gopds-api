-- 07-create-favorite-folders.sql

CREATE TABLE public.favorite_folders (
                                         id integer NOT NULL,
                                         user_id integer NOT NULL,
                                         name character varying(128) NOT NULL,
                                         public boolean DEFAULT false NOT NULL,
                                         order integer DEFAULT 0 NOT NULL
);

ALTER TABLE public.favorite_folders OWNER TO gopds;

CREATE SEQUENCE public.favorite_folders_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER TABLE public.favorite_folders_id_seq OWNER TO gopds;
ALTER SEQUENCE public.favorite_folders_id_seq OWNED BY public.favorite_folders.id;

ALTER TABLE ONLY public.favorite_folders ALTER COLUMN id SET DEFAULT nextval('public.favorite_folders_id_seq'::regclass);

ALTER TABLE ONLY public.favorite_folders
    ADD CONSTRAINT favorite_folders_pkey PRIMARY KEY (id);

CREATE INDEX favorite_folders_user_id_index ON public.favorite_folders USING btree (user_id);

ALTER TABLE ONLY public.favorite_folders
    ADD CONSTRAINT favorite_folders_user_id_fk FOREIGN KEY (user_id) REFERENCES public.auth_user(id) DEFERRABLE INITIALLY DEFERRED;
