package main

import (
	"github.com/go-pg/pg/v10"
	"github.com/go-redis/redis"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/services"
	"gopds-api/sessions"
)

func initializeDatabase() *pg.DB {
	options := &pg.Options{
		User:      cfg.Postgres.DBUser,
		Password:  cfg.Postgres.DBPass,
		Database:  cfg.Postgres.DBName,
		Addr:      cfg.Postgres.DBHost,
		OnConnect: database.DisableJIT,
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

// initializePreviewService connects the preview cache to its configured
// Redis and assembles the preview service from configuration. A failed ping
// is logged, not fatal — unlike the session store, the preview is designed
// to degrade: the service pings the cache on every request and refuses
// previews while the backend is down, so a Redis outage costs the preview
// feature instead of taking the whole server with it at startup.
func initializePreviewService() *services.PreviewService {
	previewRedis := redis.NewClient(&redis.Options{
		Addr:     cfg.GetPreviewRedisAddress(),
		Password: cfg.GetPreviewRedisPassword(),
		DB:       cfg.Preview.Redis.DB,
	})
	if _, err := previewRedis.Ping().Result(); err != nil {
		logging.Errorf("Preview Redis %s (DB %d) unavailable: %v — previews will be refused until it recovers",
			cfg.GetPreviewRedisAddress(), cfg.Preview.Redis.DB, err)
	} else {
		logging.Infof("Preview Redis connection established (%s, DB %d)",
			cfg.GetPreviewRedisAddress(), cfg.Preview.Redis.DB)
	}
	return services.NewPreviewServiceFromConfig(
		&cfg.Preview,
		services.NewCatalogBookRepo(),
		services.NewZipArchiveLoader(cfg.App.FilesPath),
		services.NewRedisPreviewCache(previewRedis),
	)
}
