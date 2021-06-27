package models

import (
	"github.com/go-pg/pg/v10/orm"
	"time"
)

func init() {
	// Register many to many model so ORM can better recognize m2m relation.
	// This should be done before dependant models are used.
	orm.RegisterTable((*OrderToAuthor)(nil))
	orm.RegisterTable((*OrderToSeries)(nil))
	orm.RegisterTable((*UserToBook)(nil))
}

type Cover struct {
	tableName   struct{} `pg:"covers,discard_unknown_columns" json:"-"`
	ID          int64    `json:"id" form:"id"`
	BookID      int64    `json:"book_id" form:"book_id"`
	Cover       string   `json:"cover" form:"cover"`
	ContentType string   `json:"content_type" form:"content_type"`
}

type Catalog struct {
	tableName struct{} `pg:"opds_catalog_catalog,discard_unknown_columns" json:"-"`
	ID        int64    `pg:"id,pk" json:"id" form:"id"`
	CatName   string   `pg:"cat_name" json:"cat_name" form:"cat_name"`
	IsScanned bool     `pg:"is_scanned" json:"is_scanned" form:"is_scanned"`
}

// Book структура книги в БД
type Book struct {
	tableName    struct{}  `pg:"opds_catalog_book,discard_unknown_columns" json:"-"`
	ID           int64     `pg:"id" json:"id"`
	Path         string    `pg:"path" json:"path"`
	Format       string    `pg:"format" json:"format"`
	FileName     string    `pg:"filename" json:"filename"`
	RegisterDate time.Time `pg:"registerdate" json:"registerdate"`
	DocDate      string    `pg:"docdate,use_zero" json:"docdate"`
	Lang         string    `pg:"lang,use_zero" json:"lang"`
	Title        string    `pg:"title" json:"title"`
	Cover        bool      `pg:"cover" json:"cover"`
	Annotation   string    `pg:"annotation" json:"annotation"`
	Fav          bool      `pg:"-" json:"fav"`
	Approved     bool      `pg:"approved" json:"approved"`
	Authors      []Author  `pg:"many2many:opds_catalog_bauthor,join_fk:author_id" json:"authors"`
	Series       []*Series `pg:"many2many:opds_catalog_bseries,join_fk:ser_id" json:"series"`
	Users        []*User   `pg:"many2many:favorite_books,join_fk:book_id" json:"favorites"`
	Covers       []*Cover  `pg:"covers,rel:has-many" json:"covers"`
}

// Author структура автора в БД
type Author struct {
	tableName struct{} `pg:"opds_catalog_author,discard_unknown_columns" json:"-"`
	ID        int64    `json:"id" form:"id"`
	FullName  string   `json:"full_name" form:"full_name"`
}

// OrderToAuthor структура для many2many связи книг и авторов
type OrderToAuthor struct {
	tableName struct{} `pg:"opds_catalog_bauthor,discard_unknown_columns" json:"-"`
	AuthorID  int64
	BookID    int64
}

// UserToBook структура для many2many связи книг и пользователей для избранного
type UserToBook struct {
	tableName struct{} `pg:"favorite_books,discard_unknown_columns" json:"-"`
	ID        int64    `pg:"id" json:"id"`
	UserID    int64    `pg:"user_id" json:"user_id"`
	BookID    int64    `pg:"book_id" json:"book_id"`
}

// Series структура серии книг
type Series struct {
	tableName struct{} `pg:"opds_catalog_series,discard_unknown_columns" json:"-"`
	ID        int64    `pg:"id" json:"id"`
	SerNo     int64    `json:"ser_no" pg:"-"`
	Ser       string   `pg:"ser,use_zero" json:"ser"`
	LangCode  int      `pg:"lang_code,use_zero" json:"lang_code,default:0"`
}

// OrderToSeries структура связи серий и книг через many2many
type OrderToSeries struct {
	tableName struct{} `pg:"opds_catalog_bseries,discard_unknown_columns" json:"-"`
	SerNo     int64    `pg:"ser_no,use_zero"`
	SeriesID  int64    `pg:"ser_id"`
	BookID    int64    `pg:"book_id"`
}

// BookFilters фильтры для query get-запроса при фильтрации по книгам
type BookFilters struct {
	Limit      int    `form:"limit" json:"limit"`
	Offset     int    `form:"offset" json:"offset"`
	Title      string `form:"title" json:"title"`
	Author     int    `form:"author" json:"author"`
	Series     int    `form:"series" json:"series"`
	Lang       string `form:"lang" json:"lang"`
	Fav        bool   `form:"fav" json:"fav"`
	UnApproved bool   `form:"unapproved" json:"unapproved"`
}

// BookDownload структура для запроса файла книги
type BookDownload struct {
	BookID int64  `json:"book_id" form:"book_id" binding:"required"`
	Format string `json:"format" form:"format" binding:"required"`
}

// FavBook структура для добавления книги в избранное
type FavBook struct {
	BookID int64 `json:"book_id" form:"book_id" binding:"required"`
	Fav    bool  `json:"fav" form:"fav"`
}

// Languages структура общего списка языков с подсчетом количества книг
type Languages []struct {
	Language      string `pg:"lang" json:"language"`
	LanguageCount int    `json:"count"`
}
