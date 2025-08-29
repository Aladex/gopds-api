package main

import (
	"github.com/go-pg/pg/v10"
	"github.com/go-redis/redis"
	"gopds-api/logging"
	"gopds-api/sessions"
)

func initializeDatabase() *pg.DB {
	options := &pg.Options{
		User:     cfg.Postgres.DBUser,
		Password: cfg.Postgres.DBPass,
		Database: cfg.Postgres.DBName,
		Addr:     cfg.Postgres.DBHost,
	}
	db := pg.Connect(options)
	if _, err := db.Exec("SELECT 1"); err != nil {
		logging.Errorf("Failed to connect to database: %v", err)
		panic(err)
	}
	logging.Info("Database connection established successfully")
	return db
}

func closeDatabaseConnection(db *pg.DB) {
	if err := db.Close(); err != nil {
		logging.Errorf("Error closing database connection: %v", err)
	}
}

func initializeSessionManagement() (*redis.Client, *redis.Client) {
	mainRedisClient := sessions.RedisConnection(0, cfg)
	tokenRedisClient := sessions.RedisConnection(1, cfg)
	return mainRedisClient, tokenRedisClient
}
