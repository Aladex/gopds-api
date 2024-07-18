package main

import (
	"github.com/go-pg/pg/v10"
	"github.com/go-redis/redis"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopds-api/sessions"
)

func initializeDatabase() *pg.DB {
	options := &pg.Options{
		User:     viper.GetString("postgres.dbuser"),
		Password: viper.GetString("postgres.dbpass"),
		Database: viper.GetString("postgres.dbname"),
		Addr:     viper.GetString("postgres.dbhost"),
	}
	db := pg.Connect(options)
	if _, err := db.Exec("SELECT 1"); err != nil {
		logrus.Fatalln("Failed to connect to database:", err)
	}
	return db
}

func closeDatabaseConnection(db *pg.DB) {
	if err := db.Close(); err != nil {
		logrus.Println("Error closing database connection:", err)
	}
}

func initializeSessionManagement() (*redis.Client, *redis.Client) {
	mainRedisClient := sessions.RedisConnection(0)
	tokenRedisClient := sessions.RedisConnection(1)
	return mainRedisClient, tokenRedisClient
}
