package models

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/go-pg/pg/v10/orm"
)

func init() {
	// Register many-to-many models for ORM recognition.
	orm.RegisterTable((*OrderToAuthor)(nil))
	orm.RegisterTable((*OrderToSeries)(nil))
	orm.RegisterTable((*OrderToGenre)(nil))
	orm.RegisterTable((*UserToBook)(nil))
	orm.RegisterTable((*BookCollectionBook)(nil)) // Register BookCollectionBook for m2m relation.
}

func TranslitDict() map[string]string {
	return map[string]string{
		"А": "A",
		"а": "a",
		"Б": "B",
		"б": "b",
		"В": "V",
		"в": "v",
		"Г": "G",
		"г": "g",
		"Д": "D",
		"д": "d",
		"Е": "E",
		"е": "e",
		"Ё": "Jo",
		"ё": "jo",
		"Ж": "Zh",
		"ж": "zh",
		"З": "Z",
		"з": "z",
		"И": "I",
		"и": "i",
		"Й": "J",
		"й": "j",
		"К": "K",
		"к": "k",
		"Л": "L",
		"л": "l",
		"М": "M",
		"м": "m",
		"Н": "N",
		"н": "n",
		"О": "O",
		"о": "o",
		"П": "P",
		"п": "p",
		"Р": "R",
		"р": "r",
		"С": "S",
		"с": "s",
		"Т": "T",
		"т": "t",
		"У": "U",
		"у": "u",
		"Ф": "F",
		"ф": "f",
		"Х": "H",
		"х": "h",
		"Ц": "C",
		"ц": "c",
		"Ч": "Ch",
		"ч": "ch",
		"Ш": "Sh",
		"ш": "sh",
		"Щ": "Shh",
		"щ": "shh",
		"Ъ": "",
		"ъ": "",
		"Ы": "Y",
		"ы": "y",
		"Ь": "",
		"ь": "",
		"Э": "Je",
		"э": "je",
		"Ю": "Ju",
		"ю": "ju",
		"Я": "Ja",
		"я": "ja",
	}
}

// Translit transliterate from russian to latin
func Translit(s string) string {
	var buffer bytes.Buffer
	dictionary := TranslitDict()

	for _, v := range s {
		if char, ok := dictionary[string(v)]; ok {
			buffer.WriteString(char)
		} else {
			buffer.WriteString(string(v))
		}
	}

	return buffer.String()
}

// Cover struct for storing covers
type Cover struct {
	tableName   struct{} `pg:"covers,discard_unknown_columns" json:"-"`
	ID          int64    `json:"id" form:"id"`
	BookID      int64    `json:"book_id" form:"book_id"`
	Cover       string   `json:"cover" form:"cover"`
	ContentType string   `json:"content_type" form:"content_type"`
}

// Catalog struct for catalog
type Catalog struct {
	tableName   struct{}   `pg:"opds_catalog_catalog,discard_unknown_columns" json:"-"`
	ID          int64      `pg:"id,pk" json:"id" form:"id"`
	CatName     string     `pg:"cat_name" json:"cat_name" form:"cat_name"`
	IsScanned   bool       `pg:"is_scanned" json:"is_scanned" form:"is_scanned"`
	ScannedAt   *time.Time `pg:"scanned_at" json:"scanned_at,omitempty" form:"scanned_at"`
	BooksCount  int        `pg:"books_count" json:"books_count" form:"books_count"`
	ErrorsCount int        `pg:"errors_count" json:"errors_count" form:"errors_count"`
}

// Book struct for books
type Book struct {
	tableName       struct{}  `pg:"opds_catalog_book,discard_unknown_columns" json:"-"`
	ID              int64     `pg:"id" json:"id"`
	Path            string    `pg:"path" json:"path"`
	Format          string    `pg:"format" json:"format"`
	FileName        string    `pg:"filename" json:"filename"`
	RegisterDate    time.Time `pg:"registerdate" json:"registerdate"`
	DocDate         string    `pg:"docdate,use_zero" json:"docdate"`
	Lang            string    `pg:"lang,use_zero" json:"lang"`
	Title           string    `pg:"title" json:"title"`
	Cover           bool      `pg:"cover" json:"cover"`
	Annotation      string    `pg:"annotation,use_zero" json:"annotation"`
	Fav             bool      `pg:"-" json:"fav"`
	Approved        bool      `pg:"approved" json:"approved"`
	MD5             string    `pg:"md5" json:"md5"`
	DuplicateHidden bool      `pg:"duplicate_hidden" json:"duplicate_hidden"`
	DuplicateOfID   *int64    `pg:"duplicate_of_id" json:"duplicate_of_id,omitempty"`
	Authors         []Author  `pg:"many2many:opds_catalog_bauthor,join_fk:author_id" json:"authors"`
	Series          []*Series `pg:"many2many:opds_catalog_bseries,join_fk:ser_id" json:"series"`
	Genres          []Genre   `pg:"many2many:opds_catalog_bgenre,join_fk:genre_id" json:"genres"`
	Users           []*User   `pg:"many2many:favorite_books,join_fk:book_id" json:"favorites"`
	Covers          []*Cover  `pg:"covers,rel:has-many" json:"covers"`
	FavoriteCount   int       `pg:"-" json:"favorite_count"`
	Position        int       `pg:"-" json:"position"`
}

func (b *Book) DownloadName() string {
	var nameRegExp = regexp.MustCompile(`[^A-Za-z0-9а-яА-ЯёЁ]+`)
	var name = nameRegExp.ReplaceAllString(b.Title, "_")
	name = Translit(name)        // Transliterate the name to Latin characters.
	return strings.ToLower(name) // Convert to lowercase and return.
}

// Author struct for authors
type Author struct {
	tableName struct{} `pg:"opds_catalog_author,discard_unknown_columns" json:"-"`
	ID        int64    `json:"id" form:"id"`
	FullName  string   `json:"full_name" form:"full_name"`
}

// OrderToAuthor struct for many-to-many relation between orders and authors
type OrderToAuthor struct {
	tableName struct{} `pg:"opds_catalog_bauthor,discard_unknown_columns" json:"-"`
	AuthorID  int64
	BookID    int64
}

// Genre struct for genres/tags
type Genre struct {
	tableName struct{} `pg:"opds_catalog_genre,discard_unknown_columns" json:"-"`
	ID        int64    `pg:"id" json:"id"`
	Genre     string   `pg:"genre" json:"-"`
	Title     string   `pg:"title" json:"-"`
}

// DisplayName returns Title if set, otherwise falls back to Genre.
func (g Genre) DisplayName() string {
	if g.Title != "" {
		return g.Title
	}
	return g.Genre
}

// MarshalJSON provides custom JSON with "genre" field using DisplayName fallback.
func (g Genre) MarshalJSON() ([]byte, error) {
	type genreJSON struct {
		ID    int64  `json:"id"`
		Genre string `json:"genre"`
	}
	return json.Marshal(genreJSON{
		ID:    g.ID,
		Genre: g.DisplayName(),
	})
}

// OrderToGenre struct for many-to-many relation between books and genres
type OrderToGenre struct {
	tableName struct{} `pg:"opds_catalog_bgenre,discard_unknown_columns" json:"-"`
	GenreID   int64    `pg:"genre_id"`
	BookID    int64    `pg:"book_id"`
}

// UserToBook struct for many-to-many relation between users and books
type UserToBook struct {
	tableName struct{} `pg:"favorite_books,discard_unknown_columns" json:"-"`
	ID        int64    `pg:"id" json:"id"`
	UserID    int64    `pg:"user_id" json:"user_id"`
	BookID    int64    `pg:"book_id" json:"book_id"`
}

// Series struct for series
type Series struct {
	tableName struct{} `pg:"opds_catalog_series,discard_unknown_columns" json:"-"`
	ID        int64    `pg:"id" json:"id"`
	SerNo     int64    `json:"ser_no" pg:"-"`
	Ser       string   `pg:"ser,use_zero" json:"ser"`
	LangCode  int      `pg:"lang_code,use_zero" json:"lang_code,default:0"`
}

// OrderToSeries struct for many-to-many relation between orders and series
type OrderToSeries struct {
	tableName struct{} `pg:"opds_catalog_bseries,discard_unknown_columns" json:"-"`
	SerNo     int64    `pg:"ser_no,use_zero"`
	SeriesID  int64    `pg:"ser_id"`
	BookID    int64    `pg:"book_id"`
}

// BookFilters params for filtering books list
type BookFilters struct {
	Limit          int    `form:"limit" json:"limit"`
	Offset         int    `form:"offset" json:"offset"`
	Title          string `form:"title" json:"title"`
	Author         int    `form:"author" json:"author"`
	Series         int    `form:"series" json:"series"`
	Lang           string `form:"lang" json:"lang"`
	Fav            bool   `form:"fav" json:"fav"`
	UnApproved     bool   `form:"unapproved" json:"unapproved"`
	UsersFavorites bool   `form:"users_favorites" json:"users_favorites"`
	Collection     int64  `form:"collection" json:"collection"`
	IncludeHidden  bool   `form:"include_hidden" json:"include_hidden"`
	Genre          int    `form:"genre" json:"genre"`
}

// CollectionFilters params for filtering collections list
type CollectionFilters struct {
	Limit  int   `form:"limit" json:"limit"`
	Offset int   `form:"offset" json:"offset"`
	BookID int64 `form:"book_id" json:"book_id"`
}

// BookDownload struct for book download
type BookDownload struct {
	BookID  int64  `json:"book_id" form:"book_id" binding:"required"`
	Format  string `json:"format" form:"format" binding:"required"`
	Hash    string `json:"md5" form:"md5"`
	Expires int64  `json:"expires" form:"expires"`
}

// FavBook struct for adding books to favorites
type FavBook struct {
	BookID int64 `json:"book_id"`
	Fav    bool  `json:"fav"`
}

// BookUpdateRequest struct for updating book information
type BookUpdateRequest struct {
	ID              int64    `json:"id" binding:"required"`
	Title           *string  `json:"title,omitempty"`
	Annotation      *string  `json:"annotation,omitempty"`
	Lang            *string  `json:"lang,omitempty"`
	DocDate         *string  `json:"docdate,omitempty"`
	Approved        *bool    `json:"approved,omitempty"`
	DuplicateOfID   *int64   `json:"duplicate_of_id,omitempty"`
	DuplicateHidden *bool    `json:"duplicate_hidden,omitempty"`
	Authors         []Author `json:"authors,omitempty"`
	Series          []Series `json:"series,omitempty"`
}

// AutocompleteSuggestion struct for autocomplete suggestions
type AutocompleteSuggestion struct {
	Value string `json:"value"`
	Type  string `json:"type"` // "book" or "author"
	ID    int64  `json:"id,omitempty"`
}

// Language struct for language information
type Language struct {
	Lang          string `json:"lang"`
	LanguageCount int    `json:"language_count"`
}

// Languages is a slice of Language
type Languages []Language

// AdminScanJob struct for tracking duplicate scan job progress
type AdminScanJob struct {
	tableName       struct{}   `pg:"admin_scan_jobs,discard_unknown_columns" json:"-"`
	ID              int64      `pg:"id,pk" json:"id"`
	Status          string     `pg:"status" json:"status"`
	TotalBooks      int        `pg:"total_books" json:"total_books"`
	ProcessedBooks  int        `pg:"processed_books" json:"processed_books"`
	DuplicatesFound int        `pg:"duplicates_found" json:"duplicates_found"`
	StartedAt       *time.Time `pg:"started_at" json:"started_at,omitempty"`
	FinishedAt      *time.Time `pg:"finished_at" json:"finished_at,omitempty"`
	Error           string     `pg:"error" json:"error,omitempty"`
	ScanParams      string     `pg:"scan_params" json:"scan_params,omitempty"`
	CreatedAt       time.Time  `pg:"created_at" json:"created_at"`
	UpdatedAt       time.Time  `pg:"updated_at" json:"updated_at"`
}
