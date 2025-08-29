package sessions

import (
	"gopds-api/config"
	"gopds-api/logging"

	"github.com/go-redis/redis"
)

var (
	rdb      *redis.Client
	rdbToken *redis.Client
)

func SetRedisConnections(mainClient, tokenClient *redis.Client) {
	rdb = mainClient
	rdbToken = tokenClient
}

func RedisConnection(dbNum int, cfg *config.Config) *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.GetRedisAddress(),
		Password: cfg.Redis.Password,
		DB:       dbNum,
	})

	// Test connection
	if _, err := client.Ping().Result(); err != nil {
		logging.Errorf("Failed to connect to Redis (DB %d): %v", dbNum, err)
		panic(err)
	}

	logging.Infof("Redis connection established (DB %d)", dbNum)
	return client
}
