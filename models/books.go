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
	// orm.RegisterTable((*OrderToCovers)(nil))
}

type Cover struct {
	tableName struct{} `pg:"covers,discard_unknown_columns" json:"-"`
	ID        int64    `json:"id" form:"id"`
	BookID    int64    `json:"book_id" form:"book_id"`
	Cover     string   `json:"cover" form:"cover"`
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
	Annotation   string    `pg:"annotation" json:"annotation"`
	Fav          bool      `pg:"-" json:"fav"`
	Authors      []*Author `pg:"many2many:opds_catalog_bauthor" json:"authors"`
	Series       []*Series `pg:"many2many:opds_catalog_bseries,joinFK:ser_id" json:"series"`
	Users        []*User   `pg:"many2many:favorite_books,joinFK:user_id" json:"favorites"`
	Covers       []*Cover  `pg:"covers" json:"covers"`
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
	UserID    int64
	BookID    int64
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

// BookFilters фильтры для query get-запроса при фильтрации по клубам
type BookFilters struct {
	Limit  int    `form:"limit" json:"limit"`
	Offset int    `form:"offset" json:"offset"`
	Title  string `form:"title" json:"title"`
	Author int    `form:"author" json:"author"`
	Series int    `form:"series" json:"series"`
	Lang   string `form:"lang" json:"lang"`
	Fav    bool   `form:"fav" json:"fav"`
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
