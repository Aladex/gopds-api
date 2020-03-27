--
-- PostgreSQL database dump
--

-- Dumped from database version 10.5 (Debian 10.5-2.pgdg90+1)
-- Dumped by pg_dump version 12.2 (Ubuntu 12.2-2.pgdg18.04+1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

--
-- Name: auth_user; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.auth_user (
    id integer NOT NULL,
    password character varying(128) NOT NULL,
    last_login timestamp with time zone,
    is_superuser boolean NOT NULL,
    username character varying(150) NOT NULL,
    first_name character varying(30) NOT NULL,
    last_name character varying(150) NOT NULL,
    email character varying(254) NOT NULL,
    is_staff boolean NOT NULL,
    is_active boolean NOT NULL,
    date_joined timestamp with time zone NOT NULL
);


ALTER TABLE public.auth_user OWNER TO postgres;

--
-- Name: auth_user_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.auth_user_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.auth_user_id_seq OWNER TO postgres;

--
-- Name: auth_user_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.auth_user_id_seq OWNED BY public.auth_user.id;


--
-- Name: opds_catalog_author; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.opds_catalog_author (
    id integer NOT NULL,
    full_name character varying(128) NOT NULL,
    search_full_name character varying(128) NOT NULL,
    lang_code integer NOT NULL
);


ALTER TABLE public.opds_catalog_author OWNER TO postgres;

--
-- Name: opds_catalog_author_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.opds_catalog_author_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.opds_catalog_author_id_seq OWNER TO postgres;

--
-- Name: opds_catalog_author_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.opds_catalog_author_id_seq OWNED BY public.opds_catalog_author.id;


--
-- Name: opds_catalog_bauthor; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.opds_catalog_bauthor (
    id integer NOT NULL,
    author_id integer NOT NULL,
    book_id integer NOT NULL
);


ALTER TABLE public.opds_catalog_bauthor OWNER TO postgres;

--
-- Name: opds_catalog_bauthor_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.opds_catalog_bauthor_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.opds_catalog_bauthor_id_seq OWNER TO postgres;

--
-- Name: opds_catalog_bauthor_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.opds_catalog_bauthor_id_seq OWNED BY public.opds_catalog_bauthor.id;


--
-- Name: opds_catalog_bgenre; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.opds_catalog_bgenre (
    id integer NOT NULL,
    book_id integer NOT NULL,
    genre_id integer NOT NULL
);


ALTER TABLE public.opds_catalog_bgenre OWNER TO postgres;

--
-- Name: opds_catalog_bgenre_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.opds_catalog_bgenre_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.opds_catalog_bgenre_id_seq OWNER TO postgres;

--
-- Name: opds_catalog_bgenre_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.opds_catalog_bgenre_id_seq OWNED BY public.opds_catalog_bgenre.id;


--
-- Name: opds_catalog_book; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.opds_catalog_book (
    id integer NOT NULL,
    filename character varying(512) NOT NULL,
    path character varying(512) NOT NULL,
    filesize integer NOT NULL,
    format character varying(8) NOT NULL,
    cat_type integer NOT NULL,
    registerdate timestamp with time zone NOT NULL,
    docdate character varying(32) NOT NULL,
    lang character varying(16) NOT NULL,
    title character varying(512) NOT NULL,
    search_title character varying(512) NOT NULL,
    annotation character varying(10000) NOT NULL,
    lang_code integer NOT NULL,
    avail integer NOT NULL,
    catalog_id integer NOT NULL
)
WITH (fillfactor='50');


ALTER TABLE public.opds_catalog_book OWNER TO postgres;

--
-- Name: opds_catalog_book_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.opds_catalog_book_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.opds_catalog_book_id_seq OWNER TO postgres;

--
-- Name: opds_catalog_book_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.opds_catalog_book_id_seq OWNED BY public.opds_catalog_book.id;


--
-- Name: opds_catalog_bookshelf; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.opds_catalog_bookshelf (
    id integer NOT NULL,
    readtime timestamp with time zone NOT NULL,
    book_id integer NOT NULL,
    user_id integer NOT NULL
);


ALTER TABLE public.opds_catalog_bookshelf OWNER TO postgres;

--
-- Name: opds_catalog_bookshelf_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.opds_catalog_bookshelf_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.opds_catalog_bookshelf_id_seq OWNER TO postgres;

--
-- Name: opds_catalog_bookshelf_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.opds_catalog_bookshelf_id_seq OWNED BY public.opds_catalog_bookshelf.id;


--
-- Name: opds_catalog_bseries; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.opds_catalog_bseries (
    id integer NOT NULL,
    ser_no integer NOT NULL,
    book_id integer NOT NULL,
    ser_id integer NOT NULL
);


ALTER TABLE public.opds_catalog_bseries OWNER TO postgres;

--
-- Name: opds_catalog_bseries_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.opds_catalog_bseries_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.opds_catalog_bseries_id_seq OWNER TO postgres;

--
-- Name: opds_catalog_bseries_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.opds_catalog_bseries_id_seq OWNED BY public.opds_catalog_bseries.id;


--
-- Name: opds_catalog_catalog; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.opds_catalog_catalog (
    id integer NOT NULL,
    cat_name character varying(190) NOT NULL,
    path character varying(512) NOT NULL,
    cat_type integer NOT NULL,
    cat_size bigint,
    parent_id integer
);


ALTER TABLE public.opds_catalog_catalog OWNER TO postgres;

--
-- Name: opds_catalog_catalog_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.opds_catalog_catalog_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.opds_catalog_catalog_id_seq OWNER TO postgres;

--
-- Name: opds_catalog_catalog_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.opds_catalog_catalog_id_seq OWNED BY public.opds_catalog_catalog.id;


--
-- Name: opds_catalog_counter; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.opds_catalog_counter (
    name character varying(16) NOT NULL,
    value integer NOT NULL,
    update_time timestamp with time zone NOT NULL
);


ALTER TABLE public.opds_catalog_counter OWNER TO postgres;

--
-- Name: opds_catalog_genre; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.opds_catalog_genre (
    id integer NOT NULL,
    genre character varying(32) NOT NULL,
    section character varying(64) NOT NULL,
    subsection character varying(100) NOT NULL
);


ALTER TABLE public.opds_catalog_genre OWNER TO postgres;

--
-- Name: opds_catalog_genre_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.opds_catalog_genre_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.opds_catalog_genre_id_seq OWNER TO postgres;

--
-- Name: opds_catalog_genre_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.opds_catalog_genre_id_seq OWNED BY public.opds_catalog_genre.id;


--
-- Name: opds_catalog_series; Type: TABLE; Schema: public; Owner: postgres
--

CREATE TABLE public.opds_catalog_series (
    id integer NOT NULL,
    ser character varying(150) NOT NULL,
    search_ser character varying(150) NOT NULL,
    lang_code integer NOT NULL
);


ALTER TABLE public.opds_catalog_series OWNER TO postgres;

--
-- Name: opds_catalog_series_id_seq; Type: SEQUENCE; Schema: public; Owner: postgres
--

CREATE SEQUENCE public.opds_catalog_series_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER TABLE public.opds_catalog_series_id_seq OWNER TO postgres;

--
-- Name: opds_catalog_series_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: postgres
--

ALTER SEQUENCE public.opds_catalog_series_id_seq OWNED BY public.opds_catalog_series.id;


--
-- Name: auth_user id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.auth_user ALTER COLUMN id SET DEFAULT nextval('public.auth_user_id_seq'::regclass);


--
-- Name: opds_catalog_author id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_author ALTER COLUMN id SET DEFAULT nextval('public.opds_catalog_author_id_seq'::regclass);


--
-- Name: opds_catalog_bauthor id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bauthor ALTER COLUMN id SET DEFAULT nextval('public.opds_catalog_bauthor_id_seq'::regclass);


--
-- Name: opds_catalog_bgenre id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bgenre ALTER COLUMN id SET DEFAULT nextval('public.opds_catalog_bgenre_id_seq'::regclass);


--
-- Name: opds_catalog_book id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_book ALTER COLUMN id SET DEFAULT nextval('public.opds_catalog_book_id_seq'::regclass);


--
-- Name: opds_catalog_bookshelf id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bookshelf ALTER COLUMN id SET DEFAULT nextval('public.opds_catalog_bookshelf_id_seq'::regclass);


--
-- Name: opds_catalog_bseries id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bseries ALTER COLUMN id SET DEFAULT nextval('public.opds_catalog_bseries_id_seq'::regclass);


--
-- Name: opds_catalog_catalog id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_catalog ALTER COLUMN id SET DEFAULT nextval('public.opds_catalog_catalog_id_seq'::regclass);


--
-- Name: opds_catalog_genre id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_genre ALTER COLUMN id SET DEFAULT nextval('public.opds_catalog_genre_id_seq'::regclass);


--
-- Name: opds_catalog_series id; Type: DEFAULT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_series ALTER COLUMN id SET DEFAULT nextval('public.opds_catalog_series_id_seq'::regclass);


--
-- Name: auth_user auth_user_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.auth_user
    ADD CONSTRAINT auth_user_pkey PRIMARY KEY (id);


--
-- Name: auth_user auth_user_username_key; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.auth_user
    ADD CONSTRAINT auth_user_username_key UNIQUE (username);


--
-- Name: opds_catalog_author opds_catalog_author_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_author
    ADD CONSTRAINT opds_catalog_author_pkey PRIMARY KEY (id);


--
-- Name: opds_catalog_bauthor opds_catalog_bauthor_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bauthor
    ADD CONSTRAINT opds_catalog_bauthor_pkey PRIMARY KEY (id);


--
-- Name: opds_catalog_bgenre opds_catalog_bgenre_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bgenre
    ADD CONSTRAINT opds_catalog_bgenre_pkey PRIMARY KEY (id);


--
-- Name: opds_catalog_book opds_catalog_book_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_book
    ADD CONSTRAINT opds_catalog_book_pkey PRIMARY KEY (id);


--
-- Name: opds_catalog_bookshelf opds_catalog_bookshelf_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bookshelf
    ADD CONSTRAINT opds_catalog_bookshelf_pkey PRIMARY KEY (id);


--
-- Name: opds_catalog_bseries opds_catalog_bseries_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bseries
    ADD CONSTRAINT opds_catalog_bseries_pkey PRIMARY KEY (id);


--
-- Name: opds_catalog_catalog opds_catalog_catalog_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_catalog
    ADD CONSTRAINT opds_catalog_catalog_pkey PRIMARY KEY (id);


--
-- Name: opds_catalog_counter opds_catalog_counter_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_counter
    ADD CONSTRAINT opds_catalog_counter_pkey PRIMARY KEY (name);


--
-- Name: opds_catalog_genre opds_catalog_genre_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_genre
    ADD CONSTRAINT opds_catalog_genre_pkey PRIMARY KEY (id);


--
-- Name: opds_catalog_series opds_catalog_series_pkey; Type: CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_series
    ADD CONSTRAINT opds_catalog_series_pkey PRIMARY KEY (id);


--
-- Name: auth_user_username_6821ab7c_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX auth_user_username_6821ab7c_like ON public.auth_user USING btree (username varchar_pattern_ops);


--
-- Name: opds_catalog_author_full_name_444f34db; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_author_full_name_444f34db ON public.opds_catalog_author USING btree (full_name);


--
-- Name: opds_catalog_author_full_name_444f34db_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_author_full_name_444f34db_like ON public.opds_catalog_author USING btree (full_name varchar_pattern_ops);


--
-- Name: opds_catalog_author_lang_code_0ff5d448; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_author_lang_code_0ff5d448 ON public.opds_catalog_author USING btree (lang_code);


--
-- Name: opds_catalog_author_search_full_name_eb4efc23; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_author_search_full_name_eb4efc23 ON public.opds_catalog_author USING btree (search_full_name);


--
-- Name: opds_catalog_author_search_full_name_eb4efc23_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_author_search_full_name_eb4efc23_like ON public.opds_catalog_author USING btree (search_full_name varchar_pattern_ops);


--
-- Name: opds_catalog_bauthor_author_id_f7332d70; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_bauthor_author_id_f7332d70 ON public.opds_catalog_bauthor USING btree (author_id);


--
-- Name: opds_catalog_bauthor_book_id_b5787ec8; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_bauthor_book_id_b5787ec8 ON public.opds_catalog_bauthor USING btree (book_id);


--
-- Name: opds_catalog_bgenre_book_id_dc74ba5a; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_bgenre_book_id_dc74ba5a ON public.opds_catalog_bgenre USING btree (book_id);


--
-- Name: opds_catalog_bgenre_genre_id_8b5bd007; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_bgenre_genre_id_8b5bd007 ON public.opds_catalog_bgenre USING btree (genre_id);


--
-- Name: opds_catalog_book_avail_c46a59b5; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_avail_c46a59b5 ON public.opds_catalog_book USING btree (avail);


--
-- Name: opds_catalog_book_catalog_id_f35decc4; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_catalog_id_f35decc4 ON public.opds_catalog_book USING btree (catalog_id);


--
-- Name: opds_catalog_book_docdate_1faf4fdf; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_docdate_1faf4fdf ON public.opds_catalog_book USING btree (docdate);


--
-- Name: opds_catalog_book_docdate_1faf4fdf_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_docdate_1faf4fdf_like ON public.opds_catalog_book USING btree (docdate varchar_pattern_ops);


--
-- Name: opds_catalog_book_filename_f1827569; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_filename_f1827569 ON public.opds_catalog_book USING btree (filename);


--
-- Name: opds_catalog_book_filename_f1827569_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_filename_f1827569_like ON public.opds_catalog_book USING btree (filename varchar_pattern_ops);


--
-- Name: opds_catalog_book_lang_code_4688aac1; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_lang_code_4688aac1 ON public.opds_catalog_book USING btree (lang_code);


--
-- Name: opds_catalog_book_path_ba0516b8; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_path_ba0516b8 ON public.opds_catalog_book USING btree (path);


--
-- Name: opds_catalog_book_path_ba0516b8_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_path_ba0516b8_like ON public.opds_catalog_book USING btree (path varchar_pattern_ops);


--
-- Name: opds_catalog_book_search_title_23b7dd56; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_search_title_23b7dd56 ON public.opds_catalog_book USING btree (search_title);


--
-- Name: opds_catalog_book_search_title_23b7dd56_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_search_title_23b7dd56_like ON public.opds_catalog_book USING btree (search_title varchar_pattern_ops);


--
-- Name: opds_catalog_book_title_ef02eb01; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_title_ef02eb01 ON public.opds_catalog_book USING btree (title);


--
-- Name: opds_catalog_book_title_ef02eb01_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_book_title_ef02eb01_like ON public.opds_catalog_book USING btree (title varchar_pattern_ops);


--
-- Name: opds_catalog_bookshelf_book_id_6868526f; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_bookshelf_book_id_6868526f ON public.opds_catalog_bookshelf USING btree (book_id);


--
-- Name: opds_catalog_bookshelf_readtime_64d1e1c5; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_bookshelf_readtime_64d1e1c5 ON public.opds_catalog_bookshelf USING btree (readtime);


--
-- Name: opds_catalog_bookshelf_user_id_9adf9486; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_bookshelf_user_id_9adf9486 ON public.opds_catalog_bookshelf USING btree (user_id);


--
-- Name: opds_catalog_bseries_book_id_04da58e5; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_bseries_book_id_04da58e5 ON public.opds_catalog_bseries USING btree (book_id);


--
-- Name: opds_catalog_bseries_ser_id_8a80f5c7; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_bseries_ser_id_8a80f5c7 ON public.opds_catalog_bseries USING btree (ser_id);


--
-- Name: opds_catalog_catalog_cat_name_75c73cd5; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_catalog_cat_name_75c73cd5 ON public.opds_catalog_catalog USING btree (cat_name);


--
-- Name: opds_catalog_catalog_cat_name_75c73cd5_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_catalog_cat_name_75c73cd5_like ON public.opds_catalog_catalog USING btree (cat_name varchar_pattern_ops);


--
-- Name: opds_catalog_catalog_parent_id_ac149f35; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_catalog_parent_id_ac149f35 ON public.opds_catalog_catalog USING btree (parent_id);


--
-- Name: opds_catalog_catalog_path_5d0ee60b; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_catalog_path_5d0ee60b ON public.opds_catalog_catalog USING btree (path);


--
-- Name: opds_catalog_catalog_path_5d0ee60b_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_catalog_path_5d0ee60b_like ON public.opds_catalog_catalog USING btree (path varchar_pattern_ops);


--
-- Name: opds_catalog_counter_name_cfca2854_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_counter_name_cfca2854_like ON public.opds_catalog_counter USING btree (name varchar_pattern_ops);


--
-- Name: opds_catalog_genre_genre_aef7d330; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_genre_genre_aef7d330 ON public.opds_catalog_genre USING btree (genre);


--
-- Name: opds_catalog_genre_genre_aef7d330_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_genre_genre_aef7d330_like ON public.opds_catalog_genre USING btree (genre varchar_pattern_ops);


--
-- Name: opds_catalog_genre_section_43ee441e; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_genre_section_43ee441e ON public.opds_catalog_genre USING btree (section);


--
-- Name: opds_catalog_genre_section_43ee441e_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_genre_section_43ee441e_like ON public.opds_catalog_genre USING btree (section varchar_pattern_ops);


--
-- Name: opds_catalog_genre_subsection_4b8b119e; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_genre_subsection_4b8b119e ON public.opds_catalog_genre USING btree (subsection);


--
-- Name: opds_catalog_genre_subsection_4b8b119e_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_genre_subsection_4b8b119e_like ON public.opds_catalog_genre USING btree (subsection varchar_pattern_ops);


--
-- Name: opds_catalog_series_lang_code_d62fcb5f; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_series_lang_code_d62fcb5f ON public.opds_catalog_series USING btree (lang_code);


--
-- Name: opds_catalog_series_search_ser_0b7b3ee9; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_series_search_ser_0b7b3ee9 ON public.opds_catalog_series USING btree (search_ser);


--
-- Name: opds_catalog_series_search_ser_0b7b3ee9_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_series_search_ser_0b7b3ee9_like ON public.opds_catalog_series USING btree (search_ser varchar_pattern_ops);


--
-- Name: opds_catalog_series_ser_7d15929e; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_series_ser_7d15929e ON public.opds_catalog_series USING btree (ser);


--
-- Name: opds_catalog_series_ser_7d15929e_like; Type: INDEX; Schema: public; Owner: postgres
--

CREATE INDEX opds_catalog_series_ser_7d15929e_like ON public.opds_catalog_series USING btree (ser varchar_pattern_ops);


--
-- Name: opds_catalog_bauthor opds_catalog_bauthor_author_id_f7332d70_fk_opds_cata; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bauthor
    ADD CONSTRAINT opds_catalog_bauthor_author_id_f7332d70_fk_opds_cata FOREIGN KEY (author_id) REFERENCES public.opds_catalog_author(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: opds_catalog_bauthor opds_catalog_bauthor_book_id_b5787ec8_fk_opds_catalog_book_id; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bauthor
    ADD CONSTRAINT opds_catalog_bauthor_book_id_b5787ec8_fk_opds_catalog_book_id FOREIGN KEY (book_id) REFERENCES public.opds_catalog_book(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: opds_catalog_bgenre opds_catalog_bgenre_book_id_dc74ba5a_fk_opds_catalog_book_id; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bgenre
    ADD CONSTRAINT opds_catalog_bgenre_book_id_dc74ba5a_fk_opds_catalog_book_id FOREIGN KEY (book_id) REFERENCES public.opds_catalog_book(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: opds_catalog_bgenre opds_catalog_bgenre_genre_id_8b5bd007_fk_opds_catalog_genre_id; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bgenre
    ADD CONSTRAINT opds_catalog_bgenre_genre_id_8b5bd007_fk_opds_catalog_genre_id FOREIGN KEY (genre_id) REFERENCES public.opds_catalog_genre(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: opds_catalog_book opds_catalog_book_catalog_id_f35decc4_fk_opds_cata; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_book
    ADD CONSTRAINT opds_catalog_book_catalog_id_f35decc4_fk_opds_cata FOREIGN KEY (catalog_id) REFERENCES public.opds_catalog_catalog(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: opds_catalog_bookshelf opds_catalog_bookshelf_book_id_6868526f_fk_opds_catalog_book_id; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bookshelf
    ADD CONSTRAINT opds_catalog_bookshelf_book_id_6868526f_fk_opds_catalog_book_id FOREIGN KEY (book_id) REFERENCES public.opds_catalog_book(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: opds_catalog_bookshelf opds_catalog_bookshelf_user_id_9adf9486_fk_auth_user_id; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bookshelf
    ADD CONSTRAINT opds_catalog_bookshelf_user_id_9adf9486_fk_auth_user_id FOREIGN KEY (user_id) REFERENCES public.auth_user(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: opds_catalog_bseries opds_catalog_bseries_book_id_04da58e5_fk_opds_catalog_book_id; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bseries
    ADD CONSTRAINT opds_catalog_bseries_book_id_04da58e5_fk_opds_catalog_book_id FOREIGN KEY (book_id) REFERENCES public.opds_catalog_book(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: opds_catalog_bseries opds_catalog_bseries_ser_id_8a80f5c7_fk_opds_catalog_series_id; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_bseries
    ADD CONSTRAINT opds_catalog_bseries_ser_id_8a80f5c7_fk_opds_catalog_series_id FOREIGN KEY (ser_id) REFERENCES public.opds_catalog_series(id) DEFERRABLE INITIALLY DEFERRED;


--
-- Name: opds_catalog_catalog opds_catalog_catalog_parent_id_ac149f35_fk_opds_cata; Type: FK CONSTRAINT; Schema: public; Owner: postgres
--

ALTER TABLE ONLY public.opds_catalog_catalog
    ADD CONSTRAINT opds_catalog_catalog_parent_id_ac149f35_fk_opds_cata FOREIGN KEY (parent_id) REFERENCES public.opds_catalog_catalog(id) DEFERRABLE INITIALLY DEFERRED;


--
-- PostgreSQL database dump complete
--

