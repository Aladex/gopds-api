package sessions

import (
	"fmt"
	"github.com/go-redis/redis"
	"github.com/spf13/viper"
)

var (
	rdb      = RedisConnection(0)
	rdbToken = RedisConnection(1)
)

// RedisConnection creates a connection to a Redis database.
func RedisConnection(dbNum int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%v:%v",
			viper.GetString("redis.host"),
			viper.GetString("redis.port")),
		Password: "", // no password set
		DB:       dbNum,
	})
}
