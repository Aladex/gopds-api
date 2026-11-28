-- 86: an author's short name, «Имя Фамилия», for telling namesakes apart
-- (design/author-display, rule 4).
--
--   * public.author_short_name(name): the name with a patronymic dropped —
--     the middle word of a three-word name, Cyrillic letters only
--     (U+0400–U+04FF), between a given and a family name with no bracket:
--     a short male form (Ильич, Кузьмич, Никитич, Лукич, Фомич, …) or a word
--     ending in -вич, -овна, -евна, -ична, -івна or -ївна after at least two
--     more letters. A bare -ич also ends family names (Лейтич), so it is not
--     a suffix of its own. Any other name comes back as it is. models.ShortAuthorName is
--     the same rule in Go; a database test holds the two together. Words are
--     split on Unicode White_Space, as Go's strings.Fields splits them.
--   * author_entity_short_name_idx: live entities by that short name, so the
--     namesake check (database/author_entity_surface.go) finds the entities
--     sharing it without reading every entity.
--
-- Read side only: no row of author_entity or any other table is written, and
-- the entity build reads neither the function nor the index.
--
-- Locks: author_entity in SHARE mode, NOWAIT, like 28-85: a build writing
-- entities makes the file fail at once and startup retries it; readers go on.

SET LOCAL lock_timeout = '5s';

LOCK TABLE public.author_entity IN SHARE MODE NOWAIT;

CREATE FUNCTION public.author_short_name(name text) RETURNS text
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT CASE WHEN m IS NULL THEN name ELSE m[1] || ' ' || m[2] END
    FROM (SELECT pg_catalog.regexp_match(name,
        '^[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*'
        || '([^\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000()\[\]]+)'
        || '[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+'
        || '(?:[\u0400-\u0481\u048a-\u04ff]{2,}'
        || '(?:[вВ][иИ][чЧ]|[оОеЕ][вВ][нН][аА]|[иИ][чЧ][нН][аА]|[іІїЇ][вВ][нН][аА])'
        || '|(?:[иИ][лЛ][ьЬ]|[иИ][лЛ][лЛ]|[кК][уУ][зЗ][ьЬ][мМ]|[кК][оО][зЗ][ьЬ][мМ]|[кК][уУ][зЗ][мМ]|[нН][иИ][кК][иИ][тТ]|[лЛ][уУ][кК]|[фФ][оО][мМ]|[фФ][оО][кК]|[хХ][оО][мМ])[иИ][чЧ])'
        || '[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+'
        || '([^\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000()\[\]]+)'
        || '[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]*$') AS m) AS found
$$;

CREATE INDEX author_entity_short_name_idx ON public.author_entity (public.author_short_name(display_name))
    WHERE merged_into IS NULL;
