package sessions

import (
	"fmt"
	"github.com/go-redis/redis"
	"gopds-api/config"
)

var (
	rdb      = RedisConnection(0)
	rdbToken = RedisConnection(1)
)

// RedisConnection creates a connection to a Redis database.
func RedisConnection(dbNum int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%v:%v",
			config.AppConfig.GetString("redis.host"),
			config.AppConfig.GetString("redis.port")),
		Password: "", // no password set
		DB:       dbNum,
	})
}
