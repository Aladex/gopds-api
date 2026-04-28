package database

import (
	"context"
	"encoding/json"

	"gopds-api/models"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

// trigramTitleMin is the minimum similarity over `lower(title)` we even consider
// a candidate. Anything below is below noise and is filtered out before bucketing.
const trigramTitleMin = 0.30

// CreateCuratedCollection inserts a new curated collection skeleton with
// status=importing, is_curated=true, is_public=false. The user_id stays NULL —
// curated collections are owned by the system, not by an admin user.
func CreateCuratedCollection(ctx context.Context, name, sourceURL string) (int64, error) {
	c := &models.BookCollection{
		Name:         name,
		SourceURL:    sourceURL,
		IsCurated:    true,
		IsPublic:     false,
		ImportStatus: models.ImportStatusImporting,
	}
	_, err := db.ModelContext(ctx, c).Insert()
	if err != nil {
		return 0, err
	}
	return c.ID, nil
}

// AddCollectionItem persists one item into book_collection_items.
func AddCollectionItem(ctx context.Context, collectionID int64, item models.PersistedCollectionItem) error {
	record := &models.BookCollectionItem{
		CollectionID:   collectionID,
		BookID:         item.BookID,
		ExternalTitle:  item.ExternalTitle,
		ExternalAuthor: item.ExternalAuthor,
		ExternalExtra:  item.ExternalExtra,
		MatchStatus:    item.MatchStatus,
		MatchScore:     item.MatchScore,
		Position:       item.Position,
	}
	_, err := db.ModelContext(ctx, record).Insert()
	return err
}

// UpdateCollectionImportStats writes only the import_stats jsonb column without
// touching status / error. Used by the LLM resolver to push live progress
// updates into the same json blob that the admin UI already polls via /status.
func UpdateCollectionImportStats(ctx context.Context, collectionID int64, stats models.CollectionImportStats) error {
	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	_, err = db.ModelContext(ctx, (*models.BookCollection)(nil)).
		Set("import_stats = ?", string(statsJSON)).
		Where("id = ?", collectionID).
		Update()
	return err
}

// UpdateCollectionImportStatus finalizes the collection after the import loop completes.
// stats is persisted into the import_stats JSONB column; go-pg handles the
// struct↔jsonb conversion via the column tag on the model.
func UpdateCollectionImportStatus(ctx context.Context, collectionID int64, status, importError string, stats models.CollectionImportStats) error {
	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	_, err = db.ModelContext(ctx, (*models.BookCollection)(nil)).
		Set("import_status = ?", status).
		Set("import_error = ?", importError).
		Set("import_stats = ?", string(statsJSON)).
		Set("imported_at = NOW()").
		Where("id = ?", collectionID).
		Update()
	return err
}

// LookupMatchDecision returns the previously decided book_id for the (author, title) pair, or nil if absent.
func LookupMatchDecision(ctx context.Context, authorNorm, titleNorm string) (*int64, error) {
	var bookID int64
	_, err := db.QueryOneContext(ctx, pg.Scan(&bookID),
		`SELECT book_id FROM book_match_decisions WHERE author_norm = ? AND title_norm = ?`,
		authorNorm, titleNorm)
	if err == pg.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &bookID, nil
}

// SaveMatchDecision upserts a manual match decision so future imports of the same pair are auto-resolved.
func SaveMatchDecision(ctx context.Context, authorNorm, titleNorm string, bookID int64, decidedByUserID *int64) error {
	d := &models.BookMatchDecision{
		AuthorNorm:      authorNorm,
		TitleNorm:       titleNorm,
		BookID:          bookID,
		DecidedByUserID: decidedByUserID,
	}
	_, err := db.ModelContext(ctx, d).
		OnConflict("(author_norm, title_norm) DO UPDATE").
		Set("book_id = EXCLUDED.book_id").
		Set("decided_by_user_id = EXCLUDED.decided_by_user_id").
		Insert()
	return err
}

// GetCuratedCollection fetches one collection by id. Returns pg.ErrNoRows if absent.
func GetCuratedCollection(ctx context.Context, id int64) (*models.BookCollection, error) {
	c := &models.BookCollection{}
	err := db.ModelContext(ctx, c).Where("id = ?", id).First()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ListCuratedCollections returns is_curated=true collections, newest first,
// paginated. total is the number of curated collections regardless of page.
func ListCuratedCollections(ctx context.Context, page, pageSize int) ([]models.BookCollection, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 25
	}
	var out []models.BookCollection
	total, err := db.ModelContext(ctx, &out).
		Where("is_curated = ?", true).
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		SelectAndCount()
	return out, total, err
}

// ListCollectionItems returns items of one collection, optionally filtered by match_status,
// paginated. Returns the page slice and the total count of matching rows for pagination.
func ListCollectionItems(ctx context.Context, collectionID int64, statusFilter string, page, pageSize int) ([]models.BookCollectionItem, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	// "matched" is a virtual filter that combines auto-matched items with the
	// ones the admin manually resolved — both are "found" from the user's POV.
	matchedStatuses := []string{models.MatchStatusAutoMatched, models.MatchStatusManual}

	applyFilter := func(q *orm.Query) *orm.Query {
		switch statusFilter {
		case "":
			return q
		case "matched":
			return q.Where("match_status IN (?)", pg.In(matchedStatuses))
		default:
			return q.Where("match_status = ?", statusFilter)
		}
	}

	countQ := applyFilter(db.ModelContext(ctx, (*models.BookCollectionItem)(nil)).
		Where("collection_id = ?", collectionID))
	total, err := countQ.Count()
	if err != nil {
		return nil, 0, err
	}

	var items []models.BookCollectionItem
	listQ := applyFilter(db.ModelContext(ctx, &items).
		Where("collection_id = ?", collectionID))
	err = listQ.
		Order("position ASC", "id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Select()
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetCollectionItem fetches a single item by id (cross-collection, since item ids are global).
func GetCollectionItem(ctx context.Context, itemID int64) (*models.BookCollectionItem, error) {
	it := &models.BookCollectionItem{}
	err := db.ModelContext(ctx, it).Where("id = ?", itemID).First()
	if err != nil {
		return nil, err
	}
	return it, nil
}

// ResolveCollectionItem links the item to a concrete book and flips its status to manual.
// Caller is responsible for separately recording the decision in book_match_decisions
// via SaveMatchDecision so future imports of the same pair are auto-resolved.
func ResolveCollectionItem(ctx context.Context, itemID, bookID int64) error {
	_, err := db.ModelContext(ctx, (*models.BookCollectionItem)(nil)).
		Set("book_id = ?", bookID).
		Set("match_status = ?", models.MatchStatusManual).
		Set("match_score = ?", 1.0).
		Where("id = ?", itemID).
		Update()
	return err
}

// IgnoreCollectionItem flips the item to status=ignored without touching book_id.
func IgnoreCollectionItem(ctx context.Context, itemID int64) error {
	_, err := db.ModelContext(ctx, (*models.BookCollectionItem)(nil)).
		Set("match_status = ?", models.MatchStatusIgnored).
		Where("id = ?", itemID).
		Update()
	return err
}

// CuratedCollectionPatch is the set of admin-mutable fields on a curated collection.
// Nil fields are not modified.
type CuratedCollectionPatch struct {
	Name      *string
	IsPublic  *bool
	SourceURL *string
}

// UpdateCuratedCollection applies a partial patch. Returns pg.ErrNoRows if id is absent.
func UpdateCuratedCollection(ctx context.Context, id int64, patch CuratedCollectionPatch) error {
	q := db.ModelContext(ctx, (*models.BookCollection)(nil)).Where("id = ?", id)
	any := false
	if patch.Name != nil {
		q = q.Set("name = ?", *patch.Name)
		any = true
	}
	if patch.IsPublic != nil {
		q = q.Set("is_public = ?", *patch.IsPublic)
		any = true
	}
	if patch.SourceURL != nil {
		q = q.Set("source_url = ?", *patch.SourceURL)
		any = true
	}
	if !any {
		return nil
	}
	res, err := q.Update()
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return pg.ErrNoRows
	}
	return nil
}

// DeleteCuratedCollection hard-deletes the collection. items and votes cascade
// via FK ON DELETE CASCADE. Returns pg.ErrNoRows if id is absent.
func DeleteCuratedCollection(ctx context.Context, id int64) error {
	res, err := db.ModelContext(ctx, (*models.BookCollection)(nil)).
		Where("id = ?", id).
		Delete()
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return pg.ErrNoRows
	}
	return nil
}

// ListPublicCuratedCollections returns published curated collections, newest first,
// paginated. Drafts and UGC are filtered out at this layer so the public handler
// can trust the result.
func ListPublicCuratedCollections(ctx context.Context, page, pageSize int) ([]models.BookCollection, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 12
	}
	var out []models.BookCollection
	total, err := db.ModelContext(ctx, &out).
		Where("is_curated = ?", true).
		Where("is_public = ?", true).
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		SelectAndCount()
	return out, total, err
}

// GetPublicCuratedCollection fetches one published curated collection.
// Returns pg.ErrNoRows if absent / draft / UGC — handler maps that to 404.
func GetPublicCuratedCollection(ctx context.Context, id int64) (*models.BookCollection, error) {
	c := &models.BookCollection{}
	err := db.ModelContext(ctx, c).
		Where("id = ?", id).
		Where("is_curated = ?", true).
		Where("is_public = ?", true).
		First()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// CollectionCoverBook is one row of the cover-strip we render on /collections cards.
// Only the fields we actually need on the frontend (path/filename for cover URL,
// cover flag, title for fallback initials) are exposed.
type CollectionCoverBook struct {
	CollectionID int64  `pg:"collection_id" json:"-"`
	ID           int64  `pg:"id" json:"id"`
	Path         string `pg:"path" json:"path"`
	Filename     string `pg:"filename" json:"filename"`
	Cover        bool   `pg:"cover" json:"cover"`
	Title        string `pg:"title" json:"title"`
	Rn           int    `pg:"rn" json:"-"`
}

// GetCollectionCovers returns up to 4 books per requested collection, prioritizing
// rows with `cover=true` so cards show real artwork whenever the library has any.
// When all matched items in a collection lack covers the collection's bucket is
// simply empty — the frontend draws a coverless fallback.
func GetCollectionCovers(ctx context.Context, collectionIDs []int64) (map[int64][]CollectionCoverBook, error) {
	if len(collectionIDs) == 0 {
		return map[int64][]CollectionCoverBook{}, nil
	}
	const q = `
		SELECT collection_id, id, path, filename, cover, title, rn FROM (
			SELECT
				i.collection_id,
				b.id,
				b.path,
				b.filename,
				b.cover,
				b.title,
				ROW_NUMBER() OVER (
					PARTITION BY i.collection_id
					ORDER BY b.cover DESC, i.position ASC, i.id ASC
				) AS rn
			FROM book_collection_items i
			JOIN opds_catalog_book b ON b.id = i.book_id
			WHERE i.book_id IS NOT NULL
			  AND i.match_status IN ('auto_matched', 'manual')
			  AND i.collection_id IN (?)
		) ranked
		WHERE rn <= 4
		ORDER BY collection_id, rn
	`
	var rows []CollectionCoverBook
	_, err := db.QueryContext(ctx, &rows, q, pg.In(collectionIDs))
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]CollectionCoverBook, len(collectionIDs))
	for _, r := range rows {
		out[r.CollectionID] = append(out[r.CollectionID], r)
	}
	return out, nil
}

// GetPublicCollectionBooks returns matched-or-manual books of a curated collection
// preserving the curator-defined order from book_collection_items.position.
// Items with NULL book_id, ambiguous, not_found or ignored are excluded.
func GetPublicCollectionBooks(ctx context.Context, collectionID int64) ([]models.Book, error) {
	var books []models.Book
	err := db.ModelContext(ctx, &books).
		ColumnExpr("book.*").
		Join("JOIN book_collection_items i ON i.book_id = book.id").
		Where("i.collection_id = ?", collectionID).
		Where("i.match_status IN (?)", pg.In([]string{
			models.MatchStatusAutoMatched,
			models.MatchStatusManual,
		})).
		OrderExpr("i.position ASC, i.id ASC").
		Select()
	return books, err
}

// FindCollectionCandidates runs trigram similarity over the local catalog for one
// normalized (author, title) pair. Returns up to 10 candidates ordered by combined
// score = title_sim * 0.6 + author_sim * 0.4.
//
// Title similarity is the primary filter (must exceed trigramTitleMin); author
// similarity is an additive boost. Books without authors fall back to author_sim = 0
// via COALESCE.
func FindCollectionCandidates(ctx context.Context, authorNorm, titleNorm string) ([]models.MatchCandidate, error) {
	if titleNorm == "" {
		return nil, nil
	}

	query := `
		SELECT b.id AS book_id,
		       (similarity(lower(b.title), ?) * 0.6 +
		        COALESCE(MAX(similarity(lower(a.full_name), ?)), 0) * 0.4)::real AS score
		FROM opds_catalog_book b
		LEFT JOIN opds_catalog_bauthor ba ON ba.book_id = b.id
		LEFT JOIN opds_catalog_author a ON a.id = ba.author_id
		WHERE similarity(lower(b.title), ?) > ?
		GROUP BY b.id, b.title
		ORDER BY score DESC
		LIMIT 10
	`
	type row struct {
		BookID int64   `pg:"book_id"`
		Score  float32 `pg:"score"`
	}
	var rows []row
	_, err := db.QueryContext(ctx, &rows, query, titleNorm, authorNorm, titleNorm, trigramTitleMin)
	if err != nil {
		return nil, err
	}

	out := make([]models.MatchCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, models.MatchCandidate{BookID: r.BookID, Score: r.Score})
	}
	return out, nil
}

