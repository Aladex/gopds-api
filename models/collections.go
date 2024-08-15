package models

import (
	"github.com/go-pg/pg/v10"
	"time"
)

// CollectionVote struct for collection votes
type CollectionVote struct {
	tableName    struct{}  `pg:"collection_votes,discard_unknown_columns" json:"-"`
	ID           int64     `pg:"id,pk" json:"id"`
	UserID       int64     `pg:"user_id" json:"user_id"`
	CollectionID int64     `pg:"collection_id" json:"collection_id"`
	Vote         bool      `pg:"vote,use_zero" json:"vote"`
	CreatedAt    time.Time `pg:"created_at" json:"created_at"`
	UpdatedAt    time.Time `pg:"updated_at" json:"updated_at"`
}

// BookCollection struct for book_collections table
type BookCollection struct {
	tableName          struct{}  `pg:"book_collections,discard_unknown_columns" json:"-"`
	ID                 int64     `pg:"id,pk" json:"id"`
	UserID             int64     `pg:"user_id" json:"user_id"`
	User               *User     `pg:"rel:has-one,fk:user_id" json:"-"`
	Name               string    `pg:"name" json:"name"`
	IsPublic           bool      `pg:"is_public,use_zero" json:"is_public"`
	CreatedAt          time.Time `pg:"created_at" json:"created_at"`
	UpdatedAt          time.Time `pg:"updated_at" json:"updated_at"`
	Books              []Book    `pg:"many2many:book_collection_books,join_fk:book_id" json:"-"`
	BookIsInCollection bool      `pg:"-" json:"book_is_in_collection"`
	BookIDs            []int64   `pg:"-" json:"book_ids"`
	VoteCount          int       `pg:"-" json:"vote_count"`
}

func (bc *BookCollection) FetchBookIDs(db *pg.DB) error {
	var bookIDs []int64
	err := db.Model((*BookCollectionBook)(nil)).
		Column("book_id").
		Where("book_collection_id = ?", bc.ID).
		Select(&bookIDs)
	if err != nil {
		return err
	}
	bc.BookIDs = bookIDs
	return nil
}

// BookCollectionBook struct for many-to-many relation between books and book collections
type BookCollectionBook struct {
	tableName        struct{}  `pg:"book_collection_books,discard_unknown_columns" json:"-"`
	ID               int64     `pg:"id,pk" json:"id"`
	BookCollectionID int64     `pg:"book_collection_id" json:"book_collection_id"`
	BookID           int64     `pg:"book_id" json:"book_id"`
	Position         int       `pg:"position,default:0" json:"position"`
	CreatedAt        time.Time `pg:"created_at,default:now()" json:"created_at"`
	UpdatedAt        time.Time `pg:"updated_at,default:now()" json:"updated_at"`
}
