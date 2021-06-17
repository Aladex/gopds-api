package database

import (
	"github.com/go-pg/pg/v9"
	"gopds-api/config"
)

var db *pg.DB

func init() {
	db = pgConn()
}

// Функция возвращает подключение к БД
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
		panic(err)
	}
	return db
}
