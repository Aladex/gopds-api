package database

import (
	"github.com/go-pg/pg/v10"
)

func SetDB(connection *pg.DB) {
	db = connection
}

func GetDB() *pg.DB {
	return db
}

var db *pg.DB
