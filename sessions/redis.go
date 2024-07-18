package sessions

import (
	"fmt"
	"github.com/go-redis/redis"
	"github.com/spf13/viper"
)

var (
	rdb      *redis.Client
	rdbToken *redis.Client
)

func SetRedisConnections(mainClient, tokenClient *redis.Client) {
	rdb = mainClient
	rdbToken = tokenClient
}

func RedisConnection(dbNum int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%v:%v",
			viper.GetString("redis.host"),
			viper.GetString("redis.port")),
		Password: "", // no password set
		DB:       dbNum,
	})
}
