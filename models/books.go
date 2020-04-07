package models

import (
	"github.com/go-pg/pg/v9/orm"
	"time"
)

func init() {
	// Register many to many model so ORM can better recognize m2m relation.
	// This should be done before dependant models are used.
	orm.RegisterTable((*OrderToAuthor)(nil))
	orm.RegisterTable((*OrderToSeries)(nil))
}

// Book структура книги в БД
type Book struct {
	tableName    struct{}  `pg:"opds_catalog_book,discard_unknown_columns" json:"-"`
	ID           int64     `pg:"id" json:"id"`
	Path         string    `pg:"path" json:"path"`
	Format       string    `pg:"format" json:"format"`
	FileName     string    `pg:"filename" json:"filename"`
	RegisterDate time.Time `pg:"registerdate" json:"registerdate"`
	DocDate      string    `pg:"docdate" json:"docdate"`
	Lang         string    `pg:"lang" json:"lang"`
	Title        string    `pg:"title" json:"title"`
	Annotation   string    `pg:"annotation" json:"annotation"`
	Cover        bool      `pg:"cover" json:"cover"`
	Authors      []*Author `pg:"many2many:opds_catalog_bauthor" json:"authors"`
	Series       []*Series `pg:"many2many:opds_catalog_bseries,joinFK:ser_id" json:"series"`
}

// Author структура автора в БД
type Author struct {
	tableName struct{} `pg:"opds_catalog_author,discard_unknown_columns" json:"-"`
	ID        int64    `json:"id"`
	FullName  string   `json:"full_name"`
}

// OrderToAuthor структура для many2many связи книг и авторов
type OrderToAuthor struct {
	tableName struct{} `pg:"opds_catalog_bauthor,discard_unknown_columns" json:"-"`
	AuthorID  int
	BookID    int
}

// Series структура серии книг
type Series struct {
	tableName struct{} `pg:"opds_catalog_series,discard_unknown_columns" json:"-"`
	ID        int64    `json:"id"`
	Ser       string   `json:"ser"`
	LangCode  int      `json:"lang_code"`
}

// OrderToSeries структура связи серий и книг через many2many
type OrderToSeries struct {
	tableName struct{} `pg:"opds_catalog_bseries,discard_unknown_columns" json:"-"`
	SeriesID  int      `pg:"ser_id"`
	BookID    int      `pg:"book_id"`
}

// BookFilters фильтры для query get-запроса при фильтрации по клубам
type BookFilters struct {
	Limit  int    `form:"limit" json:"limit"`
	Offset int    `form:"offset" json:"offset"`
	Title  string `form:"title" json:"title"`
	Author int    `form:"author" json:"author"`
	Series int    `form:"series" json:"series"`
	Lang   string `form:"lang" json:"lang"`
}

// BookDownload структура для запроса файла книги
type BookDownload struct {
	BookID int64  `json:"book_id" form:"book_id" binding:"required"`
	Format string `json:"format" form:"format" binding:"required"`
}

// Languages структура общего списка языков с подсчетом количества книг
type Languages []struct {
	Language      string `pg:"lang" json:"language"`
	LanguageCount int    `json:"count"`
}
