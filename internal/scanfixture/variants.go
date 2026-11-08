package scanfixture

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gopds-api/database"

	"github.com/go-pg/pg/v10"
	"github.com/google/uuid"
)

// VariantCard is one book uploaded three times: three visible files with one
// title and language, each its own FB2 document, credited to one legacy author
// and one author entity, and grouped into one card by a full walk of the
// variants read model.
type VariantCard struct {
	Entity uuid.UUID
	Author int64
	Books  []int64
}

// The card's size and how the walk is driven to its end.
const (
	variantCardFiles = 3
	variantWalkBatch = 50
	variantWalkSteps = 100
	// variantWalkTimeout is each book's statement timeout in the walk.
	variantWalkTimeout = 5 * time.Second
)

// NewVariantCard writes a VariantCard into a scratch database and walks the
// read model until the walk finishes, so the card is there as the site reads
// it. It fails the test unless the three files ended up in one card.
func NewVariantCard(t *testing.T, db *pg.DB) VariantCard {
	t.Helper()
	ctx := context.Background()
	scalar := func(query string, args ...any) int64 {
		t.Helper()
		var id int64
		if _, err := db.QueryOne(pg.Scan(&id), query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return id
	}
	card := VariantCard{Entity: uuid.New()}
	if _, err := db.Exec(`INSERT INTO author_entity (id, kind, display_name, sort_name, search_key)
		VALUES (?, 'person', 'Станислав Лем', 'Лем Станислав', public.search_normalize('Станислав Лем'))`,
		card.Entity); err != nil {
		t.Fatalf("making the entity: %v", err)
	}
	card.Author = scalar(`INSERT INTO opds_catalog_author (full_name) VALUES ('Лем Станислав') RETURNING id`)
	for i := range variantCardFiles {
		id := scalar(`INSERT INTO opds_catalog_book
			(filename, path, format, title, annotation, lang, registerdate, docdate, approved, cover, md5, duplicate_hidden)
			VALUES (?, 'variants.zip', 'fb2', 'Солярис', '', 'ru', ?, '', true, false, md5(?), false) RETURNING id`,
			fmt.Sprintf("solaris-%d.fb2", i), time.Now().Add(-time.Duration(variantCardFiles-i)*24*time.Hour), fmt.Sprintf("solaris-%d", i))
		scalar(`INSERT INTO opds_catalog_bauthor (book_id, author_id) VALUES (?, ?) RETURNING id`, id, card.Author)
		scalar(`INSERT INTO book_author_display
			(book_id, position, display, sort_key, search_key, source, credit_id, entity_ids)
			VALUES (?, 0, 'Станислав Лем', 'лем станислав', 'станислав лем', 'layer', ?, ARRAY[?::uuid])
			RETURNING book_id`, id, id, card.Entity)
		scalar(`INSERT INTO book_metadata_snapshot
				(book_id, book_md5, extractor_version, origin, outcome, archive_path, entry_name,
				 source_title, source_document_id, source_document_version, is_current)
			SELECT id, md5, 'extractor-v1', 'live', 'extracted', path, filename, title, ?, '1.0', true
			FROM opds_catalog_book WHERE id = ? RETURNING id`, fmt.Sprintf("solaris-doc-%d", i), id)
		card.Books = append(card.Books, id)
	}

	for step := 0; ; step++ {
		if step == variantWalkSteps {
			t.Fatal("the variants walk did not finish")
		}
		result, err := database.BookVariantWalkStep(ctx, db, variantWalkBatch, time.Hour, variantWalkTimeout, time.Time{})
		if err != nil {
			t.Fatalf("walking the variants: %v", err)
		}
		if result.Finished {
			break
		}
	}
	var cards, files int
	if _, err := db.QueryOne(pg.Scan(&cards, &files), `SELECT count(DISTINCT group_key), count(*)
		FROM book_variant_member WHERE book_id IN (?)`, pg.In(card.Books)); err != nil {
		t.Fatalf("reading the card: %v", err)
	}
	if cards != 1 || files != variantCardFiles {
		t.Fatalf("the files are not one card: %d cards, %d rows", cards, files)
	}
	return card
}
