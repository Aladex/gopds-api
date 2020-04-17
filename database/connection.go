package database

import (
	"github.com/go-pg/pg/v9"
	"github.com/spf13/viper"
	"gopds-api/logging"
)

var db *pg.DB
var customLog = logging.SetLog()

func init() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		customLog.Fatalf("Fatal error config file: %s \n", err)
	}
	db = pgConn()
}

// Функция возвращает подключение к БД
func pgConn() *pg.DB {
	db := pg.Connect(&pg.Options{
		User:     viper.GetString("postgres.dbuser"),
		Password: viper.GetString("postgres.dbpass"),
		Database: viper.GetString("postgres.dbname"),
		Addr:     viper.GetString("postgres.dbhost"),
	})

	var n int

	// Checking for connection
	_, err := db.QueryOne(pg.Scan(&n), "SELECT 1")
	if err != nil {
		panic(err)
	}
	return db
}
