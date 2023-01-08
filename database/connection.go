package database

import (
	"github.com/go-pg/pg/v10"
	"gopds-api/config"
	"log"
)

var db *pg.DB

func init() {
	db = pgConn()
}

// pgConn func for connect to postgres
func pgConn() *pg.DB {
	db := pg.Connect(&pg.Options{
		User:     config.AppConfig.GetString("postgres.dbuser"),
		Password: config.AppConfig.GetString("postgres.dbpass"),
		Database: config.AppConfig.GetString("postgres.dbname"),
		Addr:     config.AppConfig.GetString("postgres.dbhost"),
	})

	var n int

	// Checking for connection
	_, err := db.QueryOne(pg.Scan(&n), "SELECT 1")
	if err != nil {
		log.Fatalln("Connection to database failed:", err)
	}
	return db
}
