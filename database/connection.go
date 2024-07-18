package database

import (
	"github.com/go-pg/pg/v10"
	"github.com/spf13/viper"
	"log"
)

func ConnectDB() *pg.DB {
	options := &pg.Options{
		User:     viper.GetString("postgres.dbuser"),
		Password: viper.GetString("postgres.dbpass"),
		Database: viper.GetString("postgres.dbname"),
		Addr:     viper.GetString("postgres.dbhost"),
	}
	db := pg.Connect(options)
	if _, err := db.Exec("SELECT 1"); err != nil {
		log.Fatalln("Failed to connect to database:", err)
	}
	return db
}

var db = ConnectDB()
