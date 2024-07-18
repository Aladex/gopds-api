package database

import (
	"github.com/go-pg/pg/v10"
)

func SetDB(connection *pg.DB) {
	db = connection
}

var db *pg.DB
