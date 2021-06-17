package sessions

import (
	"fmt"
	"github.com/go-redis/redis"
	"gopds-api/config"
)

var rdb *redis.Client
var rdbToken *redis.Client

func init() {
	rdb = RedisConnection(0)
	rdbToken = RedisConnection(1)
}

// RedisConnection Connection to redis-master to DB
func RedisConnection(dbNum int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%v:%v", config.AppConfig.GetString("redis.host"), config.AppConfig.GetString("redis.port")),
		Password: "",    // no password set
		DB:       dbNum, // use default DB
	})
}
