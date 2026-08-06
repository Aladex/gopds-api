package database

import (
	"strings"
	"testing"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSearchNormalize pins the exact contract of public.search_normalize:
// NFC folding, lowercasing, е/ё equivalence, punctuation to spaces and
// collapsed whitespace. Every indexed expression and every query needle must
// pass through this one function.
func TestSearchNormalize(t *testing.T) {
	requireDatabase(t)

	tests := []struct{ input, want string }{
		{"  Ёжик   в тумане  ", "ежик в тумане"},
		{"Война—и–мир", "война и мир"},
		{"«Мастер» 'и' Маргарита", "мастер и маргарита"},
		{"Café", "café"},
		{"Книга № 2", "книга 2"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var got string
			_, err := db.QueryOne(pg.Scan(&got), `SELECT public.search_normalize(?)`, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("null stays null", func(t *testing.T) {
		var got *string
		_, err := db.QueryOne(pg.Scan(&got), `SELECT public.search_normalize(NULL)`)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

// TestSearchIndexesMatchNormalizedExpression requires the search indexes to be
// built over the exact canonical expression. An index over title or
// lower(title) cannot serve a predicate on public.search_normalize(title), so
// shape alone is not enough — the expression text must match.
func TestSearchIndexesMatchNormalizedExpression(t *testing.T) {
	requireDatabase(t)

	type indexShape struct {
		Name     string
		Table    string
		IndexDef string
	}
	var defs []indexShape
	_, err := db.Query(&defs, `
		SELECT indexname AS name, tablename AS table, indexdef AS index_def
		FROM pg_indexes
		WHERE schemaname = 'public'
			AND indexdef ILIKE '%search_normalize%'`)
	require.NoError(t, err)

	assertExpressionIndex := func(table, column string, features ...string) {
		t.Helper()
		// pg_indexes stores the expression in its rewritten form:
		// search_normalize((title)::text), without the schema qualifier.
		want := "search_normalize((" + column + ")::text)"
		for _, d := range defs {
			if d.Table != table {
				continue
			}
			def := strings.ReplaceAll(strings.ToLower(d.IndexDef), " ", "")
			if !strings.Contains(def, want) {
				continue
			}
			for _, feature := range features {
				if !strings.Contains(def, feature) {
					continue
				}
				return
			}
		}
		t.Errorf("no index on %s over public.search_normalize(%s) with any of %v; got %v",
			table, column, features, defs)
	}

	// The trigram GIN and the prefix B-tree on the normalized title.
	assertExpressionIndex("opds_catalog_book", "title", "gin")
	assertExpressionIndex("opds_catalog_book", "title", "pattern_ops")
	// The trigram GIN on the normalized author name.
	assertExpressionIndex("opds_catalog_author", "full_name", "gin")
}

// TestSearchIndexOperatorClasses checks the operator classes themselves, not
// the formatted indexdef text: an index built with a different GIN opclass
// would satisfy a substring check yet never serve `%`, `%>` or trigram LIKE.
// Read straight from pg_index/pg_opclass, so there is no rendering to fool.
func TestSearchIndexOperatorClasses(t *testing.T) {
	requireDatabase(t)

	type operatorClass struct {
		Index   string
		Access  string
		Opclass string
	}
	var got []operatorClass
	_, err := db.Query(&got, `
		SELECT cls.relname AS index, am.amname AS access, opc.opcname AS opclass
		FROM pg_index x
		JOIN pg_class cls ON cls.oid = x.indexrelid
		JOIN pg_namespace ns ON ns.oid = cls.relnamespace
		JOIN pg_am am ON am.oid = cls.relam
		JOIN pg_opclass opc ON opc.oid = x.indclass[0]
		WHERE ns.nspname = 'public'
			AND cls.relname IN (
				'idx_book_title_search_norm_trgm',
				'idx_book_title_search_norm_pattern',
				'idx_author_full_name_search_norm_trgm')`)
	require.NoError(t, err)

	byIndex := make(map[string]operatorClass, len(got))
	for _, oc := range got {
		byIndex[oc.Index] = oc
	}

	for index, want := range map[string]struct{ access, opclass string }{
		"idx_book_title_search_norm_trgm":       {"gin", "gin_trgm_ops"},
		"idx_book_title_search_norm_pattern":    {"btree", "text_pattern_ops"},
		"idx_author_full_name_search_norm_trgm": {"gin", "gin_trgm_ops"},
	} {
		actual, found := byIndex[index]
		require.True(t, found, "index %s missing from pg_index", index)
		assert.Equal(t, want.access, actual.Access, "access method of %s", index)
		assert.Equal(t, want.opclass, actual.Opclass, "operator class of %s", index)
	}
}
